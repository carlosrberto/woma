package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlosrberto/woma/internal/config"
	"github.com/carlosrberto/woma/internal/fsutil"
	"github.com/carlosrberto/woma/internal/git"
	"github.com/carlosrberto/woma/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.IsolateGit()
	os.Exit(m.Run())
}

// drive runs cmd and every command it produces, feeding messages back into
// the model, until there is nothing left to do. Spinner ticks are dropped so
// the loop terminates.
func drive(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case spinner.TickMsg, tea.QuitMsg, nil:
		case envelope, statusMsg, sizeMsg, deleteMsg, cleanPreviewMsg, opMsg:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

// key sends keys to the model and returns the last command produced.
func key(m *Model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		}
		_, cmd = m.Update(msg)
	}
	return cmd
}

// fixture creates:
//
//	root/api (with a remote): .worktrees/feature-a (merged),
//	                          .worktrees/feature-b (unpushed commit, dirty, 512 KB)
//	root/web:                 web-hotfix (locked, no commits)
func fixture(t *testing.T) string {
	t.Helper()
	root := testutil.TempDir(t)
	api := testutil.NewRepoWithRemote(t, root, "api")
	testutil.Git(t, api, "worktree", "add", "-q", ".worktrees/feature-a", "-b", "feature-a")
	testutil.Git(t, api, "worktree", "add", "-q", ".worktrees/feature-b", "-b", "feature-b")
	testutil.Commit(t, filepath.Join(api, ".worktrees/feature-a"), "a.txt", "work on a")
	testutil.Git(t, api, "merge", "-q", "feature-a")
	featB := filepath.Join(api, ".worktrees/feature-b")
	testutil.Commit(t, featB, "b.txt", "work on b")
	testutil.WriteFile(t, filepath.Join(featB, "data/big.bin"), 512<<10)

	web := filepath.Join(root, "web")
	testutil.NewRepo(t, web)
	testutil.Git(t, web, "worktree", "add", "-q", "../web-hotfix", "-b", "hotfix")
	testutil.Git(t, web, "worktree", "lock", "../web-hotfix")
	return root
}

func newModel(root string, pick bool) *Model {
	m := New(Options{Roots: []string{root}, Config: config.Default(), Pick: pick})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

func scanned(t *testing.T, root string) *Model {
	t.Helper()
	m := newModel(root, false)
	drive(t, m, m.Init())
	if m.state != stateList {
		t.Fatalf("state = %v after scan (err %v)", m.state, m.err)
	}
	return m
}

func find(m *Model, name string) *item {
	for _, it := range m.allItems() {
		if it.name == name {
			return it
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestScanAndInspect(t *testing.T) {
	m := scanned(t, fixture(t))
	if len(m.groups) != 2 || m.groups[0].repo.Name != "api" || len(m.groups[0].items) != 2 {
		t.Fatalf("groups: %+v", m.groups)
	}
	if m.checkDone != 3 || m.checkTotal != 3 || m.sizeDone != 3 || m.sizeTotal != 3 || m.scanning {
		t.Fatalf("checked %d/%d measured %d/%d scanning=%v", m.checkDone, m.checkTotal, m.sizeDone, m.sizeTotal, m.scanning)
	}
	a, b := find(m, ".worktrees/feature-a"), find(m, ".worktrees/feature-b")
	if !a.e.Status.Merged || !a.e.Safe() {
		t.Errorf("feature-a: %+v", a.e.Status)
	}
	if b.e.Size < 512<<10 || !b.e.Status.Dirty || b.e.Status.Unpushed != 1 {
		t.Errorf("feature-b: size=%d %+v", b.e.Size, b.e.Status)
	}

	view := m.View()
	for _, want := range []string{"api", "web", "feature-a", "merged", "modified", "1 unpushed", "locked", "3 worktrees in 2 repositories", "path", "branch"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestCursorSkipsHeaders(t *testing.T) {
	m := scanned(t, fixture(t))
	names := []string{m.current().name}
	for range 3 {
		key(m, "j")
		names = append(names, m.current().name)
	}
	want := ".worktrees/feature-a,.worktrees/feature-b,web-hotfix,web-hotfix"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("cursor path = %s", got)
	}
	key(m, "g")
	if m.current().name != ".worktrees/feature-a" {
		t.Errorf("g went to %q", m.current().name)
	}
}

func TestSelection(t *testing.T) {
	m := scanned(t, fixture(t))
	key(m, "a")
	if len(m.selected()) != 2 {
		t.Fatalf("a selected %d", len(m.selected()))
	}
	key(m, "a", "A")
	if len(m.selected()) != 3 {
		t.Fatalf("A selected %d", len(m.selected()))
	}
	key(m, "A", "m")
	if sel := m.selected(); len(sel) != 1 || sel[0].name != ".worktrees/feature-a" {
		t.Fatalf("m selected %v", sel)
	}
	key(m, "m")
	if len(m.selected()) != 0 {
		t.Fatal("second m should unselect safe worktrees")
	}
}

func TestSearch(t *testing.T) {
	m := scanned(t, fixture(t))
	key(m, "/", "h", "o", "t")
	if !m.searching || m.filter != "hot" || len(m.visible()) != 1 || m.current().name != "web-hotfix" {
		t.Fatalf("filter %q visible %d", m.filter, len(m.visible()))
	}
	if !strings.Contains(m.View(), "/hot") {
		t.Error("search box not shown")
	}
	key(m, "enter")
	if m.searching || m.filter != "hot" {
		t.Fatal("enter should keep the filter and leave search")
	}
	key(m, "A") // only selects what is shown
	if len(m.selected()) != 1 {
		t.Fatalf("A with filter selected %d", len(m.selected()))
	}
	key(m, "esc")
	if m.filter != "" || len(m.visible()) != 3 {
		t.Fatal("esc should clear the filter")
	}
	if cmd := key(m, "esc"); cmd != nil {
		t.Fatal("esc without a filter must not quit")
	}
	key(m, "/", "z", "z", "z")
	if !strings.Contains(m.View(), "Nothing matches") {
		t.Error("empty filter result not shown")
	}
}

func TestSortModes(t *testing.T) {
	m := scanned(t, fixture(t))
	key(m, "s")
	if m.sort != sortSize || m.groups[0].items[0].name != ".worktrees/feature-b" {
		t.Errorf("size sort: %v first=%s", m.sort, m.groups[0].items[0].name)
	}
	key(m, "s")
	if m.sort != sortActivity || !strings.Contains(m.View(), "sort (activity)") {
		t.Errorf("activity sort: %v", m.sort)
	}
	key(m, "s")
	if m.sort != sortName {
		t.Errorf("sort should cycle back to name, got %v", m.sort)
	}
}

func TestCancelDoesNotDelete(t *testing.T) {
	root := fixture(t)
	m := scanned(t, root)
	key(m, "space", "enter")
	if m.state != stateConfirmDelete || !strings.Contains(m.View(), "Delete 1 worktree") {
		t.Fatalf("state = %v\n%s", m.state, m.View())
	}
	key(m, "n")
	if m.state != stateList || !exists(filepath.Join(root, "api/.worktrees/feature-a")) {
		t.Fatal("cancel failed")
	}
}

func TestDeleteWithBranches(t *testing.T) {
	root := fixture(t)
	api := filepath.Join(root, "api")
	m := scanned(t, root)
	key(m, "space", "j", "space", "enter") // feature-a (merged) + feature-b (unpushed)
	view := m.View()
	if !strings.Contains(view, "uncommitted changes") {
		t.Errorf("confirm should warn about lost changes:\n%s", view)
	}
	key(m, "b")
	view = m.View()
	if !strings.Contains(view, "delete branch") || !strings.Contains(view, "keep branch (unpushed)") {
		t.Errorf("branch plan not shown:\n%s", view)
	}
	drive(t, m, key(m, "y"))

	if !m.finished || m.removed != 2 {
		t.Fatalf("finished=%v removed=%d\n%s", m.finished, m.removed, m.View())
	}
	view = m.View()
	if !strings.Contains(view, "Removed 2 worktrees") || !strings.Contains(view, "branch feature-a deleted") {
		t.Errorf("done view:\n%s", view)
	}
	if git.BranchExists(api, "feature-a") || !git.BranchExists(api, "feature-b") {
		t.Error("expected feature-a deleted and feature-b kept")
	}
	if exists(filepath.Join(api, ".worktrees/feature-a")) || exists(filepath.Join(api, ".worktrees/feature-b")) {
		t.Error("worktree folders still exist")
	}

	key(m, "x")
	if m.state != stateList || len(m.allItems()) != 1 {
		t.Fatalf("after delete: state=%v items=%d", m.state, len(m.allItems()))
	}
	if n, freed := m.Summary(); n != 2 || freed < 512<<10 {
		t.Errorf("summary = %d, %d", n, freed)
	}
}

func pickModel(t *testing.T, root string, multi bool) *Model {
	t.Helper()
	m := New(Options{Roots: []string{root}, Config: config.Default(), Pick: true, Multi: multi})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(t, m, m.Init())
	return m
}

func TestPickSingle(t *testing.T) {
	root := fixture(t)
	m := pickModel(t, root, false)
	view := m.View()
	if strings.Contains(view, "[ ]") || !strings.Contains(view, "enter pick ·") || strings.Contains(view, "delete") {
		t.Errorf("single pick view should have no checkboxes or delete hints:\n%s", view)
	}

	// Selection and every action that changes worktrees are disabled.
	for _, k := range []string{"space", "x", "a", "A", "m", "d", "backspace", "c", "L", "M", "R", "o", "t"} {
		if cmd := key(m, k); cmd != nil || m.state != stateList || len(m.selected()) != 0 {
			t.Fatalf("key %q did something in pick mode (state=%v)", k, m.state)
		}
	}
	if !exists(filepath.Join(root, "api/.worktrees/feature-a")) {
		t.Fatal("pick mode changed a worktree")
	}

	key(m, "j")
	cmd := key(m, "enter")
	if got := m.Picked(); len(got) != 1 || got[0] != filepath.Join(root, "api/.worktrees/feature-b") {
		t.Fatalf("picked %v", got)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("pick should quit")
	}
}

func TestPickSingleHelp(t *testing.T) {
	m := pickModel(t, fixture(t), false)
	key(m, "?")
	help := m.View()
	if !strings.Contains(help, "print the path of the worktree under the cursor") || strings.Contains(help, "lock") ||
		strings.Contains(help, "select") || !strings.Contains(help, "quit without picking") {
		t.Errorf("pick help:\n%s", help)
	}
}

func TestPickMulti(t *testing.T) {
	root := fixture(t)
	m := pickModel(t, root, true)
	if view := m.View(); !strings.Contains(view, "[ ]") || !strings.Contains(view, "pick (multiple)") {
		t.Errorf("multi pick view:\n%s", view)
	}
	if cmd := key(m, "d"); cmd != nil || m.state != stateList {
		t.Fatal("delete must stay disabled with --multi")
	}
	key(m, "space", "G", "space") // feature-a and web-hotfix
	key(m, "enter")
	want := []string{filepath.Join(root, "api/.worktrees/feature-a"), filepath.Join(root, "web-hotfix")}
	if got := m.Picked(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("picked %v, want %v", got, want)
	}

	// With nothing selected, enter picks the worktree under the cursor.
	m = pickModel(t, root, true)
	key(m, "j", "enter")
	if got := m.Picked(); len(got) != 1 || got[0] != filepath.Join(root, "api/.worktrees/feature-b") {
		t.Fatalf("picked %v", got)
	}
}

func TestPickSkipsMissingWorktrees(t *testing.T) {
	root := fixture(t)
	os.RemoveAll(filepath.Join(root, "api/.worktrees/feature-a"))
	m := pickModel(t, root, true)
	if !m.current().wt().Prunable {
		t.Fatal("setup: expected the missing worktree under the cursor")
	}
	if cmd := key(m, "enter"); cmd != nil || m.Picked() != nil {
		t.Fatalf("picked a missing worktree: %v", m.Picked())
	}
	key(m, "space", "j", "space", "enter") // missing + feature-b
	if got := m.Picked(); len(got) != 1 || got[0] != filepath.Join(root, "api/.worktrees/feature-b") {
		t.Fatalf("picked %v", got)
	}
}

func TestLockToggle(t *testing.T) {
	root := fixture(t)
	m := scanned(t, root)
	drive(t, m, key(m, "L"))
	repo, _ := git.LoadRepo(t.Context(), filepath.Join(root, "api"))
	if !repo.Worktrees[0].Locked || !m.current().wt().Locked || !strings.Contains(m.flash, "locked") {
		t.Fatalf("lock: git=%v model=%v flash=%q", repo.Worktrees[0].Locked, m.current().wt().Locked, m.flash)
	}
	drive(t, m, key(m, "L"))
	repo, _ = git.LoadRepo(t.Context(), filepath.Join(root, "api"))
	if repo.Worktrees[0].Locked || m.current().wt().Locked {
		t.Fatal("unlock failed")
	}
}

func TestMove(t *testing.T) {
	root := fixture(t)
	m := scanned(t, root)
	key(m, "M")
	if m.state != stateMove || !strings.Contains(m.View(), "Move api") {
		t.Fatalf("state = %v", m.state)
	}
	dest := filepath.Join(root, "moved-a")
	m.moveInput.SetValue(dest)
	drive(t, m, key(m, "enter"))
	if !exists(dest) || exists(filepath.Join(root, "api/.worktrees/feature-a")) {
		t.Fatalf("move failed: %s", m.flash)
	}
	if it := find(m, "moved-a"); it == nil || it.wt().Path != dest {
		t.Fatal("item not updated after move")
	}
}

func TestMoveErrorIsShown(t *testing.T) {
	m := scanned(t, fixture(t))
	key(m, "G") // locked web-hotfix
	key(m, "M")
	m.moveInput.SetValue(filepath.Join(t.TempDir(), "elsewhere"))
	drive(t, m, key(m, "enter"))
	if !m.flashErr || !strings.Contains(m.statusLine(), "locked") {
		t.Fatalf("expected locked error, got %q", m.flash)
	}
}

func TestCleanArtifacts(t *testing.T) {
	root := fixture(t)
	featA := filepath.Join(root, "api/.worktrees/feature-a")
	os.WriteFile(filepath.Join(featA, ".gitignore"), []byte("node_modules/\n.env\n"), 0o644)
	testutil.WriteFile(t, filepath.Join(featA, "node_modules/dep/index.js"), 256<<10)
	testutil.WriteFile(t, filepath.Join(featA, ".env"), 10)
	m := scanned(t, root)

	drive(t, m, key(m, "c"))
	if m.state != stateConfirmClean || !strings.Contains(m.View(), "node_modules/") {
		t.Fatalf("state = %v flash=%q\n%s", m.state, m.flash, m.View())
	}
	before := find(m, ".worktrees/feature-a").e.Size
	drive(t, m, key(m, "y"))
	if exists(filepath.Join(featA, "node_modules")) || !exists(filepath.Join(featA, ".env")) {
		t.Fatal("clean removed the wrong files")
	}
	if after := find(m, ".worktrees/feature-a").e.Size; after >= before {
		t.Errorf("size not refreshed: %d -> %d", before, after)
	}

	drive(t, m, key(m, "c")) // nothing left to clean
	if m.state != stateList || !strings.Contains(m.flash, "nothing to clean") {
		t.Errorf("state=%v flash=%q", m.state, m.flash)
	}
}

func TestRepairRescans(t *testing.T) {
	m := scanned(t, fixture(t))
	drive(t, m, key(m, "R"))
	if m.state != stateList || len(m.allItems()) != 3 || !strings.Contains(m.flash, "repaired api") {
		t.Fatalf("state=%v items=%d flash=%q", m.state, len(m.allItems()), m.flash)
	}
}

func TestTmuxOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	m := scanned(t, fixture(t))
	if cmd := key(m, "t"); cmd != nil || !strings.Contains(m.flash, "not running inside tmux") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestDetailAndHelp(t *testing.T) {
	m := scanned(t, fixture(t))
	key(m, "j") // feature-b
	view := m.View()
	for _, want := range []string{"no upstream", "1 commit unpushed", "changed file", "work on b", "?? data/"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail missing %q:\n%s", want, view)
		}
	}
	key(m, "i")
	if strings.Contains(m.View(), "work on b") {
		t.Error("i should hide the detail pane")
	}
	key(m, "?")
	if m.state != stateHelp || !strings.Contains(m.View(), "select safe ones") {
		t.Fatal("help not shown")
	}
	key(m, "x")
	if m.state != stateList {
		t.Fatal("any key should close help")
	}
}

func TestEmptyRoot(t *testing.T) {
	m := scanned(t, testutil.TempDir(t))
	if !strings.Contains(m.View(), "No linked worktrees found") {
		t.Errorf("view:\n%s", m.View())
	}
	key(m, "j", "space", "enter", "c", "L", "M")
	if m.state != stateList {
		t.Errorf("state = %v with nothing to act on", m.state)
	}
}

func TestEditorCommand(t *testing.T) {
	t.Setenv("VISUAL", "nvim")
	t.Setenv("EDITOR", "vi")
	if got := editorCommand(config.Config{Editor: "code --new-window"}); strings.Join(got, " ") != "code --new-window" {
		t.Errorf("configured editor: %v", got)
	}
	if got := editorCommand(config.Config{}); got[0] != "nvim" {
		t.Errorf("VISUAL: %v", got)
	}
	t.Setenv("VISUAL", "")
	if got := editorCommand(config.Config{}); got[0] != "vi" {
		t.Errorf("EDITOR: %v", got)
	}
}

func TestFormatting(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 30: "5.0 GB"} {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for d, want := range map[time.Duration]string{
		10 * time.Second: "now", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 2 * day: "2d",
		21 * day: "3w", 90 * day: "3mo", 800 * day: "2y",
	} {
		if got := Age(now.Add(-d), now); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
	if Age(time.Time{}, now) != "—" {
		t.Error("zero time should render as —")
	}
}

// firstRepo starts a scan and handles events only until the first
// repository is listed, leaving its inspection pending.
func firstRepo(t *testing.T, m *Model) {
	t.Helper()
	m.Init()
	for {
		ev := <-m.events
		m.Update(envelope{m.gen, ev})
		if _, ok := ev.(repoMsg); ok {
			return
		}
		if _, ok := ev.(scanDoneMsg); ok {
			t.Fatal("scan finished without repositories")
		}
	}
}

func TestListShowsBeforeInspection(t *testing.T) {
	m := newModel(fixture(t), false)
	firstRepo(t, m)
	if m.state != stateList || len(m.visible()) == 0 || m.checkDone != 0 || m.sizeDone != 0 {
		t.Fatalf("state=%v rows=%d checked=%d measured=%d", m.state, len(m.visible()), m.checkDone, m.sizeDone)
	}
	view := m.View()
	if !strings.Contains(view, "…") {
		t.Errorf("pending sizes should show …:\n%s", view)
	}
	m.cancel()
}

func TestCurrentRepoFirst(t *testing.T) {
	root := fixture(t)
	// Start inside web (alphabetically after api), from a subfolder.
	sub := filepath.Join(root, "web", "docs")
	os.MkdirAll(sub, 0o755)
	m := New(Options{Roots: []string{sub}, Config: config.Default()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	firstRepo(t, m)
	if m.groups[0].repo.Name != "web" || !m.groups[0].current {
		t.Fatalf("first group = %s", m.groups[0].repo.Name)
	}
	m.cancel()

	// Scanning the parent folder from inside api: api is pinned first even
	// when sorting by size puts web's bigger worktrees ahead.
	m = New(Options{Roots: []string{filepath.Join(root, "api")}, Config: config.Default()})
	drive(t, m, m.Init())
	if len(m.groups) != 1 || !m.groups[0].current {
		t.Fatalf("groups = %d", len(m.groups))
	}
}

func TestRescanDropsStaleEvents(t *testing.T) {
	m := newModel(fixture(t), false)
	firstRepo(t, m)
	oldGen := m.gen
	drive(t, m, key(m, "r"))
	if m.gen == oldGen || len(m.allItems()) != 3 || m.streaming() {
		t.Fatalf("gen=%d items=%d streaming=%v", m.gen, len(m.allItems()), m.streaming())
	}
	before := len(m.allItems())
	m.Update(envelope{oldGen, repoMsg{repo: git.Repo{Name: "stale", Worktrees: []git.Worktree{{Path: "/x"}}}}})
	if len(m.allItems()) != before {
		t.Fatal("event from the old scan was applied")
	}
}

func TestDeleteWaitsForInspection(t *testing.T) {
	m := newModel(fixture(t), false)
	firstRepo(t, m)
	defer m.cancel()
	key(m, "d")
	if m.state != stateList || !strings.Contains(m.flash, "still inspecting") {
		t.Fatalf("delete allowed before inspection: state=%v flash=%q", m.state, m.flash)
	}
	if len(m.selected()) != 0 {
		t.Error("refused delete must not leave the item selected")
	}
	if labels := m.labels(m.current()); labels != "" {
		t.Errorf("uninspected item shows labels %q", labels)
	}
}

func TestCancelUndoesAutoSelection(t *testing.T) {
	m := scanned(t, fixture(t))
	key(m, "d", "n") // auto-selects feature-a, then cancels
	if len(m.selected()) != 0 {
		t.Fatal("cancel should unselect the auto-selected worktree")
	}
	key(m, "j", "d")
	if sel := m.selected(); len(sel) != 1 || sel[0].name != ".worktrees/feature-b" {
		t.Fatalf("second delete targets %v", sel)
	}
	key(m, "esc")
	key(m, "space", "d", "n") // explicit selection survives cancel
	if len(m.selected()) != 1 {
		t.Fatal("explicit selection should survive cancel")
	}
}

func TestMoveRefusesExistingDestination(t *testing.T) {
	root := fixture(t)
	m := scanned(t, root)
	key(m, "M")
	other := filepath.Join(root, "api/.worktrees/feature-b")
	m.moveInput.SetValue(other)
	if cmd := key(m, "enter"); cmd != nil || !strings.Contains(m.flash, "already exists") {
		t.Fatalf("flash = %q", m.flash)
	}
	if !exists(filepath.Join(root, "api/.worktrees/feature-a")) || exists(filepath.Join(other, "feature-a")) {
		t.Fatal("worktree was moved into the existing folder")
	}
}

func TestLateInspectionKeepsActionChanges(t *testing.T) {
	m := scanned(t, fixture(t))
	it := m.current()
	stale := it.e // snapshot as an in-flight inspection would carry
	drive(t, m, key(m, "L"))
	if !it.wt().Locked {
		t.Fatal("lock failed")
	}
	m.Update(statusMsg{it: it, status: stale.Status})
	m.Update(sizeMsg{it: it, stats: fsutil.Stats{Size: 12345}})
	if !it.wt().Locked || it.e.Size != 12345 {
		t.Errorf("inspection result reverted the lock (locked=%v size=%d)", it.wt().Locked, it.e.Size)
	}
}

func TestCtrlCWaitsForDeletion(t *testing.T) {
	m := scanned(t, fixture(t))
	key(m, "space", "enter")
	cmd := key(m, "y")
	_, quit := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if quit != nil || !strings.Contains(m.flash, "ctrl+c is disabled") {
		t.Fatalf("ctrl+c during deletion: cmd=%v flash=%q", quit, m.flash)
	}
	drive(t, m, cmd)
	if _, quit = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); quit == nil {
		t.Fatal("ctrl+c should quit once deletion finished")
	}
}
