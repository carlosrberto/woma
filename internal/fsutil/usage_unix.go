//go:build unix

package fsutil

import (
	"io/fs"
	"syscall"
)

type fileID struct{ dev, ino uint64 }

// usage returns the allocated size of a file (what deleting it frees), its
// identity and whether it has other hard links.
func usage(info fs.FileInfo) (int64, fileID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size(), fileID{}, false
	}
	return int64(st.Blocks) * 512, fileID{uint64(st.Dev), uint64(st.Ino)}, st.Nlink > 1
}
