package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// walkers is how many folders are read at once. Scanning is bound by file
// system latency, so this is higher than the CPU count.
const walkers = 16

type walkJob struct {
	dir   string
	depth int
}

// walkQueue is a FIFO of folders to read, so folders near the roots (where
// repositories usually are) are read first. pending counts queued and
// in-progress folders; the walk ends when it reaches zero.
type walkQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	jobs    []walkJob
	pending int
}

func (q *walkQueue) push(j walkJob) {
	q.mu.Lock()
	q.jobs = append(q.jobs, j)
	q.pending++
	q.mu.Unlock()
	q.cond.Signal()
}

func (q *walkQueue) pop() (walkJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.jobs) == 0 && q.pending > 0 {
		q.cond.Wait()
	}
	if len(q.jobs) == 0 {
		return walkJob{}, false
	}
	j := q.jobs[0]
	q.jobs = q.jobs[1:]
	return j, true
}

func (q *walkQueue) done() {
	q.mu.Lock()
	q.pending--
	finished := q.pending == 0
	q.mu.Unlock()
	if finished {
		q.cond.Broadcast()
	}
}

// walk reads every folder under the roots in parallel, breadth first,
// calling add (concurrently) with each git dir it finds: ".git" folders,
// bare repositories and the targets of ".git" files.
func walk(ctx context.Context, roots []string, opts ScanOptions, p *ScanProgress, add func(string)) error {
	for _, root := range roots {
		if _, err := os.ReadDir(root); err != nil {
			return err
		}
	}
	q := &walkQueue{}
	q.cond = sync.NewCond(&q.mu)
	for _, root := range roots {
		q.push(walkJob{root, 0})
	}

	var wg sync.WaitGroup
	for range walkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, ok := q.pop()
				if !ok {
					return
				}
				if ctx.Err() == nil {
					visit(j, opts, p, add, q.push)
				}
				q.done()
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// visit reads one folder, reporting git dirs and queueing subfolders.
func visit(j walkJob, opts ScanOptions, p *ScanProgress, add func(string), push func(walkJob)) {
	p.Dirs.Add(1)
	p.current.Store(&j.dir)
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return // unreadable: skip it
	}
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(j.dir, name)
		if name == ".git" {
			switch {
			case e.IsDir():
				add(path)
			case e.Type().IsRegular():
				// A ".git" file marks a linked worktree (or a submodule).
				if dir, ok := commonDirFromGitFile(path); ok {
					add(dir)
				}
			}
			continue
		}
		if !e.IsDir() || opts.skip(name) {
			continue
		}
		if (strings.HasSuffix(name, ".git") || name == ".bare") && isBareRepo(path) {
			add(path)
			continue
		}
		if opts.MaxDepth > 0 && j.depth+1 >= opts.MaxDepth {
			// Count it and look for a ".git" here, but go no deeper.
			p.Dirs.Add(1)
			checkGitEntry(filepath.Join(path, ".git"), add)
			continue
		}
		push(walkJob{path, j.depth + 1})
	}
}
