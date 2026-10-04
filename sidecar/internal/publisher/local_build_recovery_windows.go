//go:build windows

package publisher

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// No FILE_SHARE_DELETE: a directory cannot turn into a junction while recovery
// removes its children. OPEN_REPARSE_POINT prevents resolving a raced link.
func lockRecoveryDirectory(path string) (func(), error) {
	before, err := os.Lstat(path)
	if err != nil || isPathLink(before) || !before.IsDir() {
		return nil, errors.New("恢复目录无效或经过链接")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.New("无法锁定恢复目录，已保留目录")
	}
	file := os.NewFile(uintptr(handle), path)
	opened, statErr := file.Stat()
	latest, latestErr := os.Lstat(path)
	if statErr != nil || latestErr != nil || isPathLink(opened) || isPathLink(latest) || !opened.IsDir() || !os.SameFile(before, opened) || !os.SameFile(latest, opened) {
		file.Close()
		return nil, errors.New("恢复目录在打开期间已被替换")
	}
	return func() { _ = file.Close() }, nil
}
