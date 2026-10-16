//go:build windows

package launcher

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

// A parent PID alone is not ancestry: Windows keeps it after the parent exits
// and can reuse that PID for a different project hours later.
func collectProcessIdentities(rootPID int) map[int]uint64 {
	result := map[int]uint64{}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return result
	}
	defer windows.CloseHandle(snapshot)
	parents, created := map[int]int{}, map[int]uint64{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		pid := int(entry.ProcessID)
		parents[pid] = int(entry.ParentProcessID)
		if stamp := processCreation(pid); stamp != 0 {
			created[pid] = stamp
		}
	}
	return descendantsByCreation(rootPID, parents, created)
}
func collectProcessTree(rootPID int) []int {
	identities := collectProcessIdentities(rootPID)
	result := make([]int, 0, len(identities))
	for pid := range identities {
		result = append(result, pid)
	}
	return result
}
func processCreation(pid int) uint64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if windows.GetProcessTimes(h, &c, &e, &k, &u) != nil || e.HighDateTime != 0 || e.LowDateTime != 0 {
		return 0
	}
	return uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime)
}
func processIdentityGone(pid int, created uint64) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_INVALID_PARAMETER
	}
	defer windows.CloseHandle(h)
	if state, _ := windows.WaitForSingleObject(h, 0); state == windows.WAIT_OBJECT_0 {
		return true
	}
	var c, e, k, u windows.Filetime
	if windows.GetProcessTimes(h, &c, &e, &k, &u) != nil {
		return false
	}
	return created != 0 && uint64(c.HighDateTime)<<32|uint64(c.LowDateTime) != created
}
