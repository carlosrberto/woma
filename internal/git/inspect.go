package git

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// maxChanges caps how many changed files Inspect keeps for display.
const maxChanges = 20

// Status describes the state of a worktree's checkout and branch.
type Status struct {
	Dirty      bool
	Changes    []string // `git status --porcelain` lines, at most maxChanges
	NumChanges int

	Upstream string // e.g. "origin/feature-x"; empty when not tracking
	Gone     bool   // the upstream branch was deleted on the remote
	Ahead    int    // commits not on the upstream
	Behind   int    // upstream commits not in the branch

	// Merged means the branch has commits and they are all in the default
	// branch.
	Merged bool
	// NoCommits means the branch was created but never committed to, so it
	// still points into the default branch's history.
	NoCommits bool
	// Unpushed counts commits that exist only locally: for a branch, commits
	// not on any remote nor the default branch; for a detached HEAD, commits
	// not on any branch or remote (lost if the worktree is removed).
	Unpushed int

	LastCommit time.Time
	Subject    string

	// Complete is set when every check that decides whether removing the
	// worktree or its branch loses work succeeded. When false, callers must
	// treat the worktree as unsafe.
	Complete bool
}

// Inspect gathers the Status of a worktree. Errors from individual git
// commands are ignored and leave the corresponding fields empty.
func Inspect(ctx context.Context, repo *Repo, wt Worktree) Status {
	var s Status
	dir := repo.CommonDir
	ok := true

	if !wt.Prunable {
		out, err := output(ctx, wt.Path, "status", "--porcelain")
		ok = err == nil
		if ok {
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				if line == "" {
					continue
				}
				s.NumChanges++
				if len(s.Changes) < maxChanges {
					s.Changes = append(s.Changes, line)
				}
			}
			s.Dirty = s.NumChanges > 0
		}
	}

	if wt.Head == "" {
		return s
	}
	if out, err := output(ctx, dir, "log", "-1", "--format=%ct%x00%s", wt.Head); err == nil {
		ts, subject, _ := strings.Cut(strings.TrimSpace(out), "\x00")
		if sec, err := strconv.ParseInt(ts, 10, 64); err == nil {
			s.LastCommit = time.Unix(sec, 0)
		}
		s.Subject = subject
	}

	if wt.Branch == "" {
		n, err := countCommits(ctx, dir, wt.Head, "--branches", "--remotes")
		s.Unpushed, s.Complete = n, ok && err == nil
		return s
	}

	if out, err := output(ctx, dir, "for-each-ref", "--format=%(upstream:short)%00%(upstream:track)", "refs/heads/"+wt.Branch); err == nil {
		upstream, track, _ := strings.Cut(strings.TrimSpace(out), "\x00")
		s.Upstream = upstream
		s.Gone, s.Ahead, s.Behind = parseTrack(track)
	}

	if wt.Branch != repo.BaseBranch {
		for _, base := range repo.BaseRefs {
			if _, err := output(ctx, dir, "merge-base", "--is-ancestor", wt.Head, base); err == nil {
				s.Merged = true
				break
			}
		}
		// A fresh branch is an ancestor of the default branch too; its
		// reflog tells it apart.
		if s.Merged && neverCommitted(ctx, dir, wt.Branch, s.Upstream) {
			s.Merged, s.NoCommits = false, true
		}
	}
	if !s.Merged {
		n, err := countCommits(ctx, dir, wt.Head, append([]string{"--remotes"}, repo.BaseRefs...)...)
		s.Unpushed, ok = n, ok && err == nil
	}
	s.Complete = ok
	return s
}

// neverCommitted reports whether the branch was created but never moved by
// work of its own, judging by its reflog: every entry is either its
// creation from some starting point (HEAD, main, origin/main, a tag, …) or
// a rename. Creating it from a remote branch of the same name (checking out
// someone's pushed branch) brings that branch's commits along, so it doesn't
// count. Without a reflog (e.g. disabled or expired) it returns false.
func neverCommitted(ctx context.Context, dir, branch, upstream string) bool {
	out, err := output(ctx, dir, "reflog", "show", "--format=%gs", "refs/heads/"+branch, "--")
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(strings.ToLower(line), "branch: renamed ") {
			continue
		}
		source, ok := strings.CutPrefix(line, "branch: Created from ")
		if !ok || sameBranchOnRemote(source, branch, upstream) {
			return false
		}
	}
	return true
}

// sameBranchOnRemote reports whether ref (as written in a reflog) is a
// remote-tracking copy of branch, e.g. "origin/feat-x" or
// "refs/remotes/origin/feat-x" for "feat-x".
func sameBranchOnRemote(ref, branch, upstream string) bool {
	ref = strings.TrimPrefix(ref, "refs/remotes/")
	if upstream != "" && ref == upstream && strings.HasSuffix(upstream, "/"+branch) {
		return true
	}
	_, name, ok := strings.Cut(ref, "/")
	return ok && name == branch
}

// countCommits counts commits reachable from head but not from exclude.
func countCommits(ctx context.Context, dir, head string, exclude ...string) (int, error) {
	args := append([]string{"rev-list", "--count", head, "--not"}, exclude...)
	out, err := output(ctx, dir, args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// parseTrack parses %(upstream:track): "[gone]", "[ahead 1]",
// "[behind 2]", "[ahead 1, behind 2]" or "".
func parseTrack(track string) (gone bool, ahead, behind int) {
	track = strings.Trim(track, "[]")
	if track == "gone" {
		return true, 0, 0
	}
	for _, part := range strings.Split(track, ", ") {
		kind, num, _ := strings.Cut(part, " ")
		n, _ := strconv.Atoi(num)
		switch kind {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		}
	}
	return false, ahead, behind
}
