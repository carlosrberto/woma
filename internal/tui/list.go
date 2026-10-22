package tui

import (
	"slices"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"woma/internal/git"
)

func (m *Model) listKey(msg tea.KeyMsg) tea.Cmd {
	if m.searching {
		return m.searchKey(msg)
	}
	m.flash = ""
	var cmd tea.Cmd
	switch msg.String() {
	case "q":
		m.cancel()
		return tea.Quit
	case "esc":
		m.setFilter("")
	case "up", "k":
		m.userMove(-1)
	case "down", "j":
		m.userMove(1)
	case "pgup", "ctrl+u":
		m.userMove(-max(1, m.listHeight()/2))
	case "pgdown", "ctrl+d":
		m.userMove(max(1, m.listHeight()/2))
	case "home", "g":
		m.userMove(-len(m.rows))
	case "end", "G":
		m.userMove(len(m.rows))
	case " ", "space", "x":
		if it := m.current(); it != nil {
			it.selected = !it.selected
		}
	case "a":
		if it := m.current(); it != nil {
			toggleAll(m.visibleIn(m.groupOf(it)))
		}
	case "A":
		toggleAll(m.visible())
	case "m":
		m.selectSafe()
	case "s":
		m.sort = (m.sort + 1) % 3
		m.sortItems()
		m.rebuildRows()
	case "/":
		m.searching = true
		m.search.SetValue(m.filter)
		m.search.CursorEnd()
		return m.search.Focus()
	case "i":
		m.showDetail = !m.showDetail
	case "?":
		m.state = stateHelp
	case "enter":
		if m.opts.Pick {
			if it := m.current(); it != nil && !it.wt().Prunable {
				m.picked = it.wt().Path
				m.cancel()
				return tea.Quit
			}
			break
		}
		m.confirmDelete()
	case "d", "delete", "backspace":
		m.confirmDelete()
	case "o":
		cmd = m.openEditor()
	case "t":
		cmd = m.openTmux()
	case "c":
		cmd = m.previewClean()
	case "L":
		cmd = m.toggleLock()
	case "M":
		cmd = m.startMove()
	case "R":
		cmd = m.repair()
	case "r":
		cmd = m.rescan()
	}
	m.scrollToCursor()
	return cmd
}

func (m *Model) searchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.search.Blur()
		m.setFilter("")
		return nil
	case "enter", "down", "up":
		m.searching = false
		m.search.Blur()
		return nil
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.setFilter(m.search.Value())
	return cmd
}

func (m *Model) setFilter(f string) {
	m.filter = f
	m.rebuildRows()
}

func (m *Model) matches(it *item) bool {
	if m.filter == "" {
		return true
	}
	haystack := strings.ToLower(it.name + " " + it.wt().Branch + " " + it.e.Repo.Name)
	return strings.Contains(haystack, strings.ToLower(m.filter))
}

func (m *Model) confirmDelete() {
	m.autoSelected = nil
	if len(m.selected()) == 0 {
		if it := m.current(); it != nil {
			it.selected = true
			m.autoSelected = it
		}
	}
	sel := m.selected()
	if len(sel) == 0 {
		return
	}
	// Whether deleting loses work is only known once inspected.
	for _, it := range sel {
		if !it.inspected {
			m.setFlash("still inspecting "+it.name+"; try again in a moment", true)
			m.cancelDelete()
			return
		}
	}
	m.state = stateConfirmDelete
}

func (m *Model) cancelDelete() {
	if m.autoSelected != nil {
		m.autoSelected.selected = false
		m.autoSelected = nil
	}
	m.state = stateList
}

// selectSafe selects every visible worktree that is safe to remove, or
// unselects them all when they already are.
func (m *Model) selectSafe() {
	var safe []*item
	for _, it := range m.visible() {
		if it.inspected && it.e.Safe() {
			safe = append(safe, it)
		}
	}
	if len(safe) == 0 {
		m.setFlash("no merged or gone worktrees without changes", false)
		return
	}
	toggleAll(safe)
}

// toggleAll selects every item, or deselects them all if all are selected.
func toggleAll(items []*item) {
	all := !slices.ContainsFunc(items, func(it *item) bool { return !it.selected })
	for _, it := range items {
		it.selected = !all
	}
}

// addRepo adds a newly found repository and starts inspecting its worktrees.
func (m *Model) addRepo(repo git.Repo, current bool) {
	r := &repo
	g := &group{repo: r, current: current}
	for _, wt := range r.Worktrees {
		it := &item{name: m.displayPath(r, wt.Path)}
		it.e.Repo, it.e.WT = r, wt
		g.items = append(g.items, it)
		m.inspect(it)
	}
	m.groups = append(m.groups, g)
	m.sortItems()
	m.rebuildRows()
}

func (m *Model) allItems() []*item {
	var all []*item
	for _, g := range m.groups {
		all = append(all, g.items...)
	}
	return all
}

func (m *Model) visible() []*item {
	var v []*item
	for _, r := range m.rows {
		if r.item != nil {
			v = append(v, r.item)
		}
	}
	return v
}

func (m *Model) visibleIn(g *group) []*item {
	var v []*item
	for _, it := range g.items {
		if m.matches(it) {
			v = append(v, it)
		}
	}
	return v
}

func (m *Model) selected() []*item {
	var sel []*item
	for _, it := range m.allItems() {
		if it.selected {
			sel = append(sel, it)
		}
	}
	return sel
}

func (m *Model) groupOf(it *item) *group {
	for _, g := range m.groups {
		if slices.Contains(g.items, it) {
			return g
		}
	}
	return nil
}

func groupSize(g *group) int64 {
	var total int64
	for _, it := range g.items {
		total += it.e.Size
	}
	return total
}

func (m *Model) less(a, b *item) bool {
	switch m.sort {
	case sortSize:
		if a.e.Size != b.e.Size {
			return a.e.Size > b.e.Size
		}
	case sortActivity:
		if ta, tb := a.e.LastActivity(), b.e.LastActivity(); !ta.Equal(tb) {
			return ta.Before(tb)
		}
	}
	return a.name < b.name
}

func (m *Model) sortItems() {
	for _, g := range m.groups {
		sort.SliceStable(g.items, func(i, j int) bool { return m.less(g.items[i], g.items[j]) })
	}
	sort.SliceStable(m.groups, func(i, j int) bool {
		a, b := m.groups[i], m.groups[j]
		if a.current != b.current {
			return a.current
		}
		if m.sort == sortSize {
			if sa, sb := groupSize(a), groupSize(b); sa != sb {
				return sa > sb
			}
		}
		return a.repo.Name < b.repo.Name
	})
}

// rebuildRows flattens visible items into rows, keeping the cursor on the
// same item when it is still visible.
func (m *Model) rebuildRows() {
	cur := m.current()
	if !m.cursorMoved {
		cur, m.cursor = nil, 0
	}
	oldCursor := m.cursor

	m.rows = m.rows[:0]
	m.cursor = -1
	for _, g := range m.groups {
		items := m.visibleIn(g)
		if len(items) == 0 {
			continue
		}
		m.rows = append(m.rows, row{group: g})
		for _, it := range items {
			if it == cur {
				m.cursor = len(m.rows)
			}
			m.rows = append(m.rows, row{group: g, item: it})
		}
	}
	if m.cursor < 0 {
		m.cursor = max(0, min(oldCursor, len(m.rows)-1))
		if m.current() == nil {
			m.move(1)
		}
		if m.current() == nil {
			m.move(-1)
		}
	}
	m.scrollToCursor()
}

func (m *Model) userMove(n int) {
	m.cursorMoved = true
	m.move(n)
}

// move moves the cursor n items up (negative) or down, skipping headers.
func (m *Model) move(n int) {
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	for i := m.cursor + step; n > 0 && i >= 0 && i < len(m.rows); i += step {
		if m.rows[i].item != nil {
			m.cursor = i
			n--
		}
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) current() *item {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor].item
	}
	return nil
}

const detailHeight = 8

func (m *Model) detailVisible() bool {
	return m.showDetail && m.height >= 20
}

func (m *Model) listHeight() int {
	h := m.height - 6
	if m.detailVisible() {
		h -= detailHeight
	}
	return max(3, h)
}

func (m *Model) scrollToCursor() {
	h := m.listHeight()
	top := m.cursor
	if top > 0 && top < len(m.rows) && m.rows[top-1].item == nil {
		top-- // keep the repository header in view
	}
	if top < m.offset {
		m.offset = top
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, len(m.rows)-h))
}
