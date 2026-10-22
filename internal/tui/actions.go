package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"woma/internal/config"
	"woma/internal/core"
	"woma/internal/fsutil"
	"woma/internal/git"
)

const maxParallelDeletes = 3

// reinspect refreshes an item after an action changed it.
func (m *Model) reinspect(it *item) tea.Cmd {
	ctx, repo, wt := m.ctx, it.e.Repo, it.e.WT
	return tea.Batch(
		func() tea.Msg { return statusMsg{it: it, status: git.Inspect(ctx, repo, wt)} },
		func() tea.Msg { return sizeMsg{it: it, stats: fsutil.Measure(ctx, wt.Path)} },
	)
}

func (m *Model) rescan() tea.Cmd {
	m.cursorMoved = false
	return m.startScan()
}

// runOp runs fn in the background with a spinner and status text. Only one
// action runs at a time.
func (m *Model) runOp(desc string, fn func() tea.Msg) tea.Cmd {
	if m.opRunning != "" {
		m.setFlash("wait for "+m.opRunning+" to finish", true)
		return nil
	}
	m.opRunning = desc
	return tea.Batch(fn, m.startSpinner())
}

func (m *Model) confirmDeleteKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		return m.startDelete()
	case "b":
		m.deleteBranch = !m.deleteBranch
	case "n", "N", "esc", "q":
		m.cancelDelete()
	}
	return nil
}

func (m *Model) startDelete() tea.Cmd {
	m.autoSelected = nil
	sel := m.selected()
	m.state = stateDeleting
	m.deleting, m.deleted, m.finished = sel, 0, false

	type job struct {
		it *item
		e  core.Entry
	}
	jobs := make(chan job, len(sel))
	for _, it := range sel {
		it.done, it.err, it.note = false, nil, ""
		it.started.Store(false)
		it.freed.Store(0)
		jobs <- job{it, it.e}
	}
	close(jobs)
	results := make(chan deleteMsg, len(sel))
	m.deleteCh = results
	waitFor := func() tea.Cmd { return func() tea.Msg { return <-results } }

	deleteBranch := m.deleteBranch
	for range min(maxParallelDeletes, len(sel)) {
		go func() {
			for j := range jobs {
				j.it.started.Store(true)
				results <- deleteMsg{j.it, core.Remove(j.e, deleteBranch, &j.it.freed)}
			}
		}()
	}
	return tea.Batch(waitFor(), m.startSpinner())
}

func (m *Model) finishDelete(msg deleteMsg) tea.Cmd {
	it := msg.it
	it.done, it.err = true, msg.r.Err
	it.note = msg.r.BranchNote
	if msg.r.BranchDeleted {
		it.note = "branch " + it.wt().Branch + " deleted"
	}
	m.deleted++
	if it.err == nil {
		m.removed++
		m.freedSize += max(it.e.Size, it.freed.Load())
	}
	if m.deleted < len(m.deleting) {
		ch := m.deleteCh
		return func() tea.Msg { return <-ch }
	}

	m.finished = true
	for _, g := range m.groups {
		g.items = slices.DeleteFunc(g.items, func(it *item) bool { return it.done && it.err == nil })
	}
	m.groups = slices.DeleteFunc(m.groups, func(g *group) bool { return len(g.items) == 0 })
	m.rebuildRows()
	return nil
}

// editorCommand returns the command used to open a folder.
func editorCommand(cfg config.Config) []string {
	for _, c := range []string{cfg.Editor, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if fields := strings.Fields(c); len(fields) > 0 {
			return fields
		}
	}
	if runtime.GOOS == "darwin" {
		return []string{"open"}
	}
	return []string{"xdg-open"}
}

func (m *Model) openEditor() tea.Cmd {
	it := m.current()
	if it == nil || it.wt().Prunable {
		return nil
	}
	args := editorCommand(m.opts.Config)
	cmd := exec.Command(args[0], append(args[1:], it.wt().Path)...)
	name := it.name
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return opMsg{err: fmt.Errorf("open with %s: %w", args[0], err)}
		}
		return opMsg{flash: "opened " + name}
	})
}

func (m *Model) openTmux() tea.Cmd {
	it := m.current()
	if it == nil || it.wt().Prunable {
		return nil
	}
	if os.Getenv("TMUX") == "" {
		m.setFlash("not running inside tmux", true)
		return nil
	}
	path, name := it.wt().Path, filepath.Base(it.wt().Path)
	return m.runOp("opening tmux window", func() tea.Msg {
		out, err := exec.Command("tmux", "new-window", "-c", path, "-n", name).CombinedOutput()
		if err != nil {
			return opMsg{err: fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))}
		}
		return opMsg{flash: "opened tmux window " + name}
	})
}

func (m *Model) previewClean() tea.Cmd {
	it := m.current()
	if it == nil || it.wt().Prunable {
		return nil
	}
	path, keep := it.wt().Path, m.opts.Config.Copy
	return m.runOp("looking for build artifacts", func() tea.Msg {
		files, err := git.CleanIgnored(path, keep, true)
		return cleanPreviewMsg{it, files, err}
	})
}

func (m *Model) confirmCleanKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		m.state = stateList
		it, keep := m.cleanTarget, m.opts.Config.Copy
		path, name, before := it.wt().Path, it.name, it.e.Size
		return m.runOp("cleaning "+name, func() tea.Msg {
			if _, err := git.CleanIgnored(path, keep, false); err != nil {
				return opMsg{err: fmt.Errorf("clean: %w", err)}
			}
			return opMsg{it: it, flash: fmt.Sprintf("cleaned %s (was %s)", name, HumanBytes(before)), reinspect: true}
		})
	case "n", "N", "esc", "q":
		m.state = stateList
	}
	return nil
}

func (m *Model) toggleLock() tea.Cmd {
	it := m.current()
	if it == nil {
		return nil
	}
	common, path, name, locked := it.e.Repo.CommonDir, it.wt().Path, it.name, it.wt().Locked
	verb := map[bool]string{true: "unlocked", false: "locked"}[locked]
	return m.runOp("updating lock", func() tea.Msg {
		var err error
		if locked {
			err = git.Unlock(common, path)
		} else {
			err = git.Lock(common, path)
		}
		return opMsg{it: it, err: err, flash: verb + " " + name, apply: func() { it.e.WT.Locked = !locked }}
	})
}

func (m *Model) startMove() tea.Cmd {
	it := m.current()
	if it == nil || it.wt().Prunable {
		return nil
	}
	m.moveTarget = it
	m.moveInput.SetValue(it.wt().Path)
	m.moveInput.CursorEnd()
	m.state = stateMove
	return m.moveInput.Focus()
}

func (m *Model) moveKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.state = stateList
		m.moveInput.Blur()
		return nil
	case "enter":
		m.state = stateList
		m.moveInput.Blur()
		it := m.moveTarget
		dest, err := filepath.Abs(config.ExpandHome(strings.TrimSpace(m.moveInput.Value())))
		if err != nil || dest == it.wt().Path {
			return nil
		}
		// git would move the worktree *into* an existing folder, leaving it
		// somewhere other than dest.
		if _, err := os.Lstat(dest); err == nil {
			m.setFlash(Tildify(dest)+" already exists; choose a new path", true)
			return nil
		}
		common, path := it.e.Repo.CommonDir, it.wt().Path
		return m.runOp("moving "+it.name, func() tea.Msg {
			err := git.Move(common, path, dest)
			return opMsg{it: it, err: err, flash: "moved to " + Tildify(dest), apply: func() {
				it.e.WT.Path = dest
				it.name = m.displayPath(it.e.Repo, dest)
			}}
		})
	}
	var cmd tea.Cmd
	m.moveInput, cmd = m.moveInput.Update(msg)
	return cmd
}

func (m *Model) repair() tea.Cmd {
	it := m.current()
	if it == nil {
		return nil
	}
	repo := *it.e.Repo
	return m.runOp("repairing "+repo.Name, func() tea.Msg {
		out, err := git.Repair(repo)
		flash := "repaired " + repo.Name
		if out != "" {
			flash += ": " + strings.ReplaceAll(out, "\n", "; ")
		}
		return opMsg{err: err, flash: flash, rescan: true}
	})
}
