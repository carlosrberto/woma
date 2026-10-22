package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"woma/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.IsolateGit()
	os.Exit(m.Run())
}

func TestDiscover(t *testing.T) {
	root := testutil.TempDir(t)
	outside := testutil.TempDir(t)

	// Repo with a nested, locked worktree and a detached one outside root.
	api := filepath.Join(root, "api")
	testutil.NewRepo(t, api)
	testutil.Git(t, api, "worktree", "add", "-q", ".worktrees/feature-x", "-b", "feature-x")
	testutil.Git(t, api, "worktree", "lock", ".worktrees/feature-x")
	testutil.Git(t, api, "worktree", "add", "-q", "--detach", filepath.Join(outside, "api-detached"))

	// Repo without worktrees is ignored.
	testutil.NewRepo(t, filepath.Join(root, "plain"))

	// Repo outside root whose worktree is inside root is still found.
	web := filepath.Join(outside, "web")
	testutil.NewRepo(t, web)
	testutil.Git(t, web, "worktree", "add", "-q", filepath.Join(root, "web-redesign"), "-b", "redesign")

	// Repos inside node_modules are skipped.
	dep := filepath.Join(root, "node_modules", "dep")
	testutil.NewRepo(t, dep)
	testutil.Git(t, dep, "worktree", "add", "-q", filepath.Join(outside, "dep-wt"), "-b", "x")

	var p ScanProgress
	repos, err := Discover(context.Background(), []string{root}, ScanOptions{}, &p)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("want 2 repos, got %d: %+v", len(repos), repos)
	}
	if repos[0].Name != "api" || len(repos[0].Worktrees) != 2 {
		t.Fatalf("repo api: %+v", repos[0])
	}
	if wt := repos[0].Worktrees[0]; wt.Branch != "feature-x" || !wt.Locked {
		t.Errorf("feature-x worktree: %+v", wt)
	}
	if wt := repos[0].Worktrees[1]; wt.Branch != "" || wt.Head == "" {
		t.Errorf("detached worktree: %+v", wt)
	}
	if repos[1].Name != "web" || repos[1].Worktrees[0].Path != filepath.Join(root, "web-redesign") {
		t.Fatalf("repo web: %+v", repos[1])
	}
	if got := p.Found.Load(); got != 3 { // api, plain, web
		t.Errorf("found %d git dirs, want 3", got)
	}
	if p.Dirs.Load() == 0 {
		t.Error("no directories counted")
	}
}

func TestDiscoverBareRepo(t *testing.T) {
	root := testutil.TempDir(t)
	src := filepath.Join(root, "src")
	testutil.NewRepo(t, src)
	testutil.Git(t, root, "clone", "-q", "--bare", src, "app.git")
	testutil.Git(t, filepath.Join(root, "app.git"), "worktree", "add", "-q", filepath.Join(root, "app-main"), "main")
	os.RemoveAll(src)

	repos, err := Discover(context.Background(), []string{root}, ScanOptions{}, &ScanProgress{})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Name != "app" || len(repos[0].Worktrees) != 1 {
		t.Fatalf("bare repo: %+v", repos)
	}
}

func TestDiscoverMissingWorktree(t *testing.T) {
	root := testutil.TempDir(t)
	repo := filepath.Join(root, "api")
	testutil.NewRepo(t, repo)
	testutil.Git(t, repo, "worktree", "add", "-q", filepath.Join(root, "gone"), "-b", "gone")
	os.RemoveAll(filepath.Join(root, "gone"))

	repos, err := Discover(context.Background(), []string{root}, ScanOptions{}, &ScanProgress{})
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || !repos[0].Worktrees[0].Prunable {
		t.Fatalf("want one prunable worktree: %+v", repos)
	}
	if err := RemoveWorktree(repos[0].CommonDir, repos[0].Worktrees[0].Path); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, []string{testutil.TempDir(t)}, ScanOptions{}, &ScanProgress{}); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestRemoveWorktreeRefusesMain(t *testing.T) {
	repo := filepath.Join(testutil.TempDir(t), "api")
	testutil.NewRepo(t, repo)
	if err := RemoveWorktree(filepath.Join(repo, ".git"), repo); err == nil {
		t.Fatal("removing the main worktree must fail")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal("main worktree was damaged")
	}
}

func TestParsePorcelain(t *testing.T) {
	out := "worktree /repos/app.git\x00bare\x00\x00" +
		"worktree /repos/app-a\x00HEAD abc\x00branch refs/heads/feat/a\x00\x00" +
		"worktree /repos/app-b\x00HEAD def\x00detached\x00locked reason here\x00prunable gitdir points nowhere\x00\x00"
	wts := parsePorcelain([]byte(out))
	if len(wts) != 3 {
		t.Fatalf("got %d entries", len(wts))
	}
	if wts[1].Branch != "feat/a" || wts[1].Head != "abc" {
		t.Errorf("entry a: %+v", wts[1])
	}
	if b := wts[2]; b.Branch != "" || !b.Locked || !b.Prunable {
		t.Errorf("entry b: %+v", b)
	}
	if got := repoName(wts[0].Path); got != "app" {
		t.Errorf("repoName = %q", got)
	}
}

func TestDiscoverEachReportsCurrentRepoFirst(t *testing.T) {
	root := testutil.TempDir(t)
	app := filepath.Join(root, "app")
	testutil.NewRepo(t, app)
	testutil.Git(t, app, "worktree", "add", "-q", ".worktrees/feature-x", "-b", "feature-x")
	// Repositories nested below, found by walking.
	for _, name := range []string{"aaa-lib", "zzz-tool"} {
		dir := filepath.Join(app, "packages", name)
		testutil.NewRepo(t, dir)
		testutil.Git(t, dir, "worktree", "add", "-q", dir+"-wt", "-b", "x")
	}

	// Started from a subfolder of the repository.
	start := filepath.Join(app, "packages")
	var order []string
	var current []bool
	err := DiscoverEach(context.Background(), []string{start}, ScanOptions{}, &ScanProgress{}, func(r Repo, cur bool) {
		order = append(order, r.Name)
		current = append(current, cur)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "app" || !current[0] || current[1] || current[2] {
		t.Fatalf("order = %v current = %v", order, current)
	}
}
