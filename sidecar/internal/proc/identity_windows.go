//go:build windows

package proc

import (
	"golang.org/x/sys/windows"
	"strconv"
)

// Called immediately after spawn, before the run enters the public registry.
func (h *Handle) IdentityCreated() string {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(h.rootPID))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(p)
	var created, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(p, &created, &exit, &kernel, &user) != nil || exit.HighDateTime != 0 || exit.LowDateTime != 0 {
		return ""
	}
	return strconv.FormatUint(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime), 10)
}
