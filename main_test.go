package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlosrberto/woma/internal/git"
	"github.com/carlosrberto/woma/internal/testutil"
	"github.com/carlosrberto/woma/internal/tui"
)

func TestMain(m *testing.M) {
	testutil.IsolateGit()
	// Never read the user's real config.
	os.Setenv("WOMA_CONFIG", filepath.Join(os.TempDir(), "woma-test-no-config.toml"))
	os.Exit(m.Run())
}

func woma(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(args, strings.NewReader(""), &out, &errOut)
	return out.String(), errOut.String(), err
}

// fixture creates root/api with a remote and three worktrees:
// done (merged), wip (unpushed commit) and dirty (merged but modified).
func fixture(t *testing.T) string {
	t.Helper()
	root := testutil.TempDir(t)
	api := testutil.NewRepoWithRemote(t, root, "api")
	for _, b := range []string{"done", "wip", "dirty"} {
		testutil.Git(t, api, "worktree", "add", "-q", filepath.Join(root, b), "-b", b)
	}
	testutil.Commit(t, filepath.Join(root, "wip"), "w.txt", "wip")
	for _, b := range []string{"done", "dirty"} {
		testutil.Commit(t, filepath.Join(root, b), b+".txt", b)
		testutil.Git(t, api, "merge", "-q", "--no-edit", b)
	}
	testutil.WriteFile(t, filepath.Join(root, "dirty", "scratch.txt"), 10)
	return root
}

func TestListTable(t *testing.T) {
	root := fixture(t)
	out, errOut, err := woma(t, "list", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REPO", "done", "merged", "1 unpushed", "modified", "api"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "3 worktree(s)") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestListJSONAndFilters(t *testing.T) {
	root := fixture(t)
	list := func(args ...string) []jsonEntry {
		out, _, err := woma(t, append([]string{"list", "--json", root}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var entries []jsonEntry
		if err := json.Unmarshal([]byte(out), &entries); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return entries
	}
	names := func(entries []jsonEntry) string {
		var n []string
		for _, e := range entries {
			n = append(n, e.Branch)
		}
		return strings.Join(n, ",")
	}
	all := list()
	if names(all) != "dirty,done,wip" || all[1].Repo != "api" || !all[1].Merged || !all[1].Safe || all[2].Unpushed != 1 {
		t.Fatalf("all: %+v", all)
	}
	if got := names(list("--merged")); got != "dirty,done" {
		t.Errorf("--merged: %s", got)
	}
	// Flags after the path work too, and filters combine.
	if got := names(list("--merged", "--clean")); got != "done" {
		t.Errorf("--merged --clean: %s", got)
	}
	if got := names(list("--safe")); got != "done" {
		t.Errorf("--safe: %s", got)
	}
	if got := names(list("--older-than", "30d")); got != "" {
		t.Errorf("--older-than: %s", got)
	}
	if _, _, err := woma(t, "list", root, "--older-than", "soon"); err == nil {
		t.Error("invalid age should fail")
	}
}

func TestCleanRequiresFilter(t *testing.T) {
	_, _, err := woma(t, "clean", fixture(t))
	if err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("err = %v", err)
	}
}

func TestCleanDryRunAndConfirmation(t *testing.T) {
	root := fixture(t)
	out, _, err := woma(t, "clean", root, "--merged", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "skip") || !strings.Contains(out, "uncommitted") || !strings.Contains(out, "would delete 1 worktree(s)") {
		t.Errorf("dry run output:\n%s", out)
	}
	if !exists(filepath.Join(root, "done")) {
		t.Fatal("dry run deleted files")
	}
	// Without --yes and without a terminal it refuses.
	if _, _, err := woma(t, "clean", root, "--merged"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
	if !exists(filepath.Join(root, "done")) {
		t.Fatal("deleted without confirmation")
	}
}

func TestCleanWithBranches(t *testing.T) {
	root := fixture(t)
	api := filepath.Join(root, "api")
	out, _, err := woma(t, "clean", root, "--all", "--yes", "--delete-branch")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "removed "+tui.Tildify(filepath.Join(root, "done"))+" and branch done") ||
		!strings.Contains(out, "kept branch wip") || !strings.Contains(out, "removed 2 worktree(s)") {
		t.Errorf("output:\n%s", out)
	}
	if exists(filepath.Join(root, "done")) || exists(filepath.Join(root, "wip")) || !exists(filepath.Join(root, "dirty")) {
		t.Error("wrong worktrees removed")
	}
	if git.BranchExists(api, "done") || !git.BranchExists(api, "wip") {
		t.Error("wrong branches deleted")
	}

	// --force also removes the dirty one.
	if out, _, err := woma(t, "clean", root, "--all", "--yes", "--force"); err != nil || exists(filepath.Join(root, "dirty")) {
		t.Fatalf("force: %v\n%s", err, out)
	}
	if out, _, _ := woma(t, "clean", root, "--all", "--yes"); !strings.Contains(out, "nothing to clean") {
		t.Errorf("output: %s", out)
	}
}

func TestNew(t *testing.T) {
	root := testutil.TempDir(t)
	api := filepath.Join(root, "api")
	testutil.NewRepo(t, api)
	os.WriteFile(filepath.Join(api, ".env"), []byte("A=1"), 0o600)

	out, errOut, err := woma(t, "new", "feat/login-page", "-C", api, "--no-install")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	want := filepath.Join(api, ".worktrees", "feat-login-page")
	if strings.TrimSpace(out) != want {
		t.Errorf("stdout = %q, want only the path %q", out, want)
	}
	if !exists(filepath.Join(want, ".env")) || !strings.Contains(errOut, "copied .env") {
		t.Errorf("env not copied; log:\n%s", errOut)
	}

	custom := filepath.Join(root, "custom")
	if out, _, err := woma(t, "new", "-C", api, "--path", custom, "--base", "main", "--no-copy", "other"); err != nil || strings.TrimSpace(out) != custom {
		t.Fatalf("custom path: %v %q", err, out)
	}
	if exists(filepath.Join(custom, ".env")) {
		t.Error("--no-copy still copied")
	}
	if _, _, err := woma(t, "new", "-C", api); err == nil {
		t.Error("missing branch should fail")
	}
}

func TestShellInit(t *testing.T) {
	for shell, want := range map[string]string{"zsh": "wcd() {", "bash": "wnew() {", "fish": "function wcd"} {
		out, _, err := woma(t, "shell-init", shell)
		if err != nil || !strings.Contains(out, want) || !strings.Contains(out, "woma pick") {
			t.Errorf("%s: %v\n%s", shell, err, out)
		}
	}
	if _, _, err := woma(t, "shell-init", "powershell"); err == nil {
		t.Error("unsupported shell should fail")
	}
}

func TestMiscCommands(t *testing.T) {
	if out, _, _ := woma(t, "version"); !strings.HasPrefix(out, "woma ") {
		t.Errorf("version: %q", out)
	}
	if out, _, _ := woma(t, "help"); !strings.Contains(out, "woma clean") {
		t.Errorf("help: %q", out)
	}
	out, _, err := woma(t, "config")
	if err != nil || !strings.Contains(out, "not found, using defaults") || !strings.Contains(out, `stale_after = "30d"`) {
		t.Errorf("config: %v\n%s", err, out)
	}
}

func TestConfigRootsAndErrors(t *testing.T) {
	root := fixture(t)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte("roots = [\""+root+"\"]\n"), 0o644)
	t.Setenv("WOMA_CONFIG", cfgPath)
	out, _, err := woma(t, "list", "--json")
	if err != nil || strings.Count(out, `"path"`) != 3 {
		t.Errorf("configured roots not used: %v\n%s", err, out)
	}

	os.WriteFile(cfgPath, []byte("nope = 1\n"), 0o644)
	if _, _, err := woma(t, "list"); err == nil || !strings.Contains(err.Error(), "unknown setting") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveRootsErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o644)
	if _, _, err := woma(t, "list", file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("err = %v", err)
	}
	if _, _, err := woma(t, "list", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing path should fail")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestBrokenConfigKeepsBasicCommandsWorking(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(cfgPath, []byte(`stale_after = "30days"`+"\n"), 0o644)
	t.Setenv("WOMA_CONFIG", cfgPath)
	for _, args := range [][]string{{"help"}, {"version"}, {"shell-init", "zsh"}} {
		if _, _, err := woma(t, args...); err != nil {
			t.Errorf("%v failed with broken config: %v", args, err)
		}
	}
	out, _, err := woma(t, "config")
	if err == nil || !strings.Contains(out, cfgPath) || !strings.Contains(err.Error(), "stale_after") {
		t.Errorf("config should show the path and the problem: %q %v", out, err)
	}
	if _, _, err := woma(t, "list", t.TempDir()); err == nil {
		t.Error("list should fail with a broken config")
	}
}

func TestParseArgsDoubleDash(t *testing.T) {
	for _, c := range []struct {
		args []string
		pos  string
		json bool
	}{
		{[]string{"a", "--json", "b"}, "a,b", true},
		{[]string{"--json", "--", "-odd", "--json"}, "-odd,--json", true},
		{[]string{"a", "--", "-x", "c"}, "a,-x,c", false},
	} {
		fs := newFlagSet("t", "", &bytes.Buffer{})
		asJSON := fs.Bool("json", false, "")
		pos, err := parseArgs(fs, c.args)
		if err != nil || strings.Join(pos, ",") != c.pos || *asJSON != c.json {
			t.Errorf("%v: pos=%v json=%v err=%v", c.args, pos, *asJSON, err)
		}
	}
}
