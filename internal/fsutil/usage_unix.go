//go:build unix

package fsutil

import (
	"io/fs"
	"syscall"
)

type fileID struct{ dev, ino uint64 }

// usage returns the allocated size of a file, its identity and its number
// of hard links.
func usage(info fs.FileInfo) (int64, fileID, uint64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size(), fileID{}, 1
	}
	return int64(st.Blocks) * 512, fileID{uint64(st.Dev), uint64(st.Ino)}, uint64(st.Nlink)
}
