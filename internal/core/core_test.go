package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"woma/internal/config"
	"woma/internal/git"
	"woma/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.IsolateGit()
	os.Exit(m.Run())
}

var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func entry(mod func(*Entry)) Entry {
	e := Entry{Repo: &git.Repo{BaseBranch: "main"}, WT: git.Worktree{Branch: "feature-x", Head: "abc"}}
	e.Status.LastCommit = now.Add(-time.Hour)
	e.Status.Complete = true
	if mod != nil {
		mod(&e)
	}
	return e
}

func TestSafety(t *testing.T) {
	cases := []struct {
		name                      string
		e                         Entry
		safe, loses, deleteBranch bool
	}{
		{"active", entry(nil), false, false, true},
		{"merged", entry(func(e *Entry) { e.Status.Merged = true }), true, false, true},
		{"gone", entry(func(e *Entry) { e.Status.Gone = true }), true, false, true},
		{"gone squash-merged", entry(func(e *Entry) { e.Status.Gone, e.Status.Unpushed = true, 2 }), true, false, false},
		{"merged but dirty", entry(func(e *Entry) { e.Status.Merged, e.Status.Dirty = true, true }), false, true, true},
		{"merged but locked", entry(func(e *Entry) { e.Status.Merged, e.WT.Locked = true, true }), false, false, true},
		{"missing", entry(func(e *Entry) { e.WT.Prunable = true }), true, false, true},
		{"unpushed", entry(func(e *Entry) { e.Status.Unpushed = 3 }), false, false, false},
		{"detached orphan", entry(func(e *Entry) { e.WT.Branch, e.Status.Unpushed, e.Status.Merged = "", 1, true }), false, true, false},
		{"base branch", entry(func(e *Entry) { e.WT.Branch, e.Status.Merged = "main", true }), true, false, false},
		{"not inspected", Entry{Repo: &git.Repo{}, WT: git.Worktree{Branch: "x"}}, false, true, false},
		{"inspection failed", entry(func(e *Entry) { e.Status.Merged, e.Status.Complete = true, false }), false, true, false},
	}
	for _, c := range cases {
		if got := c.e.Safe(); got != c.safe {
			t.Errorf("%s: Safe = %v", c.name, got)
		}
		if got := c.e.LosesWork(); got != c.loses {
			t.Errorf("%s: LosesWork = %v", c.name, got)
		}
		if got := c.e.CanDeleteBranch(); got != c.deleteBranch {
			t.Errorf("%s: CanDeleteBranch = %v", c.name, got)
		}
	}
}

func TestLastActivityAndStale(t *testing.T) {
	e := entry(func(e *Entry) {
		e.Status.LastCommit = now.Add(-40 * 24 * time.Hour)
		e.Newest = now.Add(-10 * 24 * time.Hour)
	})
	if !e.LastActivity().Equal(e.Newest) {
		t.Errorf("LastActivity = %v", e.LastActivity())
	}
	if e.Stale(30*24*time.Hour, now) || !e.Stale(7*24*time.Hour, now) {
		t.Error("stale threshold wrong")
	}
	if (Entry{}).Stale(time.Hour, now) {
		t.Error("unknown activity must not be stale")
	}
	if e.Stale(0, now) {
		t.Error("zero threshold disables stale")
	}
}

func TestLabels(t *testing.T) {
	e := entry(func(e *Entry) {
		e.WT.Locked = true
		e.Status.Dirty, e.Status.Unpushed, e.Status.Gone = true, 2, true
		e.Status.LastCommit = now.Add(-60 * 24 * time.Hour)
	})
	var texts []string
	for _, l := range e.Labels(30*24*time.Hour, now) {
		texts = append(texts, l.Text)
	}
	if got := strings.Join(texts, ","); got != "locked,modified,2 unpushed,gone,stale" {
		t.Errorf("labels = %s", got)
	}
}

func TestFilter(t *testing.T) {
	old := entry(func(e *Entry) { e.Status.Merged = true; e.Status.LastCommit = now.Add(-90 * 24 * time.Hour) })
	fresh := entry(func(e *Entry) { e.Status.Merged = true })
	dirty := entry(func(e *Entry) { e.Status.Dirty = true })
	day := 24 * time.Hour

	cases := []struct {
		f    Filter
		want []bool // old, fresh, dirty
	}{
		{Filter{}, []bool{true, true, true}},
		{Filter{Merged: true}, []bool{true, true, false}},
		{Filter{Merged: true, OlderThan: 30 * day}, []bool{true, false, false}},
		{Filter{Stale: true, StaleAfter: 30 * day}, []bool{true, false, false}},
		{Filter{Clean: true}, []bool{true, true, false}},
		{Filter{Safe: true}, []bool{true, true, false}},
		{Filter{Gone: true}, []bool{false, false, false}},
	}
	for _, c := range cases {
		for i, e := range []Entry{old, fresh, dirty} {
			if got := c.f.Match(e, now); got != c.want[i] {
				t.Errorf("%+v on entry %d = %v", c.f, i, got)
			}
		}
	}
	if !(Filter{}).IsZero() || (Filter{Clean: true}).IsZero() {
		t.Error("IsZero wrong")
	}
}

// repoWithWorktrees creates root/api with merged and unpushed worktrees.
func repoWithWorktrees(t *testing.T) (string, []git.Repo) {
	root := testutil.TempDir(t)
	dir := filepath.Join(root, "api")
	testutil.NewRepo(t, dir)
	testutil.Git(t, dir, "worktree", "add", "-q", filepath.Join(root, "done"), "-b", "done")
	testutil.Git(t, dir, "worktree", "add", "-q", filepath.Join(root, "wip"), "-b", "wip")
	testutil.Commit(t, filepath.Join(root, "done"), "d.txt", "done")
	testutil.Git(t, dir, "merge", "-q", "--no-edit", "done")
	testutil.Commit(t, filepath.Join(root, "wip"), "w.txt", "work in progress")
	testutil.WriteFile(t, filepath.Join(root, "wip", "data.bin"), 64<<10)
	repos, err := git.Discover(context.Background(), []string{root}, git.ScanOptions{}, &git.ScanProgress{})
	if err != nil {
		t.Fatal(err)
	}
	return root, repos
}

func TestInspectAll(t *testing.T) {
	_, repos := repoWithWorktrees(t)
	got := map[string]Entry{}
	InspectAll(context.Background(), repos, func(e Entry) { got[filepath.Base(e.WT.Path)] = e })
	if len(got) != 2 {
		t.Fatalf("got %d entries", len(got))
	}
	if !got["done"].Status.Merged || got["wip"].Status.Unpushed != 1 || got["wip"].Size < 64<<10 {
		t.Errorf("entries: %+v", got)
	}
	if got["wip"].Newest.IsZero() {
		t.Error("Newest not measured")
	}
}

func TestRemoveWithBranch(t *testing.T) {
	_, repos := repoWithWorktrees(t)
	entries := map[string]Entry{}
	InspectAll(context.Background(), repos, func(e Entry) { entries[filepath.Base(e.WT.Path)] = e })
	dir := repos[0].Path

	var freed atomic.Int64
	r := Remove(entries["done"], true, &freed)
	if r.Err != nil || !r.BranchDeleted || git.BranchExists(dir, "done") {
		t.Errorf("done: %+v", r)
	}
	r = Remove(entries["wip"], true, &freed)
	if r.Err != nil || r.BranchDeleted || !strings.Contains(r.BranchNote, "unpushed") || !git.BranchExists(dir, "wip") {
		t.Errorf("wip: %+v", r)
	}
	if freed.Load() < 64<<10 {
		t.Errorf("freed = %d", freed.Load())
	}
	if _, err := os.Stat(entries["wip"].WT.Path); !os.IsNotExist(err) {
		t.Error("wip folder still exists")
	}
}

func TestCreateWorktree(t *testing.T) {
	root := testutil.TempDir(t)
	dir := filepath.Join(root, "web")
	testutil.NewRepo(t, dir)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env*\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "apps", "site"), 0o755)
	testutil.Commit(t, dir, "apps/site/index.txt", "site")
	testutil.Git(t, dir, "add", ".gitignore")
	testutil.Git(t, dir, "commit", "-q", "-m", "ignore")
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "apps", "site", ".env.local"), []byte("X=1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("{}"), 0o644)
	testutil.Git(t, dir, "add", "package-lock.json")
	testutil.Git(t, dir, "commit", "-q", "-m", "lock")

	cfg := config.Default()
	cfg.Install = "touch installed.marker"
	var log bytes.Buffer
	path, err := CreateWorktree(context.Background(), filepath.Join(dir, "apps"), cfg, CreateOptions{Branch: "feat/login-page", Log: &log})
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if want := filepath.Join(dir, ".worktrees", "feat-login-page"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	for _, f := range []string{".env", "apps/site/.env.local", "installed.marker"} {
		if _, err := os.Stat(filepath.Join(path, f)); err != nil {
			t.Errorf("%s missing in new worktree\n%s", f, log.String())
		}
	}
	if info, _ := os.Stat(filepath.Join(path, ".env")); info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v", info.Mode().Perm())
	}
	if info, _ := os.Lstat(filepath.Join(path, ".env")); info.Mode()&os.ModeSymlink != 0 {
		t.Error(".env must be copied, not symlinked")
	}
	exclude, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(string(exclude), "\n/.worktrees/feat-login-page/\n") != 1 {
		t.Errorf("exclude = %q", exclude)
	}
	if out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); strings.Contains(string(out), ".worktrees") {
		t.Errorf("worktree shows as untracked: %s", out)
	}

	// Existing branches are checked out instead of created.
	testutil.Git(t, dir, "branch", "existing")
	cfg.Install = "none"
	if _, err := CreateWorktree(context.Background(), dir, cfg, CreateOptions{Branch: "existing"}); err != nil {
		t.Fatal(err)
	}
	// Excluding the same path twice doesn't duplicate it.
	repo, _ := git.LoadRepo(context.Background(), dir)
	excludeFromRepo(repo, filepath.Join(dir, ".worktrees", "existing"))
	exclude, _ = os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(string(exclude), "/.worktrees/existing/") != 1 {
		t.Errorf("exclude duplicated: %q", exclude)
	}
	if _, err := CreateWorktree(context.Background(), dir, cfg, CreateOptions{Branch: "existing"}); err == nil {
		t.Error("creating over an existing path should fail")
	}
}

func TestCreateWorktreeCustomPathAndBase(t *testing.T) {
	root := testutil.TempDir(t)
	dir := filepath.Join(root, "api")
	testutil.NewRepo(t, dir)
	testutil.Git(t, dir, "tag", "v1")
	testutil.Commit(t, dir, "later.txt", "later")

	cfg := config.Default()
	cfg.WorktreeDir = filepath.Join(root, "{repo_name}-{name}")
	path, err := CreateWorktree(context.Background(), dir, cfg, CreateOptions{Branch: "hotfix/x", Base: "v1", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, "api-hotfix-x") {
		t.Errorf("path = %s", path)
	}
	if _, err := os.Stat(filepath.Join(path, "later.txt")); !os.IsNotExist(err) {
		t.Error("worktree should be based on v1")
	}
	if _, err := CreateWorktree(context.Background(), root, cfg, CreateOptions{Branch: "x"}); err == nil {
		t.Error("expected error outside a repository")
	}
}

func TestInstallCommand(t *testing.T) {
	dir := t.TempDir()
	if got := InstallCommand(dir, ""); got != "" {
		t.Errorf("no lockfile: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "yarn.lock"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), nil, 0o644)
	if got := InstallCommand(dir, ""); got != "pnpm install" {
		t.Errorf("detected %q", got)
	}
	if got := InstallCommand(dir, "none"); got != "" {
		t.Errorf("none: %q", got)
	}
	if got := InstallCommand(dir, "make deps"); got != "make deps" {
		t.Errorf("configured: %q", got)
	}
}

func TestRemoveRestoresFolderWhenGitFails(t *testing.T) {
	_, repos := repoWithWorktrees(t)
	var e Entry
	InspectAll(context.Background(), repos, func(x Entry) {
		if x.WT.Branch == "wip" {
			e = x
		}
	})
	broken := *e.Repo
	broken.CommonDir = filepath.Join(t.TempDir(), "not-a-repo")
	e.Repo = &broken
	var freed atomic.Int64
	if r := Remove(e, false, &freed); r.Err == nil {
		t.Fatal("expected git to fail")
	}
	if _, err := os.Stat(filepath.Join(e.WT.Path, "data.bin")); err != nil {
		t.Fatal("folder must be restored untouched when git fails")
	}
	if freed.Load() != 0 {
		t.Errorf("freed %d although nothing was deleted", freed.Load())
	}
	entries, _ := os.ReadDir(filepath.Dir(e.WT.Path))
	for _, de := range entries {
		if strings.Contains(de.Name(), "woma-deleting") {
			t.Errorf("leftover %s", de.Name())
		}
	}
}

func TestRemoveRefusesMainCheckout(t *testing.T) {
	_, repos := repoWithWorktrees(t)
	e := Entry{Repo: &repos[0], WT: git.Worktree{Path: repos[0].Path, Branch: "main"}}
	var freed atomic.Int64
	if r := Remove(e, false, &freed); r.Err == nil {
		t.Fatal("removing the main checkout must fail")
	}
	if _, err := os.Stat(filepath.Join(repos[0].Path, ".git")); err != nil {
		t.Fatal("main checkout was damaged")
	}
}

func TestCreateWorktreeRelativePathUsesDir(t *testing.T) {
	root := testutil.TempDir(t)
	dir := filepath.Join(root, "code", "app")
	testutil.NewRepo(t, dir)
	cfg := config.Default()
	path, err := CreateWorktree(context.Background(), dir, cfg, CreateOptions{Branch: "feat", Path: "../app-feat", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "code", "app-feat"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	cfg.WorktreeDir = "../{repo_name}-{name}"
	path, err = CreateWorktree(context.Background(), filepath.Join(dir), cfg, CreateOptions{Branch: "other", NoInstall: true})
	if err != nil || path != filepath.Join(root, "code", "app-other") {
		t.Errorf("relative worktree_dir: %s, %v", path, err)
	}
}

func TestCreateWorktreeBareRepo(t *testing.T) {
	root := testutil.TempDir(t)
	src := filepath.Join(root, "src")
	testutil.NewRepo(t, src)
	project := filepath.Join(root, "project")
	os.MkdirAll(project, 0o755)
	testutil.Git(t, project, "clone", "-q", "--bare", src, ".bare")
	os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: ./.bare\n"), 0o644)

	path, err := CreateWorktree(context.Background(), project, config.Default(), CreateOptions{Branch: "feat", NoInstall: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(project, ".worktrees", "feat"); path != want {
		t.Errorf("path = %s, want %s (not inside the bare git dir)", path, want)
	}
}
