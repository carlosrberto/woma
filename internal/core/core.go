// Package core holds the worktree logic shared by the CLI and the TUI:
// inspection, labels, filters, safety rules, removal and creation.
package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/carlosrberto/woma/internal/fsutil"
	"github.com/carlosrberto/woma/internal/git"
)

// Entry is a worktree with everything woma knows about it.
type Entry struct {
	Repo   *git.Repo
	WT     git.Worktree
	Size   int64
	Newest time.Time // newest file modification in the worktree
	Status git.Status
}

// Inspect measures a worktree on disk and reads its git status.
func Inspect(ctx context.Context, repo *git.Repo, wt git.Worktree) Entry {
	e := Entry{Repo: repo, WT: wt}
	if !wt.Prunable {
		st := fsutil.Measure(ctx, wt.Path)
		e.Size, e.Newest = st.Size, st.Newest
	}
	e.Status = git.Inspect(ctx, repo, wt)
	return e
}

// InspectAll inspects every worktree of repos in parallel, calling fn (from
// a single goroutine at a time) as each one finishes.
func InspectAll(ctx context.Context, repos []git.Repo, fn func(Entry)) {
	jobs := make(chan Entry)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				e := Inspect(ctx, j.Repo, j.WT)
				mu.Lock()
				fn(e)
				mu.Unlock()
			}
		}()
	}
	for i := range repos {
		for _, wt := range repos[i].Worktrees {
			jobs <- Entry{Repo: &repos[i], WT: wt}
		}
	}
	close(jobs)
	wg.Wait()
}

// LastActivity is the latest of the last commit and the newest file change.
func (e Entry) LastActivity() time.Time {
	if e.Newest.After(e.Status.LastCommit) {
		return e.Newest
	}
	return e.Status.LastCommit
}

// Stale reports whether there was no activity for longer than after.
func (e Entry) Stale(after time.Duration, now time.Time) bool {
	last := e.LastActivity()
	return after > 0 && !last.IsZero() && now.Sub(last) > after
}

// LosesWork reports whether removing the worktree loses anything: uncommitted
// changes, or commits on a detached HEAD that no branch or remote contains.
// Commits on a branch are never lost because branches are kept. When the
// inspection was incomplete it assumes the worst.
func (e Entry) LosesWork() bool {
	return !e.Status.Complete || e.Status.Dirty || (e.WT.Branch == "" && e.Status.Unpushed > 0)
}

// Safe reports whether the worktree looks done with (merged, its remote
// branch deleted, or its folder already gone) and removing it loses nothing.
func (e Entry) Safe() bool {
	done := e.Status.Merged || e.Status.Gone || e.WT.Prunable
	return done && !e.LosesWork() && !e.WT.Locked
}

// CanDeleteBranch reports whether the branch can be deleted without losing
// commits: it is merged into the default branch or fully pushed.
func (e Entry) CanDeleteBranch() bool {
	b := e.WT.Branch
	return e.Status.Complete && b != "" && b != e.Repo.BaseBranch && (e.Status.Merged || e.Status.Unpushed == 0)
}

// LabelKind says how a label should be highlighted.
type LabelKind int

const (
	LabelInfo   LabelKind = iota // neutral
	LabelGood                    // done with: merged, gone
	LabelWarn                    // needs attention: modified, unpushed
	LabelDanger                  // locked
)

// Label is a short tag describing a worktree.
type Label struct {
	Text string
	Kind LabelKind
}

// Labels returns the tags shown next to a worktree.
func (e Entry) Labels(staleAfter time.Duration, now time.Time) []Label {
	var l []Label
	add := func(cond bool, text string, kind LabelKind) {
		if cond {
			l = append(l, Label{text, kind})
		}
	}
	add(!e.Status.Complete, "unknown", LabelWarn)
	add(e.WT.Prunable, "missing", LabelInfo)
	add(e.WT.Locked, "locked", LabelDanger)
	add(e.Status.Dirty, "modified", LabelWarn)
	add(e.Status.Unpushed > 0, fmt.Sprintf("%d unpushed", e.Status.Unpushed), LabelWarn)
	add(e.Status.Merged, "merged", LabelGood)
	add(e.Status.NoCommits, "no commits", LabelInfo)
	add(e.Status.Gone, "gone", LabelGood)
	add(e.Stale(staleAfter, now), "stale", LabelInfo)
	return l
}

// Filter selects worktrees; all set conditions must hold.
type Filter struct {
	Merged    bool
	Gone      bool
	Stale     bool
	Clean     bool // removing it loses nothing
	Safe      bool
	OlderThan time.Duration // no activity for this long

	StaleAfter time.Duration // threshold used by Stale
}

// IsZero reports whether no condition is set.
func (f Filter) IsZero() bool {
	return !f.Merged && !f.Gone && !f.Stale && !f.Clean && !f.Safe && f.OlderThan == 0
}

// Match reports whether e satisfies every condition of f.
func (f Filter) Match(e Entry, now time.Time) bool {
	switch {
	case f.Merged && !e.Status.Merged,
		f.Gone && !e.Status.Gone,
		f.Stale && !e.Stale(f.StaleAfter, now),
		f.Clean && e.LosesWork(),
		f.Safe && !e.Safe(),
		f.OlderThan > 0 && !e.Stale(f.OlderThan, now):
		return false
	}
	return true
}

// Removal is the outcome of Remove.
type Removal struct {
	Err           error
	BranchDeleted bool
	BranchNote    string // why the branch was kept, when it was
}

// Remove deletes a worktree, reporting freed bytes as files are deleted.
// With deleteBranch, its branch is deleted too when CanDeleteBranch allows.
//
// The folder is first renamed aside and git unregisters the worktree; only
// then are files deleted. If git fails the folder is put back untouched, and
// an interruption leaves at most a hidden ".<name>.woma-deleting-*" folder
// that is no longer a worktree.
func Remove(e Entry, deleteBranch bool, freed *atomic.Int64) Removal {
	path := e.WT.Path
	if _, err := os.Lstat(path); err == nil {
		if !fsutil.IsLinkedWorktree(path) {
			return Removal{Err: fmt.Errorf("%s is not a linked worktree", path)}
		}
		trash := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.woma-deleting-%d", filepath.Base(path), time.Now().UnixNano()))
		if err := os.Rename(path, trash); err != nil {
			return Removal{Err: err}
		}
		if err := git.RemoveWorktree(e.Repo.CommonDir, path); err != nil {
			if restoreErr := os.Rename(trash, path); restoreErr != nil {
				err = fmt.Errorf("%v; restoring the folder also failed, it is at %s", err, trash)
			}
			return Removal{Err: err}
		}
		if err := fsutil.RemoveTree(trash, freed); err != nil {
			return Removal{Err: fmt.Errorf("worktree removed from git, but %w", err)}
		}
	} else if err := git.RemoveWorktree(e.Repo.CommonDir, path); err != nil {
		return Removal{Err: err}
	}
	var r Removal
	if deleteBranch && e.WT.Branch != "" {
		switch {
		case !e.CanDeleteBranch():
			r.BranchNote = fmt.Sprintf("kept branch %s: it has unpushed commits", e.WT.Branch)
		default:
			if err := git.DeleteBranch(e.Repo.CommonDir, e.WT.Branch); err != nil {
				r.BranchNote = fmt.Sprintf("kept branch %s: %v", e.WT.Branch, err)
			} else {
				r.BranchDeleted = true
			}
		}
	}
	return r
}
