//go:build !windows

package publisher

import (
	"errors"
	"os"
	"syscall"
)

func lockScratchFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// Unix unlink does not follow symlinks. Validate/open the directory itself;
// the Windows implementation additionally denies replacement via sharing flags.
func pinScratchDirectory(path string) (func(), error) {
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || isPathLink(before) {
		return nil, errors.New("invalid scratch directory")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		f.Close()
		return nil, errors.New("scratch directory replaced")
	}
	return func() { f.Close() }, nil
}
