package core

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"woma/internal/config"
	"woma/internal/git"
)

// CreateOptions configure CreateWorktree.
type CreateOptions struct {
	Branch    string
	Base      string // create the branch from this ref instead of HEAD
	Path      string // override the configured location
	NoCopy    bool
	NoInstall bool
	Log       io.Writer // progress messages and install output
}

// WorktreeName turns a branch into a folder name: "feat/login" -> "feat-login".
func WorktreeName(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// CreateWorktree adds a worktree for opts.Branch to the repository that dir
// belongs to, copies env files from the main worktree and installs
// dependencies. It returns the new worktree's path.
func CreateWorktree(ctx context.Context, dir string, cfg config.Config, opts CreateOptions) (string, error) {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Branch == "" {
		return "", fmt.Errorf("a branch name is required")
	}
	repo, err := git.LoadRepo(ctx, dir)
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}

	path := opts.Path
	if path == "" {
		path = strings.NewReplacer(
			"{repo}", repo.Path,
			"{repo_name}", repo.Name,
			"{name}", WorktreeName(opts.Branch),
		).Replace(cfg.WorktreeDir)
		path = config.ExpandHome(path)
	}
	if path, err = filepath.Abs(path); err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}

	bare := isBare(repo)
	if !bare {
		if err := excludeFromRepo(repo, path); err != nil {
			fmt.Fprintf(opts.Log, "warning: could not update .git/info/exclude: %v\n", err)
		}
	}

	base := opts.Base
	if base == "" && !git.BranchExists(dir, opts.Branch) {
		base = "HEAD"
	}
	if err := git.AddWorktree(dir, path, opts.Branch, base); err != nil {
		return "", err
	}
	fmt.Fprintf(opts.Log, "created worktree %s (branch %s)\n", path, opts.Branch)

	if !opts.NoCopy && !bare {
		for _, rel := range copyFiles(repo.Path, path, cfg.Copy) {
			fmt.Fprintf(opts.Log, "copied %s\n", rel)
		}
	}
	if !opts.NoInstall {
		install(ctx, path, cfg.Install, opts.Log)
	}
	return path, nil
}

func isBare(repo git.Repo) bool {
	info, err := os.Lstat(filepath.Join(repo.Path, ".git"))
	return err != nil || !info.IsDir()
}

// excludeFromRepo keeps a worktree nested inside the main worktree from
// showing up as untracked, via the local (uncommitted) .git/info/exclude.
func excludeFromRepo(repo git.Repo, path string) error {
	rel, err := filepath.Rel(repo.Path, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return nil
	}
	entry := "/" + filepath.ToSlash(strings.Split(rel, string(filepath.Separator))[0]) + "/"
	exclude := filepath.Join(repo.CommonDir, "info", "exclude")
	b, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if slices.Contains(strings.Split(string(b), "\n"), entry) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		entry = "\n" + entry
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry + "\n")
	return err
}

// copyFiles copies files whose name matches patterns from src into the same
// relative location in dst, when missing there. Nested repositories,
// worktrees and node_modules are skipped. It returns the copied paths.
func copyFiles(src, dst string, patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	var copied []string
	_ = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == src {
				return nil
			}
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return fs.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return fs.SkipDir // nested repository or worktree
			}
			return nil
		}
		if !d.Type().IsRegular() || !matchesAny(d.Name(), patterns) {
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if _, err := os.Lstat(target); err == nil {
			return nil // tracked or already there
		}
		if _, err := os.Stat(filepath.Dir(target)); err != nil {
			return nil // folder doesn't exist in the new checkout
		}
		if copyFile(path, target) == nil {
			copied = append(copied, rel)
		}
		return nil
	})
	return copied
}

func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// lockfiles maps lockfiles to the command that installs dependencies.
var lockfiles = []struct{ file, cmd string }{
	{"pnpm-lock.yaml", "pnpm install"},
	{"bun.lock", "bun install"},
	{"bun.lockb", "bun install"},
	{"yarn.lock", "yarn install"},
	{"package-lock.json", "npm install"},
}

// InstallCommand returns the command to install dependencies in dir: the
// configured one, or one detected from lockfiles. Empty means none.
func InstallCommand(dir, configured string) string {
	switch configured {
	case "none":
		return ""
	case "":
		for _, l := range lockfiles {
			if _, err := os.Stat(filepath.Join(dir, l.file)); err == nil {
				return l.cmd
			}
		}
		return ""
	}
	return configured
}

func install(ctx context.Context, dir, configured string, log io.Writer) {
	command := InstallCommand(dir, configured)
	if command == "" {
		return
	}
	if fields := strings.Fields(command); configured == "" {
		if _, err := exec.LookPath(fields[0]); err != nil {
			fmt.Fprintf(log, "skipping install: %s not found\n", fields[0])
			return
		}
	}
	fmt.Fprintf(log, "running %s\n", command)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(log, "warning: %s failed: %v\n", command, err)
	}
}
