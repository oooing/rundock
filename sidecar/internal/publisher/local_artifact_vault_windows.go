//go:build windows

package publisher

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Holding a read-only, no-write/no-delete-sharing handle prevents replacement
// or mutation between hashing and streaming on the supported desktop platform.
func openLocalArtifactReadOnly(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(localArtifactWindowsPath(path))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

// Direct Win32 file reads do not inherit os.Open's long-path conversion.
func localArtifactWindowsPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	if filepath.IsAbs(path) {
		return `\\?\` + path
	}
	return path
}

func lockLocalArtifactDirectory(path string) (func(), error) {
	return lockRecoveryDirectory(localArtifactWindowsPath(path))
}
