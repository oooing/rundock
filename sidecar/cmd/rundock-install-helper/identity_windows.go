package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
)

// Short-lived OS lock serializes helper operations, including stale recovery.
// The handle (and lock file) is released on a helper crash; the journal survives.
func lockDirectory(root string) (func(), error) {
	path := filepath.Join(root, ".rundock-install.lock")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("installer lock is not a regular file")
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
	if err != nil {
		return nil, fmt.Errorf("another installer operation or inaccessible directory: %w", err)
	}
	return func() { windows.CloseHandle(h) }, nil
}

func processIdentity(pid uint32) (uint64, bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("cannot verify installer process: %w", err)
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, false, err
	}
	state, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return 0, false, err
	}
	if state != windows.WAIT_OBJECT_0 && state != uint32(windows.WAIT_TIMEOUT) {
		return 0, false, errors.New("unknown installer process state")
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), state == uint32(windows.WAIT_TIMEOUT), nil
}

func identifyOwner(owner string) (*recovery, error) {
	pid, err := strconv.ParseUint(owner, 10, 32)
	if err != nil {
		// MSI passes ProductCode rather than PID. Bind a new transaction to its
		// immediate launcher too, but never auto-adopt an opaque owner's journal.
		pid = uint64(os.Getppid())
	}
	if pid == 0 {
		return nil, errors.New("invalid installer process")
	}
	created, alive, err := processIdentity(uint32(pid))
	if err != nil {
		return nil, err
	}
	if !alive || created == 0 {
		return nil, errors.New("installer process has already exited")
	}
	return &recovery{Version: 1, Owner: owner, PID: uint32(pid), Created: created}, nil
}

func ownerAlive(data *recovery) (bool, error) {
	created, alive, err := processIdentity(data.PID)
	return alive && created == data.Created, err
}
