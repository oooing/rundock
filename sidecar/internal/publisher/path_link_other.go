//go:build !windows

package publisher

import "io/fs"

func isPathLink(info fs.FileInfo) bool {
	return info.Mode()&fs.ModeSymlink != 0
}
