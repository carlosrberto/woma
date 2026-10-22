package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/carlosrberto/woma/internal/config"
	"github.com/carlosrberto/woma/internal/core"
	"github.com/carlosrberto/woma/internal/git"
	"github.com/carlosrberto/woma/internal/tui"
)

type filterFlags struct {
	merged, gone, stale, clean, safe bool
	olderThan                        string
}

func addFilterFlags(fs *flag.FlagSet) *filterFlags {
	f := &filterFlags{}
	fs.BoolVar(&f.merged, "merged", false, "only worktrees whose branch is merged into the default branch")
	fs.BoolVar(&f.gone, "gone", false, "only worktrees whose remote branch was deleted")
	fs.BoolVar(&f.stale, "stale", false, "only worktrees without activity for stale_after (default 30d)")
	fs.BoolVar(&f.clean, "clean", false, "only worktrees whose removal loses nothing (no uncommitted changes)")
	fs.BoolVar(&f.safe, "safe", false, "only merged/gone/missing worktrees that are clean and not locked")
	fs.StringVar(&f.olderThan, "older-than", "", "only worktrees without activity for this long (e.g. 30d, 2w, 6mo)")
	return f
}

func (f *filterFlags) filter(cfg config.Config) (core.Filter, error) {
	older, err := config.ParseAge(f.olderThan)
	if err != nil {
		return core.Filter{}, err
	}
	return core.Filter{
		Merged: f.merged, Gone: f.gone, Stale: f.stale, Clean: f.clean, Safe: f.safe,
		OlderThan: older, StaleAfter: cfg.StaleDuration(),
	}, nil
}

// collect scans roots, inspects every worktree and returns those matching
// filter, sorted by repository and path. Progress goes to stderr when it is
// a terminal.
func collect(roots []string, cfg config.Config, filter core.Filter, stderr io.Writer) ([]core.Entry, error) {
	ctx := context.Background()
	progress := &git.ScanProgress{}
	showProgress := isTerminal(stderr)
	done, stopped := make(chan struct{}), make(chan struct{})
	var inspected, total int
	if showProgress {
		go func() {
			defer close(stopped)
			t := time.NewTicker(100 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-done:
					fmt.Fprint(stderr, "\r\033[K")
					return
				case <-t.C:
					fmt.Fprintf(stderr, "\r\033[Kscanning: %d directories, %d repositories", progress.Dirs.Load(), progress.Found.Load())
				}
			}
		}()
	} else {
		close(stopped)
	}
	repos, err := git.Discover(ctx, roots, git.ScanOptions{Ignore: cfg.Ignore, MaxDepth: cfg.MaxDepth}, progress)
	close(done)
	<-stopped // the scan line is cleared before inspection progress starts
	if err != nil {
		return nil, err
	}
	for _, r := range repos {
		total += len(r.Worktrees)
	}

	now := time.Now()
	var entries []core.Entry
	core.InspectAll(ctx, repos, func(e core.Entry) {
		inspected++
		if showProgress {
			fmt.Fprintf(stderr, "\r\033[Kinspecting: %d/%d", inspected, total)
		}
		if filter.Match(e, now) {
			entries = append(entries, e)
		}
	})
	if showProgress {
		fmt.Fprint(stderr, "\r\033[K")
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Repo.Name != b.Repo.Name {
			return a.Repo.Name < b.Repo.Name
		}
		return a.WT.Path < b.WT.Path
	})
	return entries, nil
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

type jsonEntry struct {
	Repo         string    `json:"repo"`
	RepoPath     string    `json:"repo_path"`
	Path         string    `json:"path"`
	Branch       string    `json:"branch,omitempty"`
	Head         string    `json:"head"`
	Upstream     string    `json:"upstream,omitempty"`
	Size         int64     `json:"size"`
	LastActivity time.Time `json:"last_activity,omitzero"`
	Merged       bool      `json:"merged"`
	NoCommits    bool      `json:"no_commits"`
	Gone         bool      `json:"gone"`
	Stale        bool      `json:"stale"`
	Dirty        bool      `json:"dirty"`
	Locked       bool      `json:"locked"`
	Missing      bool      `json:"missing"`
	Ahead        int       `json:"ahead"`
	Behind       int       `json:"behind"`
	Unpushed     int       `json:"unpushed"`
	Safe         bool      `json:"safe"`
}

func runList(args []string, cfg config.Config, stdout, stderr io.Writer) error {
	fs := newFlagSet("list", "woma list [path...] [filters] [--json]", stderr)
	filters := addFilterFlags(fs)
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	paths, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	filter, err := filters.filter(cfg)
	if err != nil {
		return err
	}
	roots, err := resolveRoots(paths, cfg)
	if err != nil {
		return err
	}
	entries, err := collect(roots, cfg, filter, stderr)
	if err != nil {
		return err
	}

	now := time.Now()
	stale := cfg.StaleDuration()
	if *asJSON {
		out := make([]jsonEntry, 0, len(entries))
		for _, e := range entries {
			out = append(out, jsonEntry{
				Repo: e.Repo.Name, RepoPath: e.Repo.Path, Path: e.WT.Path, Branch: e.WT.Branch, Head: e.WT.Head,
				Upstream: e.Status.Upstream, Size: e.Size, LastActivity: e.LastActivity(),
				Merged: e.Status.Merged, NoCommits: e.Status.NoCommits, Gone: e.Status.Gone, Stale: e.Stale(stale, now), Dirty: e.Status.Dirty,
				Locked: e.WT.Locked, Missing: e.WT.Prunable, Ahead: e.Status.Ahead, Behind: e.Status.Behind,
				Unpushed: e.Status.Unpushed, Safe: e.Safe(),
			})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(entries) == 0 {
		fmt.Fprintln(stderr, "no worktrees found")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tWORKTREE\tBRANCH\tACTIVE\tSIZE\tLABELS")
	var total int64
	for _, e := range entries {
		total += e.Size
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Repo.Name, tui.Tildify(e.WT.Path), tui.BranchLabel(e.WT),
			tui.Age(e.LastActivity(), now), tui.HumanBytes(e.Size), labelText(e, stale, now))
	}
	tw.Flush()
	fmt.Fprintf(stderr, "%d worktree(s), %s\n", len(entries), tui.HumanBytes(total))
	return nil
}

func labelText(e core.Entry, stale time.Duration, now time.Time) string {
	var parts []string
	for _, l := range e.Labels(stale, now) {
		parts = append(parts, l.Text)
	}
	return strings.Join(parts, ",")
}
