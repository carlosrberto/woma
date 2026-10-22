package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/term"

	"woma/internal/config"
	"woma/internal/core"
	"woma/internal/tui"
)

func runClean(args []string, cfg config.Config, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := newFlagSet("clean", "woma clean [path...] [filters] [--dry-run] [--yes]", stderr)
	filters := addFilterFlags(fs)
	all := fs.Bool("all", false, "match every worktree (filters still apply)")
	dryRun := fs.Bool("dry-run", false, "show what would be deleted without deleting")
	yes := fs.Bool("yes", false, "don't ask for confirmation")
	force := fs.Bool("force", false, "also delete worktrees that are locked or would lose uncommitted work")
	deleteBranch := fs.Bool("delete-branch", false, "also delete each worktree's branch when merged or fully pushed")
	paths, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	filter, err := filters.filter(cfg)
	if err != nil {
		return err
	}
	if filter.IsZero() && !*all {
		return fmt.Errorf("choose what to clean with filters (--merged, --gone, --stale, --safe, --older-than, --clean) or --all")
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
	var targets []core.Entry
	var total int64
	for _, e := range entries {
		var reason string
		switch {
		case *force:
		case !e.Status.Complete:
			reason = "could not be fully inspected"
		case e.WT.Locked:
			reason = "locked"
		case e.Status.Dirty:
			reason = "has uncommitted changes"
		case e.LosesWork():
			reason = "has commits on no branch"
		}
		if reason != "" {
			fmt.Fprintf(stdout, "skip    %s (%s; use --force)\n", tui.Tildify(e.WT.Path), reason)
			continue
		}
		targets = append(targets, e)
		total += e.Size
	}
	if len(targets) == 0 {
		fmt.Fprintln(stdout, "nothing to clean")
		return nil
	}

	for _, e := range targets {
		branch := ""
		if *deleteBranch && e.WT.Branch != "" {
			if e.CanDeleteBranch() {
				branch = "  + delete branch " + e.WT.Branch
			} else {
				branch = "  (branch " + e.WT.Branch + " kept: unpushed commits)"
			}
		}
		fmt.Fprintf(stdout, "delete  %s  %s  %s  [%s]%s\n", e.Repo.Name, tui.Tildify(e.WT.Path),
			tui.HumanBytes(e.Size), labelText(e, stale, now), branch)
	}
	summary := fmt.Sprintf("%d worktree(s), %s", len(targets), tui.HumanBytes(total))
	if *dryRun {
		fmt.Fprintf(stdout, "dry run: would delete %s\n", summary)
		return nil
	}
	if !*yes {
		f, ok := stdin.(*os.File)
		if !ok || !term.IsTerminal(f.Fd()) {
			return fmt.Errorf("not deleting without confirmation; pass --yes")
		}
		fmt.Fprintf(stdout, "Delete %s? [y/N] ", summary)
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout, "cancelled")
			return nil
		}
	}

	var removed int
	var freed int64
	failed := false
	for _, e := range targets {
		var counter atomic.Int64
		r := core.Remove(e, *deleteBranch, &counter)
		if r.Err != nil {
			failed = true
			fmt.Fprintf(stdout, "failed  %s: %v\n", tui.Tildify(e.WT.Path), r.Err)
			continue
		}
		removed++
		freed += max(e.Size, counter.Load())
		line := "removed " + tui.Tildify(e.WT.Path)
		if r.BranchDeleted {
			line += " and branch " + e.WT.Branch
		} else if r.BranchNote != "" {
			line += " (" + r.BranchNote + ")"
		}
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "removed %d worktree(s), freed %s\n", removed, tui.HumanBytes(freed))
	if failed {
		return errSilent
	}
	return nil
}
