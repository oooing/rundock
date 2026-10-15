//go:build windows

package publisher

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockScratchFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

func pinScratchDirectory(path string) (func(), error) { return lockRecoveryDirectory(path) }
