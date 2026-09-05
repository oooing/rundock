//go:build windows

package publisher

import (
	"io/fs"
	"syscall"
)

func isPathLink(info fs.FileInfo) bool {
	if info.Mode()&fs.ModeSymlink != 0 {
		return true
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
