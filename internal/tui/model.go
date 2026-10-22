// Package tui implements woma's interactive worktree browser.
package tui

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"woma/internal/config"
	"woma/internal/core"
	"woma/internal/git"
)

type state int

const (
	stateScanning state = iota
	stateList
	stateConfirmDelete
	stateDeleting
	stateConfirmClean
	stateMove
	stateHelp
)

type sortMode int

const (
	sortName sortMode = iota
	sortSize
	sortActivity // least recently active first
)

func (s sortMode) String() string {
	return [...]string{"name", "size", "activity"}[s]
}

type item struct {
	e         core.Entry
	name      string // path shown in the list
	inspected bool
	selected  bool

	// Deletion state. started and freed are written by delete workers.
	started atomic.Bool
	freed   atomic.Int64
	done    bool
	err     error
	note    string
}

func (it *item) wt() git.Worktree { return it.e.WT }

type group struct {
	repo  *git.Repo
	items []*item
}

// row is a line of the list: a repository header when item is nil.
type row struct {
	group *group
	item  *item
}

type (
	scanDoneMsg struct {
		repos []git.Repo
		err   error
	}
	inspectMsg struct {
		it      *item
		e       core.Entry
		initial bool // part of the scan, counted in the progress bar
	}
	deleteMsg struct {
		it *item
		r  core.Removal
	}
	cleanPreviewMsg struct {
		it    *item
		files []string
		err   error
	}
	// opMsg reports a finished action. apply, if set, runs in Update to
	// change model state; reinspect refreshes the item afterwards and
	// rescan reloads everything.
	opMsg struct {
		it        *item
		flash     string
		err       error
		apply     func()
		reinspect bool
		rescan    bool
	}
)

// Options configure the TUI.
type Options struct {
	Roots  []string
	Config config.Config
	// Pick makes enter choose the worktree under the cursor and quit;
	// Picked then returns its path.
	Pick bool
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// Model is the Bubble Tea model for woma.
type Model struct {
	opts   Options
	ctx    context.Context
	cancel context.CancelFunc
	now    func() time.Time

	state         state
	width, height int
	spinner       spinner.Model
	spinning      bool
	bar           progress.Model
	scan          *git.ScanProgress
	err           error

	groups     []*group
	rows       []row
	cursor     int
	offset     int
	sort       sortMode
	showDetail bool

	search    textinput.Model
	searching bool
	filter    string

	inspectCh    chan inspectMsg
	inspectTotal int
	inspectDone  int

	deleteCh     chan deleteMsg
	deleting     []*item
	deleted      int
	finished     bool
	deleteBranch bool
	removed      int
	freedSize    int64

	// autoSelected is the item confirmDelete selected because nothing was;
	// cancelling unselects it again.
	autoSelected *item

	cleanTarget *item
	cleanFiles  []string
	moveInput   textinput.Model
	moveTarget  *item

	opRunning string // description of the running action, if any
	flash     string
	flashErr  bool
	picked    string
}

// New returns a model that scans opts.Roots for worktrees.
func New(opts Options) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	search := textinput.New()
	search.Prompt = "/"
	search.Placeholder = "filter by path, branch or repo"
	move := textinput.New()
	move.Prompt = "new path: "
	return &Model{
		opts:       opts,
		ctx:        ctx,
		cancel:     cancel,
		now:        now,
		width:      80,
		height:     24,
		spinner:    spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(styles.spinner)),
		spinning:   true,
		bar:        newBar(),
		scan:       &git.ScanProgress{},
		search:     search,
		moveInput:  move,
		showDetail: true,
	}
}

// Err returns the error that aborted the scan, if any.
func (m *Model) Err() error { return m.err }

// Summary returns how many worktrees were removed and the space freed.
func (m *Model) Summary() (int, int64) { return m.removed, m.freedSize }

// Picked returns the path chosen in pick mode, or "".
func (m *Model) Picked() string { return m.picked }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startScan())
}

func (m *Model) startScan() tea.Cmd {
	ctx, roots, progress := m.ctx, m.opts.Roots, m.scan
	opts := git.ScanOptions{Ignore: m.opts.Config.Ignore, MaxDepth: m.opts.Config.MaxDepth}
	return func() tea.Msg {
		repos, err := git.Discover(ctx, roots, opts, progress)
		return scanDoneMsg{repos, err}
	}
}

func waitFor[T any](ch <-chan T) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m *Model) busy() bool {
	return m.state == stateScanning ||
		m.inspectDone < m.inspectTotal ||
		m.opRunning != "" ||
		(m.state == stateDeleting && !m.finished)
}

func (m *Model) startSpinner() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return m.spinner.Tick
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.bar.Width = max(10, min(40, m.width-50))
		m.scrollToCursor()
		return m, nil

	case spinner.TickMsg:
		if !m.busy() {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case scanDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.buildGroups(msg.repos)
		m.state = stateList
		return m, m.startInspecting()

	case inspectMsg:
		// Only take what inspection measured: the worktree itself (path,
		// lock) may have changed through an action since the job started.
		it := msg.it
		it.e.Size, it.e.Newest, it.e.Status, it.inspected = msg.e.Size, msg.e.Newest, msg.e.Status, true
		if msg.initial {
			m.inspectDone++
		}
		if m.sort != sortName {
			m.sortItems()
			m.rebuildRows()
		}
		if msg.initial && m.inspectDone < m.inspectTotal {
			return m, waitFor(m.inspectCh)
		}
		return m, nil

	case deleteMsg:
		return m, m.finishDelete(msg)

	case cleanPreviewMsg:
		m.opRunning = ""
		switch {
		case msg.err != nil:
			m.setFlash("clean: "+msg.err.Error(), true)
		case len(msg.files) == 0:
			m.setFlash("nothing to clean in "+msg.it.name, false)
		default:
			m.cleanTarget, m.cleanFiles = msg.it, msg.files
			m.state = stateConfirmClean
		}
		return m, nil

	case opMsg:
		m.opRunning = ""
		if msg.err != nil {
			m.setFlash(msg.err.Error(), true)
			return m, nil
		}
		if msg.apply != nil {
			msg.apply()
		}
		m.setFlash(msg.flash, false)
		m.sortItems()
		m.rebuildRows()
		switch {
		case msg.rescan:
			return m, m.rescan()
		case msg.reinspect && msg.it != nil:
			return m, m.reinspect(msg.it)
		}
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.state == stateDeleting && !m.finished {
				// Quitting now would kill deletions halfway.
				m.setFlash("deleting… ctrl+c is disabled until it finishes", true)
				return m, nil
			}
			m.cancel()
			return m, tea.Quit
		}
		switch m.state {
		case stateList:
			return m, m.listKey(msg)
		case stateConfirmDelete:
			return m, m.confirmDeleteKey(msg)
		case stateConfirmClean:
			return m, m.confirmCleanKey(msg)
		case stateMove:
			return m, m.moveKey(msg)
		case stateHelp:
			m.state = stateList
		case stateDeleting:
			if m.finished {
				m.state = stateList
				m.deleting = nil
			}
		}
	}
	return m, nil
}

func (m *Model) setFlash(msg string, isErr bool) {
	m.flash, m.flashErr = msg, isErr
}
