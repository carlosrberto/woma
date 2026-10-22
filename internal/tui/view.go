package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	sizeWidth = 9
	ageWidth  = 4
)

func (m *Model) View() string {
	switch m.state {
	case stateScanning:
		return m.viewScanning()
	case stateConfirmDelete:
		return m.viewConfirmDelete()
	case stateDeleting:
		return m.viewDeleting()
	case stateConfirmClean:
		return m.viewConfirmClean()
	case stateMove:
		return m.viewMove()
	case stateHelp:
		return m.viewHelp()
	}
	return m.viewList()
}

func (m *Model) title() string {
	roots := make([]string, len(m.opts.Roots))
	for i, r := range m.opts.Roots {
		roots[i] = Tildify(r)
	}
	t := styles.title.Render("woma")
	switch {
	case m.opts.Pick && m.opts.Multi:
		t += styles.dim.Render(" pick (multiple)")
	case m.opts.Pick:
		t += styles.dim.Render(" pick")
	}
	return t + "  " + styles.dim.Render(truncRight(strings.Join(roots, ", "), max(10, m.width-12)))
}

func (m *Model) viewScanning() string {
	var b strings.Builder
	b.WriteString(m.title() + "\n\n")
	fmt.Fprintf(&b, " %sScanning for git repositories…\n\n", m.spinner.View())
	fmt.Fprintf(&b, "   %s directories · %s repositories\n",
		styles.bold.Render(fmt.Sprint(m.scan.Dirs.Load())),
		styles.bold.Render(fmt.Sprint(m.scan.Found.Load())))
	b.WriteString("   " + styles.dim.Render(truncLeft(Tildify(m.scan.Current()), m.width-4)) + "\n")
	return b.String()
}

// sizeLabel totals the items' sizes, marking it with "…" while some are
// still being measured.
func sizeLabel(items []*item) string {
	var total int64
	pending := false
	for _, it := range items {
		total += it.e.Size
		pending = pending || !it.measured
	}
	if pending {
		return HumanBytes(total) + "…"
	}
	return HumanBytes(total)
}

func (m *Model) viewList() string {
	var b strings.Builder
	b.WriteString(m.title() + "\n")

	all := m.allItems()
	stats := fmt.Sprintf("%d %s in %d %s · %s", len(all), plural(len(all), "worktree"),
		len(m.groups), plural(len(m.groups), "repository", "repositories"), sizeLabel(all))
	switch {
	case m.scanning:
		stats += fmt.Sprintf("  %sscanning %d folders", m.spinner.View(), m.scan.Dirs.Load())
	case m.checkDone < m.checkTotal:
		stats += fmt.Sprintf("  %schecking git %d/%d", m.spinner.View(), m.checkDone, m.checkTotal)
	}
	if !m.scanning && m.sizeDone < m.sizeTotal {
		stats += fmt.Sprintf("  measuring %s %d/%d",
			m.bar.ViewAs(float64(m.sizeDone)/float64(m.sizeTotal)), m.sizeDone, m.sizeTotal)
	}
	b.WriteString(styles.dim.Render(stats) + "\n")
	switch {
	case m.searching:
		b.WriteString(m.search.View() + "\n")
	case m.filter != "":
		b.WriteString(styles.key.Render("/"+m.filter) + styles.dim.Render(fmt.Sprintf("  %d shown · esc clears", len(m.visible()))) + "\n")
	default:
		b.WriteString("\n")
	}

	h := m.listHeight()
	if len(m.rows) == 0 {
		msg := "  No linked worktrees found."
		if m.filter != "" {
			msg = "  Nothing matches the filter."
		}
		b.WriteString(msg + "\n" + strings.Repeat("\n", h-1))
	} else {
		nameW, branchW := m.columnWidths(m.visible())
		end := min(len(m.rows), m.offset+h)
		for i := m.offset; i < end; i++ {
			b.WriteString(m.renderRow(m.rows[i], i == m.cursor, nameW, branchW) + "\n")
		}
		b.WriteString(strings.Repeat("\n", h-(end-m.offset)))
	}
	if m.detailVisible() {
		b.WriteString(m.viewDetail())
	}

	b.WriteString("\n" + m.statusLine() + "\n")
	sorting := "s sort (" + m.sort.String() + ")"
	help := "↑/↓ move · space select · m select safe · / search · " + sorting + " · enter delete · ? help · q quit"
	switch {
	case m.opts.Pick && m.opts.Multi:
		help = "↑/↓ move · space select · / search · " + sorting + " · enter pick selected · ? help · q cancel"
	case m.opts.Pick:
		help = "↑/↓ move · / search · " + sorting + " · enter pick · ? help · q cancel"
	}
	b.WriteString(styles.dim.Render(truncRight(help, m.width)))
	return b.String()
}

func (m *Model) statusLine() string {
	switch {
	case m.opRunning != "":
		return " " + m.spinner.View() + styles.dim.Render(m.opRunning+"…")
	case m.flash != "" && m.flashErr:
		return " " + styles.err.Render(truncRight(m.flash, m.width-2))
	case m.flash != "":
		return " " + styles.ok.Render(truncRight(m.flash, m.width-2))
	}
	if sel := m.selected(); len(sel) > 0 {
		var size int64
		for _, it := range sel {
			size += it.e.Size
		}
		return " " + styles.check.Render(fmt.Sprintf("%d selected · %s", len(sel), HumanBytes(size)))
	}
	return ""
}

func (m *Model) columnWidths(items []*item) (nameW, branchW int) {
	maxName, maxBranch := 4, 6
	for _, it := range items {
		maxName = max(maxName, lipgloss.Width(it.name))
		maxBranch = max(maxBranch, lipgloss.Width(BranchLabel(it.wt())))
	}
	// pointer, checkbox, age, size, labels and gaps take ~50 columns.
	avail := max(20, m.width-50)
	branchW = min(maxBranch, max(8, avail/3))
	nameW = min(maxName, max(10, avail-branchW))
	return nameW, branchW
}

func (m *Model) renderRow(r row, isCursor bool, nameW, branchW int) string {
	if r.item == nil {
		g := r.group
		items := m.visibleIn(g)
		info := fmt.Sprintf("  %d %s · %s", len(items), plural(len(items), "worktree"), sizeLabel(items))
		path := Tildify(g.repo.Path)
		for _, root := range m.opts.Roots {
			if rel, ok := within(root, g.repo.Path); ok {
				path = rel
			}
		}
		if path != g.repo.Name {
			info = "  " + path + "  ·" + info
		}
		return " " + styles.repo.Render(g.repo.Name) + styles.dim.Render(truncRight(info, max(0, m.width-lipgloss.Width(g.repo.Name)-2)))
	}

	it := r.item
	pointer := "  "
	name := pad(truncLeft(it.name, nameW), nameW)
	if isCursor {
		pointer = styles.pointer.Render("▸ ")
		name = styles.bold.Render(name)
	}
	box := "[ ] "
	switch {
	case m.opts.Pick && !m.opts.Multi:
		box = "" // nothing to select: enter picks the cursor
	case it.selected:
		box = styles.check.Render("[x]") + " "
	}
	branch := styles.dim.Render(pad(truncRight(BranchLabel(it.wt()), branchW), branchW))
	return "  " + pointer + box + name + "  " + branch + "  " + m.ageCell(it) + "  " + sizeCell(it) + "  " + m.labels(it)
}

func (m *Model) ageCell(it *item) string {
	if !it.inspected {
		return styles.dim.Render(fmt.Sprintf("%*s", ageWidth, "…"))
	}
	return styles.dim.Render(fmt.Sprintf("%*s", ageWidth, Age(it.e.LastActivity(), m.now())))
}

func sizeCell(it *item) string {
	switch {
	case it.wt().Prunable:
		return styles.dim.Render(fmt.Sprintf("%*s", sizeWidth, "—"))
	case !it.measured:
		return styles.dim.Render(fmt.Sprintf("%*s", sizeWidth, "…"))
	}
	return styles.size.Render(fmt.Sprintf("%*s", sizeWidth, HumanBytes(it.e.Size)))
}

func (m *Model) labels(it *item) string {
	if !it.inspected {
		return ""
	}
	var parts []string
	for _, l := range it.e.Labels(m.opts.Config.StaleDuration(), m.now()) {
		parts = append(parts, labelStyle(l.Kind).Render(l.Text))
	}
	if it.done && it.err != nil {
		parts = append(parts, styles.err.Render("delete failed"))
	}
	return strings.Join(parts, " ")
}

func (m *Model) viewDetail() string {
	lines := []string{styles.dim.Render(strings.Repeat("─", max(0, m.width)))}
	it := m.current()
	field := func(name, value string) {
		lines = append(lines, " "+styles.dim.Render(pad(name, 8))+truncRight(value, max(10, m.width-10)))
	}
	switch {
	case it == nil:
	case !it.inspected:
		field("path", Tildify(it.wt().Path))
		field("", m.spinner.View()+"inspecting…")
	default:
		e, s, now := it.e, it.e.Status, m.now()
		field("path", Tildify(e.WT.Path))

		branch := BranchLabel(e.WT)
		switch {
		case s.Gone:
			branch += styles.ok.Render("  upstream deleted (gone)")
		case s.Upstream != "":
			branch += " → " + s.Upstream + styles.dim.Render(fmt.Sprintf("  ahead %d, behind %d", s.Ahead, s.Behind))
		case e.WT.Branch != "":
			branch += styles.dim.Render("  no upstream")
		}
		field("branch", branch)

		var state []string
		if s.Merged {
			state = append(state, styles.ok.Render("merged into "+e.Repo.BaseBranch))
		}
		if s.NoCommits {
			state = append(state, "no commits yet")
		}
		if s.Unpushed > 0 {
			what := "unpushed"
			if e.WT.Branch == "" {
				what = "not on any branch"
			}
			state = append(state, styles.warn.Render(fmt.Sprintf("%d %s %s", s.Unpushed, plural(s.Unpushed, "commit"), what)))
		}
		if s.Dirty {
			state = append(state, styles.warn.Render(fmt.Sprintf("%d changed %s", s.NumChanges, plural(s.NumChanges, "file"))))
		}
		if e.WT.Locked {
			state = append(state, styles.err.Render("locked"))
		}
		if len(state) == 0 {
			state = append(state, "clean")
		}
		field("state", strings.Join(state, styles.dim.Render(" · ")))

		if s.Subject != "" {
			field("commit", s.Subject+styles.dim.Render(" · "+ago(s.LastCommit, now)))
		}
		size := "measuring…"
		if it.measured {
			size = HumanBytes(e.Size)
		}
		field("active", ago(e.LastActivity(), now)+styles.dim.Render(" · ")+size)
		if len(s.Changes) > 0 {
			changes := strings.Join(s.Changes, styles.dim.Render(" · "))
			if s.NumChanges > len(s.Changes) {
				changes += styles.dim.Render(fmt.Sprintf(" · +%d more", s.NumChanges-len(s.Changes)))
			}
			field("changes", changes)
		}
	}
	for len(lines) < detailHeight {
		lines = append(lines, "")
	}
	return strings.Join(lines[:detailHeight], "\n") + "\n"
}

func (m *Model) viewConfirmDelete() string {
	sel := m.selected()
	var b strings.Builder
	b.WriteString(m.title() + "\n\n")

	var total int64
	var loses, locked, keptBranches int
	for _, it := range sel {
		total += it.e.Size
		if it.e.LosesWork() {
			loses++
		}
		if it.wt().Locked {
			locked++
		}
		if it.wt().Branch != "" && !it.e.CanDeleteBranch() {
			keptBranches++
		}
	}
	fmt.Fprintf(&b, " %s\n\n", styles.bold.Render(fmt.Sprintf("Delete %d %s (%s)?", len(sel), plural(len(sel), "worktree"), HumanBytes(total))))

	shown := sel
	if limit := max(3, m.height-16); len(shown) > limit {
		shown = shown[:limit]
	}
	nameW, branchW := m.columnWidths(shown)
	for _, it := range shown {
		label := pad(truncLeft(it.e.Repo.Name+": "+it.name, nameW+12), nameW+12)
		branch := styles.dim.Render(pad(truncRight(BranchLabel(it.wt()), branchW), branchW))
		line := fmt.Sprintf("   %s  %s  %s  %s", label, branch, sizeCell(it), m.labels(it))
		if m.deleteBranch && it.wt().Branch != "" {
			if it.e.CanDeleteBranch() {
				line += "  " + styles.err.Render("delete branch")
			} else {
				line += "  " + styles.ok.Render("keep branch (unpushed)")
			}
		}
		b.WriteString(line + "\n")
	}
	if more := len(sel) - len(shown); more > 0 {
		b.WriteString(styles.dim.Render(fmt.Sprintf("   … and %d more", more)) + "\n")
	}

	b.WriteString("\n")
	if loses > 0 {
		b.WriteString(styles.warn.Render(fmt.Sprintf(" ⚠ %d %s uncommitted changes or commits on no branch that will be lost.", loses, plural(loses, "worktree has", "worktrees have"))) + "\n")
	}
	if locked > 0 {
		b.WriteString(styles.warn.Render(fmt.Sprintf(" ⚠ %d %s locked and will be removed anyway.", locked, plural(locked, "worktree is", "worktrees are"))) + "\n")
	}
	if m.deleteBranch {
		msg := " Merged or fully pushed branches will be deleted too."
		if keptBranches > 0 {
			msg += fmt.Sprintf(" %d with unpushed commits will be kept.", keptBranches)
		}
		b.WriteString(styles.warn.Render(msg) + "\n\n")
	} else {
		b.WriteString(styles.dim.Render(" Branches are kept; only the worktree folders are removed.") + "\n\n")
	}
	toggle := "also delete branches"
	if m.deleteBranch {
		toggle = "keep branches"
	}
	b.WriteString(" " + styles.err.Bold(true).Render("y") + " delete   " +
		styles.bold.Render("b") + styles.dim.Render(" "+toggle+"   ") +
		styles.bold.Render("n") + styles.dim.Render("/esc cancel"))
	return b.String()
}

func (m *Model) viewDeleting() string {
	var b strings.Builder
	b.WriteString(m.title() + "\n\n")

	var total, freed int64
	for _, it := range m.deleting {
		total += it.e.Size
		if it.done && it.err == nil {
			freed += it.e.Size
		} else {
			freed += min(it.freed.Load(), it.e.Size)
		}
	}
	fraction := float64(m.deleted) / float64(len(m.deleting))
	if total > 0 {
		fraction = float64(freed) / float64(total)
	}

	if m.finished {
		removed := countRemoved(m.deleting)
		msg := fmt.Sprintf("Removed %d %s", removed, plural(removed, "worktree"))
		if failed := len(m.deleting) - removed; failed > 0 {
			b.WriteString(" " + styles.warn.Render(fmt.Sprintf("✓ %s, %d failed", msg, failed)) + "\n")
		} else {
			b.WriteString(" " + styles.ok.Render("✓ "+msg) + "\n")
		}
	} else {
		fmt.Fprintf(&b, " %sDeleting %d %s…\n", m.spinner.View(), len(m.deleting), plural(len(m.deleting), "worktree"))
	}
	fmt.Fprintf(&b, " %s %3.0f%%  %s\n\n", m.bar.ViewAs(fraction), fraction*100,
		styles.dim.Render(fmt.Sprintf("%d/%d · %s / %s freed", m.deleted, len(m.deleting), HumanBytes(freed), HumanBytes(total))))

	// Show a window starting just before the first unfinished item.
	limit := max(3, m.height-10)
	start := 0
	for i, it := range m.deleting {
		if !it.done {
			start = max(0, i-2)
			break
		}
	}
	start = max(0, min(start, len(m.deleting)-limit))
	end := min(len(m.deleting), start+limit)

	small := m.bar
	small.Width = 20
	nameW := 0
	for _, it := range m.deleting[start:end] {
		nameW = max(nameW, lipgloss.Width(it.e.Repo.Name+": "+it.name))
	}
	nameW = min(nameW, max(10, m.width-40))
	for _, it := range m.deleting[start:end] {
		label := pad(truncLeft(it.e.Repo.Name+": "+it.name, nameW), nameW)
		var status string
		switch {
		case it.done && it.err != nil:
			label = styles.err.Render("✗ ") + label
			status = styles.err.Render(truncRight(it.err.Error(), max(10, m.width-nameW-8)))
		case it.done:
			label = styles.ok.Render("✓ ") + label
			status = sizeCell(it)
			if it.note != "" {
				status += "  " + styles.dim.Render(it.note)
			}
		case it.started.Load():
			label = m.spinner.View() + label
			f := 0.0
			if it.e.Size > 0 {
				f = min(1, float64(it.freed.Load())/float64(it.e.Size))
			}
			status = small.ViewAs(f) + styles.dim.Render(fmt.Sprintf(" %3.0f%%", f*100))
		default:
			label = styles.dim.Render("· " + label)
			status = styles.dim.Render("waiting")
		}
		fmt.Fprintf(&b, "  %s  %s\n", label, status)
	}
	if more := len(m.deleting) - end; more > 0 {
		b.WriteString(styles.dim.Render(fmt.Sprintf("  … and %d more", more)) + "\n")
	}

	if m.finished {
		b.WriteString("\n" + styles.dim.Render(" press any key to continue"))
	}
	return b.String()
}

func countRemoved(items []*item) int {
	n := 0
	for _, it := range items {
		if it.done && it.err == nil {
			n++
		}
	}
	return n
}

func (m *Model) viewConfirmClean() string {
	it := m.cleanTarget
	var b strings.Builder
	b.WriteString(m.title() + "\n\n")
	fmt.Fprintf(&b, " %s\n\n", styles.bold.Render(fmt.Sprintf("Remove git-ignored files from %s: %s?", it.e.Repo.Name, it.name)))
	shown := m.cleanFiles
	if limit := max(3, m.height-12); len(shown) > limit {
		shown = shown[:limit]
	}
	for _, f := range shown {
		b.WriteString("   " + f + "\n")
	}
	if more := len(m.cleanFiles) - len(shown); more > 0 {
		b.WriteString(styles.dim.Render(fmt.Sprintf("   … and %d more", more)) + "\n")
	}
	b.WriteString("\n" + styles.dim.Render(" Dependencies and build output are removed; tracked files, untracked files") + "\n")
	b.WriteString(styles.dim.Render(" and files matching "+strings.Join(m.opts.Config.Copy, ", ")+" are kept.") + "\n\n")
	b.WriteString(" " + styles.err.Bold(true).Render("y") + " clean   " + styles.bold.Render("n") + styles.dim.Render("/esc cancel"))
	return b.String()
}

func (m *Model) viewMove() string {
	var b strings.Builder
	b.WriteString(m.title() + "\n\n")
	fmt.Fprintf(&b, " %s\n\n", styles.bold.Render("Move "+m.moveTarget.e.Repo.Name+": "+m.moveTarget.name))
	b.WriteString(" " + m.moveInput.View() + "\n\n")
	b.WriteString(styles.dim.Render(" enter move · esc cancel"))
	return b.String()
}

// helpKeys lists the keys; key is the one checked against pickAllows, and
// entries with key "enter" are replaced in pick mode.
var helpKeys = []struct{ label, desc, key string }{
	{"↑ ↓ / k j", "move", "up"},
	{"pgup pgdown", "move half a page", "pgup"},
	{"g / G", "first / last worktree", "g"},
	{"space / x", "select or unselect", "x"},
	{"a", "select all in this repository", "a"},
	{"A", "select all shown", "A"},
	{"m", "select safe ones: merged or gone, nothing to lose, not locked", "m"},
	{"/", "search by path, branch or repository (esc clears)", "/"},
	{"s", "sort by name, size or activity (oldest first)", "s"},
	{"i", "show or hide the detail pane", "i"},
	{"enter / d", "delete selected (or the one under the cursor)", "enter"},
	{"o", "open in editor", "o"},
	{"t", "open in a new tmux window", "t"},
	{"c", "clean build artifacts (git-ignored files)", "c"},
	{"L", "lock or unlock", "L"},
	{"M", "move to another folder", "M"},
	{"R", "repair the repository's worktree links", "R"},
	{"r", "rescan", "r"},
	{"esc", "clear the search", "esc"},
	{"q", "quit", "q"},
}

func (m *Model) viewHelp() string {
	var b strings.Builder
	b.WriteString(m.title() + "\n\n")
	for _, k := range helpKeys {
		label, desc := k.label, k.desc
		if m.opts.Pick {
			if !m.pickAllows(k.key) {
				continue
			}
			switch {
			case k.key == "enter" && m.opts.Multi:
				label, desc = "enter", "print the selected paths (or the one under the cursor) and quit"
			case k.key == "enter":
				label, desc = "enter", "print the path of the worktree under the cursor and quit"
			case k.key == "q":
				desc = "quit without picking"
			}
		}
		b.WriteString("  " + styles.key.Render(pad(label, 13)) + " " + desc + "\n")
	}
	b.WriteString("\n" + styles.dim.Render(" press any key to go back"))
	return b.String()
}
