// Package fsutil measures and deletes directory trees with progress reporting.
package fsutil

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Stats summarises a directory tree.
type Stats struct {
	Size   int64     // disk usage, counting hard-linked files once
	Newest time.Time // most recent modification time of any file
}

// Measure walks the tree rooted at root and returns its Stats. The
// worktree's ".git" file is ignored for Newest since git touches it.
func Measure(ctx context.Context, root string) Stats {
	var st Stats
	gitFile := filepath.Join(root, ".git")
	seen := map[fileID]bool{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size, id, hardlinked := usage(info)
		if hardlinked && !d.IsDir() {
			if seen[id] {
				return nil
			}
			seen[id] = true
		}
		st.Size += size
		if !d.IsDir() && path != gitFile && info.ModTime().After(st.Newest) {
			st.Newest = info.ModTime()
		}
		return nil
	})
	return st
}

// IsLinkedWorktree reports whether dir has a ".git" file (not a directory),
// which is what distinguishes a linked worktree from a main checkout.
func IsLinkedWorktree(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && info.Mode().IsRegular()
}

// RemoveContents deletes everything inside dir except its top-level ".git"
// file, adding the disk usage of each removed entry to freed as it goes.
// Errors are ignored: whatever is left is cleaned up by `git worktree remove`.
func RemoveContents(dir string, freed *atomic.Int64) {
	keep := filepath.Join(dir, ".git")
	type entry struct {
		path string
		size int64
	}
	var dirs []entry
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == keep {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size, _, _ := usage(info)
		if d.IsDir() {
			// Read-only directories (e.g. Go module caches) can't be emptied.
			if perm := info.Mode().Perm(); perm&0o700 != 0o700 {
				_ = os.Chmod(path, perm|0o700)
			}
			if path != dir {
				dirs = append(dirs, entry{path, size})
			}
			return nil
		}
		if os.Remove(path) == nil {
			freed.Add(size)
		}
		return nil
	})
	// Directories are removed deepest first, once they're empty.
	for i := len(dirs) - 1; i >= 0; i-- {
		if os.Remove(dirs[i].path) == nil {
			freed.Add(dirs[i].size)
		}
	}
}
