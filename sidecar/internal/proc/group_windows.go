//go:build windows

package proc

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessGroup struct {
	mu  sync.Mutex
	job windows.Handle
}

func newProcessGroup(pid int) (processGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			windows.CloseHandle(job)
		}
	}()
	// No BREAKAWAY_OK: untracked descendants cannot be safely kept or stopped.
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE}}
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return nil, err
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(p)
	if err = windows.AssignProcessToJobObject(job, p); err != nil {
		return nil, err
	}
	ok = true
	return &windowsProcessGroup{job: job}, nil
}

// Caller holds mu. Query the dedicated job, never a parent PID or all processes.
func (g *windowsProcessGroup) pids() ([]int, error) {
	if g.job == 0 {
		return nil, nil
	}
	for capacity := 32; capacity <= 65536; capacity *= 2 {
		buf := make([]byte, 8+capacity*int(unsafe.Sizeof(uintptr(0))))
		err := windows.QueryInformationJobObject(g.job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil)
		if err == windows.ERROR_MORE_DATA {
			continue
		}
		if err != nil {
			return nil, err
		}
		count := *(*uint32)(unsafe.Pointer(&buf[4]))
		if int(count) > capacity {
			continue
		}
		ids := unsafe.Slice((*uintptr)(unsafe.Pointer(&buf[8])), int(count))
		out := make([]int, 0, count)
		for _, id := range ids {
			out = append(out, int(id))
		}
		return out, nil
	}
	return nil, fmt.Errorf("project process group exceeds query limit")
}

func (g *windowsProcessGroup) Active() (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids, err := g.pids()
	return len(ids) > 0, err
}

var isProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

func (g *windowsProcessGroup) Members() (map[int]uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids, err := g.pids()
	if err != nil {
		return nil, err
	}
	members := map[int]uint64{}
	for _, id := range ids {
		p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(id))
		if err != nil {
			continue
		} // may have exited since QueryInformationJobObject
		var member int32
		ok, _, _ := isProcessInJob.Call(uintptr(p), uintptr(g.job), uintptr(unsafe.Pointer(&member)))
		var born, exit, kernel, user windows.Filetime
		err = windows.GetProcessTimes(p, &born, &exit, &kernel, &user)
		windows.CloseHandle(p)
		if ok != 0 && member != 0 && err == nil && exit.HighDateTime == 0 && exit.LowDateTime == 0 {
			members[id] = uint64(born.HighDateTime)<<32 | uint64(born.LowDateTime)
		}
	}
	return members, nil
}

func (g *windowsProcessGroup) Terminate() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(g.job, 1)
}

func (g *windowsProcessGroup) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job == 0 {
		return nil
	}
	err := windows.CloseHandle(g.job)
	if err == nil {
		g.job = 0
	}
	return err
}
