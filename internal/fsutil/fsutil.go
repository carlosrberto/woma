// Package fsutil measures and deletes directory trees with progress reporting.
package fsutil

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Stats summarises a directory tree.
type Stats struct {
	// Size is the disk space deleting the tree would free. A hard-linked
	// file counts only when all of its links are inside the tree (e.g. files
	// linked from a package manager's global store count as nothing).
	Size   int64
	Newest time.Time // most recent modification time of any file
}

// Measure walks the tree rooted at root and returns its Stats. The
// worktree's ".git" file is ignored for Newest since git touches it.
func Measure(ctx context.Context, root string) Stats {
	var st Stats
	gitFile := filepath.Join(root, ".git")
	links := map[fileID]uint64{}
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
		size, id, nlink := usage(info)
		if !d.IsDir() && path != gitFile && info.ModTime().After(st.Newest) {
			st.Newest = info.ModTime()
		}
		if nlink > 1 && !d.IsDir() {
			// Count the file once all of its links have been seen.
			links[id]++
			if links[id] != nlink {
				return nil
			}
		}
		st.Size += size
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

// RemoveTree deletes dir and everything in it, adding the disk space freed
// by each removal to freed as it goes. Read-only directories are made
// writable first. It fails if anything is left behind.
func RemoveTree(dir string, freed *atomic.Int64) error {
	type entry struct {
		path string
		size int64
	}
	var dirs []entry
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size, _, nlink := usage(info)
		if d.IsDir() {
			// Read-only directories (e.g. Go module caches) can't be emptied.
			if perm := info.Mode().Perm(); perm&0o700 != 0o700 {
				_ = os.Chmod(path, perm|0o700)
			}
			dirs = append(dirs, entry{path, size})
			return nil
		}
		// Space comes back only when the last link to a file goes.
		if os.Remove(path) == nil && nlink <= 1 {
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
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("could not delete everything in %s", dir)
	}
	return nil
}
