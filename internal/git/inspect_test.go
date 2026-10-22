package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"woma/internal/testutil"
)

// inspectFixture creates a repo with a remote and worktrees in every state
// Inspect distinguishes, returning the repo and its worktrees by branch name
// ("detached" for the detached one).
func inspectFixture(t *testing.T) (*Repo, map[string]Worktree) {
	t.Helper()
	root := testutil.TempDir(t)
	dir := testutil.NewRepoWithRemote(t, root, "api")
	add := func(branch string) string {
		path := filepath.Join(root, "wt-"+branch)
		testutil.Git(t, dir, "worktree", "add", "-q", path, "-b", branch)
		return path
	}

	// merged: a commit that was merged into main.
	merged := add("merged")
	testutil.Commit(t, merged, "m.txt", "merged work")
	testutil.Git(t, dir, "merge", "-q", "--no-edit", "merged")

	// pushed: tracking branch, one commit ahead after pushing.
	pushed := add("pushed")
	testutil.Commit(t, pushed, "p.txt", "pushed work")
	testutil.Git(t, pushed, "push", "-q", "-u", "origin", "pushed")
	testutil.Commit(t, pushed, "p2.txt", "local only")

	// gone: pushed, then deleted on the remote.
	gone := add("gone")
	testutil.Commit(t, gone, "g.txt", "gone work")
	testutil.Git(t, gone, "push", "-q", "-u", "origin", "gone")
	testutil.Git(t, dir, "push", "-q", "origin", "--delete", "gone")
	testutil.Git(t, dir, "fetch", "-q", "--prune")

	// local: never pushed, dirty.
	local := add("local")
	testutil.Commit(t, local, "l.txt", "local work")
	testutil.WriteFile(t, filepath.Join(local, "scratch.txt"), 3)

	// fresh: created, never committed to.
	add("fresh")

	// detached with an orphan commit.
	detached := filepath.Join(root, "wt-detached")
	testutil.Git(t, dir, "worktree", "add", "-q", "--detach", detached)
	testutil.Commit(t, detached, "d.txt", "orphan")

	repo, err := LoadRepo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	wts := map[string]Worktree{}
	for _, wt := range repo.Worktrees {
		name := strings.TrimPrefix(filepath.Base(wt.Path), "wt-")
		wts[name] = wt
	}
	return &repo, wts
}

func TestInspect(t *testing.T) {
	repo, wts := inspectFixture(t)
	if repo.BaseBranch != "main" || len(repo.BaseRefs) != 2 {
		t.Fatalf("base = %q %v", repo.BaseBranch, repo.BaseRefs)
	}
	ctx := context.Background()
	st := func(name string) Status { return Inspect(ctx, repo, wts[name]) }

	if s := st("merged"); !s.Merged || s.Unpushed != 0 || s.Dirty {
		t.Errorf("merged: %+v", s)
	}
	if s := st("pushed"); s.Merged || s.Upstream != "origin/pushed" || s.Ahead != 1 || s.Unpushed != 1 || s.Gone {
		t.Errorf("pushed: %+v", s)
	}
	if s := st("gone"); !s.Gone || s.Merged || s.Unpushed != 1 {
		t.Errorf("gone: %+v", s)
	}
	s := st("local")
	if !s.Dirty || s.NumChanges != 1 || s.Changes[0] != "?? scratch.txt" || s.Unpushed != 1 || s.Upstream != "" {
		t.Errorf("local: %+v", s)
	}
	if s.Subject != "local work" || time.Since(s.LastCommit) > time.Hour {
		t.Errorf("local last commit: %q %v", s.Subject, s.LastCommit)
	}
	if s := st("fresh"); s.Merged || !s.NoCommits || s.Unpushed != 0 {
		t.Errorf("fresh: %+v", s)
	}
	if s := st("detached"); s.Unpushed != 1 || s.Merged {
		t.Errorf("detached: %+v", s)
	}
}

func TestInspectMissingWorktree(t *testing.T) {
	repo, wts := inspectFixture(t)
	wt := wts["merged"]
	os.RemoveAll(wt.Path)
	wt.Prunable = true
	if s := Inspect(context.Background(), repo, wt); !s.Merged || s.Dirty {
		t.Errorf("missing merged worktree: %+v", s)
	}
}

func TestParseTrack(t *testing.T) {
	for in, want := range map[string][3]int{
		"": {0, 0, 0}, "[gone]": {1, 0, 0}, "[ahead 2]": {0, 2, 0},
		"[behind 3]": {0, 0, 3}, "[ahead 1, behind 4]": {0, 1, 4},
	} {
		gone, a, b := parseTrack(in)
		g := 0
		if gone {
			g = 1
		}
		if [3]int{g, a, b} != want {
			t.Errorf("parseTrack(%q) = %v %d %d", in, gone, a, b)
		}
	}
}

func TestScanOptions(t *testing.T) {
	root := testutil.TempDir(t)
	for _, p := range []string{"shallow/api", "deep/a/b/c/web", "vendor/lib"} {
		dir := filepath.Join(root, p)
		testutil.NewRepo(t, dir)
		testutil.Git(t, dir, "worktree", "add", "-q", dir+"-wt", "-b", "x")
	}
	names := func(opts ScanOptions) string {
		repos, err := Discover(context.Background(), []string{root}, opts, &ScanProgress{})
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, r := range repos {
			n = append(n, r.Name)
		}
		return strings.Join(n, ",")
	}
	if got := names(ScanOptions{}); got != "api,lib,web" {
		t.Errorf("no options: %s", got)
	}
	if got := names(ScanOptions{Ignore: []string{"vend*"}}); got != "api,web" {
		t.Errorf("ignore: %s", got)
	}
	if got := names(ScanOptions{MaxDepth: 2}); got != "api,lib" {
		t.Errorf("max depth 2: %s", got)
	}
}

func TestDiscoverMultipleRoots(t *testing.T) {
	a, b := testutil.TempDir(t), testutil.TempDir(t)
	for _, dir := range []string{filepath.Join(a, "api"), filepath.Join(b, "web")} {
		testutil.NewRepo(t, dir)
		testutil.Git(t, dir, "worktree", "add", "-q", dir+"-wt", "-b", "x")
	}
	// Overlapping roots must not duplicate repositories.
	repos, err := Discover(context.Background(), []string{a, b, a}, ScanOptions{}, &ScanProgress{})
	if err != nil || len(repos) != 2 {
		t.Fatalf("repos = %+v, %v", repos, err)
	}
}

func TestWorktreeOperations(t *testing.T) {
	root := testutil.TempDir(t)
	dir := filepath.Join(root, "api")
	testutil.NewRepo(t, dir)
	wt := filepath.Join(root, "wt")
	if err := AddWorktree(dir, wt, "feature-x", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if !BranchExists(dir, "feature-x") || BranchExists(dir, "nope") {
		t.Fatal("BranchExists wrong")
	}
	common := filepath.Join(dir, ".git")
	load := func() Worktree {
		repo, err := LoadRepo(context.Background(), dir)
		if err != nil || len(repo.Worktrees) != 1 {
			t.Fatalf("load: %+v %v", repo, err)
		}
		return repo.Worktrees[0]
	}

	if err := Lock(common, wt); err != nil || !load().Locked {
		t.Fatalf("lock: %v", err)
	}
	if err := Move(common, wt, wt+"-moved"); err == nil {
		t.Fatal("moving a locked worktree should fail")
	}
	if err := Unlock(common, wt); err != nil || load().Locked {
		t.Fatalf("unlock: %v", err)
	}
	if err := Move(common, wt, wt+"-moved"); err != nil || load().Path != wt+"-moved" {
		t.Fatalf("move: %v", err)
	}

	// Moving the folder by hand breaks the link until repaired.
	os.Rename(wt+"-moved", wt)
	repo, _ := LoadRepo(context.Background(), dir)
	repo.Worktrees[0].Path = wt
	if _, err := Repair(repo); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if got := load(); got.Path != wt || got.Prunable {
		t.Fatalf("after repair: %+v", got)
	}

	if err := RemoveWorktree(common, wt); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBranch(common, "feature-x"); err != nil || BranchExists(dir, "feature-x") {
		t.Fatalf("delete branch: %v", err)
	}
}

func TestCleanIgnored(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "web")
	testutil.NewRepo(t, dir)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\ndist/\n.env*\n"), 0o644)
	testutil.Git(t, dir, "add", ".gitignore")
	testutil.Git(t, dir, "commit", "-q", "-m", "ignore")
	testutil.WriteFile(t, filepath.Join(dir, "node_modules/x/index.js"), 10)
	testutil.WriteFile(t, filepath.Join(dir, "dist/app.js"), 10)
	testutil.WriteFile(t, filepath.Join(dir, ".env"), 10)
	testutil.WriteFile(t, filepath.Join(dir, "apps/site/.env.local"), 10)
	testutil.WriteFile(t, filepath.Join(dir, "notes.txt"), 10) // untracked, not ignored

	keep := []string{".env", ".env.*"}
	preview, err := CleanIgnored(dir, keep, true)
	if err != nil || strings.Join(preview, ",") != "dist/,node_modules/" {
		t.Fatalf("preview = %v, %v", preview, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist")); err != nil {
		t.Fatal("dry run removed files")
	}
	if _, err := CleanIgnored(dir, keep, false); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{"dist": false, "node_modules": false, ".env": true, "apps/site/.env.local": true, "notes.txt": true} {
		_, err := os.Stat(filepath.Join(dir, path))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", path, err == nil, want)
		}
	}
}
