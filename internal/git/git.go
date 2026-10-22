// Package git discovers repositories and their linked worktrees and wraps the
// git commands woma runs on them.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Worktree is a linked (non-main) worktree of a repository.
type Worktree struct {
	Path     string
	Branch   string // short branch name; empty when HEAD is detached
	Head     string
	Locked   bool
	Prunable bool // the worktree directory no longer exists
}

// Repo is a repository that has at least one linked worktree.
type Repo struct {
	Name      string
	Path      string // main worktree, or the repository dir itself when bare
	CommonDir string // the shared git dir (".git" for non-bare repositories)
	// BaseBranch is the default branch (e.g. "main") and BaseRefs the refs
	// that represent it locally and on the remote. Both may be empty.
	BaseBranch string
	BaseRefs   []string
	Worktrees  []Worktree
}

// ScanProgress is updated by Discover and can be read concurrently.
type ScanProgress struct {
	Dirs    atomic.Int64
	Found   atomic.Int64
	current atomic.Pointer[string]
}

// Current returns the directory being visited.
func (p *ScanProgress) Current() string {
	if s := p.current.Load(); s != nil {
		return *s
	}
	return ""
}

// ScanOptions tune Discover.
type ScanOptions struct {
	// Ignore holds directory names or globs to skip, besides node_modules.
	Ignore []string
	// MaxDepth limits how many directory levels below a root are visited;
	// 0 means no limit.
	MaxDepth int
}

func (o ScanOptions) skip(name string) bool {
	if name == "node_modules" {
		return true
	}
	for _, pattern := range o.Ignore {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// Discover walks the roots looking for git repositories and returns the ones
// that have linked worktrees, sorted by name. Worktrees living outside the
// roots are included too, since they are listed by their repository.
func Discover(ctx context.Context, roots []string, opts ScanOptions, p *ScanProgress) ([]Repo, error) {
	var repos []Repo
	err := DiscoverEach(ctx, roots, opts, p, func(r Repo, _ bool) { repos = append(repos, r) })
	if err != nil {
		return nil, err
	}
	sort.Slice(repos, func(i, j int) bool {
		if repos[i].Name != repos[j].Name {
			return repos[i].Name < repos[j].Name
		}
		return repos[i].Path < repos[j].Path
	})
	return repos, nil
}

// DiscoverEach is Discover reporting each repository through emit as soon
// as it is loaded, instead of after the whole walk. Repositories that
// contain a root (you ran woma inside them) are reported first, before the
// walk starts, with current set. emit is never called concurrently.
func DiscoverEach(ctx context.Context, roots []string, opts ScanOptions, p *ScanProgress, emit func(repo Repo, current bool)) error {
	var mu sync.Mutex
	seen := map[string]bool{}
	claim := func(dir string) (string, bool) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		mu.Lock()
		defer mu.Unlock()
		if seen[dir] {
			return "", false
		}
		seen[dir] = true
		p.Found.Add(1)
		return dir, true
	}
	var emitMu sync.Mutex
	report := func(dir string, current bool) {
		if repo, err := loadRepo(ctx, dir); err == nil && len(repo.Worktrees) > 0 {
			emitMu.Lock()
			defer emitMu.Unlock()
			emit(repo, current)
		}
	}

	// The repository you're in is one git call away; show it right away.
	for _, root := range roots {
		if out, err := output(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
			if dir, ok := claim(strings.TrimSpace(out)); ok {
				report(dir, true)
			}
		}
	}

	// Load repositories in parallel while the walk continues; most have no
	// linked worktrees.
	jobs := make(chan string, 64)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for dir := range jobs {
				report(dir, false)
			}
		}()
	}
	add := func(dir string) {
		if dir, ok := claim(dir); ok {
			select {
			case jobs <- dir:
			case <-ctx.Done():
			}
		}
	}

	walkErr := walk(ctx, roots, opts, p, add)
	close(jobs)
	wg.Wait()
	if walkErr != nil {
		return walkErr
	}
	return ctx.Err()
}

// depth is how many directory levels path is below root.
func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func checkGitEntry(path string, add func(string)) {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
	case info.IsDir():
		add(path)
	case info.Mode().IsRegular():
		if dir, ok := commonDirFromGitFile(path); ok {
			add(dir)
		}
	}
}

func isBareRepo(dir string) bool {
	for _, name := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// commonDirFromGitFile resolves a ".git" file ("gitdir: <path>") to the
// repository's common git dir.
func commonDirFromGitFile(gitFile string) (string, bool) {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return "", false
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", false
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(gitFile), gitDir)
	}
	// Linked worktrees point at <common>/worktrees/<name>, which has a
	// "commondir" file. Submodules don't; their gitdir is the repository.
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		return filepath.Clean(common), true
	}
	return filepath.Clean(gitDir), true
}

// LoadRepo reads the worktrees of the repository that dir belongs to.
func LoadRepo(ctx context.Context, dir string) (Repo, error) {
	out, err := output(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Repo{}, err
	}
	return loadRepo(ctx, strings.TrimSpace(out))
}

func loadRepo(ctx context.Context, commonDir string) (Repo, error) {
	out, err := output(ctx, commonDir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return Repo{}, err
	}
	wts := parsePorcelain([]byte(out))
	if len(wts) == 0 {
		return Repo{}, fmt.Errorf("no worktrees reported for %s", commonDir)
	}
	main := wts[0]
	repo := Repo{
		Name:      repoName(main.Path),
		Path:      main.Path,
		CommonDir: commonDir,
		Worktrees: wts[1:],
	}
	if len(repo.Worktrees) > 0 {
		repo.BaseBranch, repo.BaseRefs = baseRefs(ctx, commonDir)
	}
	return repo, nil
}

// baseRefs finds the default branch: origin/HEAD when set, plus the matching
// local branch (falling back to main or master).
func baseRefs(ctx context.Context, commonDir string) (string, []string) {
	var branch string
	var refs []string
	if out, err := output(ctx, commonDir, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(out)
		refs = append(refs, ref)
		branch = strings.TrimPrefix(ref, "refs/remotes/origin/")
	}
	for _, b := range []string{branch, "main", "master"} {
		if b != "" && refExists(ctx, commonDir, "refs/heads/"+b) {
			refs = append(refs, "refs/heads/"+b)
			if branch == "" {
				branch = b
			}
			break
		}
	}
	return branch, refs
}

func refExists(ctx context.Context, dir, ref string) bool {
	_, err := output(ctx, dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// parsePorcelain parses `git worktree list --porcelain -z`. The first entry is
// always the main worktree (or the bare repository).
func parsePorcelain(out []byte) []Worktree {
	var wts []Worktree
	for _, field := range strings.Split(string(out), "\x00") {
		key, val, _ := strings.Cut(field, " ")
		if key == "worktree" {
			wts = append(wts, Worktree{Path: val})
			continue
		}
		if len(wts) == 0 {
			continue
		}
		wt := &wts[len(wts)-1]
		switch key {
		case "HEAD":
			wt.Head = val
		case "branch":
			wt.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "locked":
			wt.Locked = true
		case "prunable":
			wt.Prunable = true
		}
	}
	return wts
}

func repoName(path string) string {
	base := filepath.Base(path)
	if base == ".bare" || base == ".git" {
		base = filepath.Base(filepath.Dir(path))
	}
	return strings.TrimSuffix(base, ".git")
}

// output runs git in dir and returns stdout. Errors include git's stderr.
func output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), errors.New(msg)
		}
		return string(out), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func run(dir string, args ...string) error {
	_, err := output(context.Background(), dir, args...)
	return err
}

// RemoveWorktree removes a linked worktree and its administrative files, even
// when it is locked, dirty or its directory is already gone. Branches are kept.
func RemoveWorktree(commonDir, path string) error {
	return run(commonDir, "worktree", "remove", "--force", "--force", path)
}

// AddWorktree creates a worktree at path. When base is empty, branch is
// checked out (git creates it from a matching remote branch if needed);
// otherwise a new branch is created from base.
func AddWorktree(dir, path, branch, base string) error {
	if base == "" {
		return run(dir, "worktree", "add", path, branch)
	}
	return run(dir, "worktree", "add", "-b", branch, path, base)
}

// BranchExists reports whether branch exists locally or on any remote.
func BranchExists(dir, branch string) bool {
	ctx := context.Background()
	if refExists(ctx, dir, "refs/heads/"+branch) {
		return true
	}
	out, err := output(ctx, dir, "for-each-ref", "--format=%(refname)", "refs/remotes/*/"+branch)
	return err == nil && strings.TrimSpace(out) != ""
}

// UntrackedFiles lists files in dir that git doesn't track, including ignored
// ones. Folders without any tracked file are listed once, ending in "/".
func UntrackedFiles(dir string) ([]string, error) {
	out, err := output(context.Background(), dir, "ls-files", "--others", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// Lock locks a worktree so git won't prune or move it.
func Lock(commonDir, path string) error { return run(commonDir, "worktree", "lock", path) }

// Unlock unlocks a worktree.
func Unlock(commonDir, path string) error { return run(commonDir, "worktree", "unlock", path) }

// Move moves a worktree to a new location.
func Move(commonDir, path, newPath string) error {
	return run(commonDir, "worktree", "move", path, newPath)
}

// Repair fixes the links between a repository and its worktrees, e.g. after
// folders were moved by hand.
func Repair(repo Repo) (string, error) {
	args := []string{"worktree", "repair"}
	for _, wt := range repo.Worktrees {
		args = append(args, wt.Path)
	}
	cmd := exec.Command("git", append([]string{"-C", repo.Path}, args...)...)
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil && msg != "" {
		return "", errors.New(msg)
	}
	return msg, err
}

// DeleteBranch force-deletes a local branch. Callers must check it is safe.
func DeleteBranch(commonDir, branch string) error {
	return run(commonDir, "branch", "-D", "--", branch)
}

// CleanIgnored removes git-ignored files (dependencies, build output) from a
// worktree, keeping files that match any of the keep patterns (e.g. .env).
// With dryRun it only returns what would be removed.
func CleanIgnored(path string, keep []string, dryRun bool) ([]string, error) {
	mode := "-fdX"
	if dryRun {
		mode = "-ndX"
	}
	args := []string{"clean", mode}
	for _, k := range keep {
		args = append(args, "-e", "!"+k)
	}
	out, err := output(context.Background(), path, args...)
	var removed []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "Would remove ")
		removed = append(removed, strings.TrimPrefix(line, "Removing "))
	}
	return removed, err
}
