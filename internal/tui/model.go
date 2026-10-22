// Package tui implements woma's interactive worktree browser.
package tui

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/carlosrberto/woma/internal/config"
	"github.com/carlosrberto/woma/internal/core"
	"github.com/carlosrberto/woma/internal/fsutil"
	"github.com/carlosrberto/woma/internal/git"
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
	inspected bool   // git status known (needed before deleting)
	measured  bool   // size and file activity known
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
	repo    *git.Repo
	items   []*item
	current bool // the repository woma was started in; listed first
}

// row is a line of the list: a repository header when item is nil.
type row struct {
	group *group
	item  *item
}

type (
	// envelope wraps messages streamed from a scan, so ones from a scan
	// that was replaced by a rescan can be dropped.
	envelope struct {
		gen int
		msg tea.Msg
	}
	repoMsg struct {
		repo    git.Repo
		current bool
	}
	scanDoneMsg struct {
		err error
	}
	// statusMsg and sizeMsg carry the two halves of inspecting a worktree:
	// the quick git checks and the slow disk walk. initial marks the ones
	// counted in the scan's progress.
	statusMsg struct {
		it      *item
		status  git.Status
		initial bool
	}
	sizeMsg struct {
		it      *item
		stats   fsutil.Stats
		initial bool
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
	// Picked then returns its path. Actions that change worktrees are
	// disabled.
	Pick bool
	// Multi (with Pick) allows selecting several worktrees to pick.
	Multi bool
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

	groups []*group
	rows   []row
	cursor int
	offset int
	// cursorMoved is set once the user navigates; until then the cursor
	// stays on the first row while repositories stream in.
	cursorMoved bool
	sort        sortMode
	showDetail  bool

	search    textinput.Model
	searching bool
	filter    string

	// Streaming scan state. Events from goroutines arrive on events; gen
	// identifies the current scan.
	gen        int
	events     chan tea.Msg
	scanCtx    context.Context
	scanCancel context.CancelFunc
	scanning   bool
	gitSem     chan struct{}
	sizeSem    chan struct{}
	checkTotal int
	checkDone  int
	sizeTotal  int
	sizeDone   int

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
	picked    []string
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

// Picked returns the paths chosen in pick mode, if any.
func (m *Model) Picked() []string { return m.picked }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startScan())
}

// startScan (re)starts scanning. Repositories stream in as they are found
// (the one woma was started in first) and each worktree is inspected right
// away: git checks and disk usage run in separate pools so labels don't wait
// for sizes.
func (m *Model) startScan() tea.Cmd {
	if m.scanCancel != nil {
		m.scanCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.scanCtx, m.scanCancel = ctx, cancel
	m.gen++
	m.events = make(chan tea.Msg)
	m.scanning = true
	m.scan = &git.ScanProgress{}
	m.state = stateScanning
	m.groups, m.rows = nil, nil
	m.checkTotal, m.checkDone, m.sizeTotal, m.sizeDone = 0, 0, 0, 0
	m.gitSem = make(chan struct{}, runtime.NumCPU())
	m.sizeSem = make(chan struct{}, max(2, runtime.NumCPU()/2))

	events, roots, progress := m.events, m.opts.Roots, m.scan
	opts := git.ScanOptions{Ignore: m.opts.Config.Ignore, MaxDepth: m.opts.Config.MaxDepth}
	go func() {
		err := git.DiscoverEach(ctx, roots, opts, progress, func(r git.Repo, current bool) {
			send(ctx, events, repoMsg{r, current})
		})
		send(ctx, events, scanDoneMsg{err})
	}()
	return tea.Batch(m.listen(), m.startSpinner())
}

// send delivers msg unless the scan was cancelled.
func send(ctx context.Context, events chan<- tea.Msg, msg tea.Msg) {
	select {
	case events <- msg:
	case <-ctx.Done():
	}
}

// acquire takes a slot of sem, or reports false if the scan was cancelled.
func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// listen waits for the next event of the current scan.
func (m *Model) listen() tea.Cmd {
	events, gen := m.events, m.gen
	return func() tea.Msg { return envelope{gen, <-events} }
}

// streaming reports whether more scan events are expected.
func (m *Model) streaming() bool {
	return m.scanning || m.checkDone < m.checkTotal || m.sizeDone < m.sizeTotal
}

// inspect starts the git checks and the size measurement of a new item.
func (m *Model) inspect(it *item) {
	ctx, events, gitSem, sizeSem := m.scanCtx, m.events, m.gitSem, m.sizeSem
	repo, wt := it.e.Repo, it.e.WT
	m.checkTotal++
	go func() {
		if !acquire(ctx, gitSem) {
			return
		}
		status := git.Inspect(ctx, repo, wt)
		<-gitSem
		send(ctx, events, statusMsg{it, status, true})
	}()
	if wt.Prunable {
		it.measured = true
		return
	}
	m.sizeTotal++
	go func() {
		if !acquire(ctx, sizeSem) {
			return
		}
		stats := fsutil.Measure(ctx, wt.Path)
		<-sizeSem
		send(ctx, events, sizeMsg{it, stats, true})
	}()
}

// handleEvent applies a scan or inspection result.
func (m *Model) handleEvent(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case repoMsg:
		m.addRepo(msg.repo, msg.current)
		if m.state == stateScanning {
			m.state = stateList
		}
	case scanDoneMsg:
		m.scanning = false
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			m.err = msg.err
			return tea.Quit
		}
		if m.state == stateScanning {
			m.state = stateList
		}
	case statusMsg:
		// Only take what inspection found: the worktree itself (path, lock)
		// may have changed through an action since the job started.
		msg.it.e.Status, msg.it.inspected = msg.status, true
		if msg.initial {
			m.checkDone++
		}
		if m.sort == sortActivity {
			m.sortItems()
			m.rebuildRows()
		}
	case sizeMsg:
		msg.it.e.Size, msg.it.e.Newest, msg.it.measured = msg.stats.Size, msg.stats.Newest, true
		if msg.initial {
			m.sizeDone++
		}
		if m.sort != sortName {
			m.sortItems()
			m.rebuildRows()
		}
	}
	return nil
}

func (m *Model) busy() bool {
	return m.state == stateScanning ||
		m.streaming() ||
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

	case envelope:
		if msg.gen != m.gen {
			return m, nil // from a scan that was replaced
		}
		cmd := m.handleEvent(msg.msg)
		if m.streaming() {
			cmd = tea.Batch(cmd, m.listen())
		}
		return m, cmd

	case statusMsg, sizeMsg: // refreshes after an action
		return m, m.handleEvent(msg)

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
