//go:build !unix

package fsutil

import "io/fs"

type fileID struct{}

func usage(info fs.FileInfo) (int64, fileID, bool) {
	return info.Size(), fileID{}, false
}
