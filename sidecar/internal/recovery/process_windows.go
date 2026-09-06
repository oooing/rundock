//go:build windows

package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func commandArgs(line string) []string { args, _ := windows.DecomposeCommandLine(line); return args }

func Snapshot() ([]Process, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// Constant script; no user paths or log text are interpolated into PowerShell.
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new(); $items=@(Get-CimInstance Win32_Process | ForEach-Object { @{pid=[int]$_.ProcessId;parentPid=[int]$_.ParentProcessId;created=if($_.CreationDate){$_.CreationDate.ToFileTimeUtc().ToString()}else{''};executable=[string]$_.ExecutablePath;commandLine=[string]$_.CommandLine} }); ConvertTo-Json -InputObject $items -Compress`)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("无法读取占用进程，请查看日志或手动处理")
	}
	var result []Process
	err = json.Unmarshal(out, &result)
	// WMI timestamps lose sub-microsecond precision. Use the same native clock
	// as Terminate, and refuse recovery if the exact identity cannot be read.
	for i := range result {
		wmiCreated, _ := strconv.ParseUint(result[i].Created, 10, 64)
		result[i].Created = ""
		h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(result[i].PID))
		if openErr != nil {
			continue
		}
		var created, exit, kernel, user windows.Filetime
		if windows.GetProcessTimes(h, &created, &exit, &kernel, &user) == nil {
			actual := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
			// Match WMI identity at its microsecond precision too. A PID reused
			// between the WMI query and OpenProcess must not inherit the old command line.
			if actual/10 == wmiCreated/10 {
				result[i].Created = strconv.FormatUint(actual, 10)
			}
		}
		windows.CloseHandle(h)
	}
	return result, err
}

// Open a handle and verify creation time before terminating: PID reuse cannot hit a new process.
func Terminate(p Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(p.PID))
	if err != nil {
		return fmt.Errorf("无法结束占用进程（PID %d），请检查权限", p.PID)
	}
	defer windows.CloseHandle(h)
	var created, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err != nil {
		return err
	}
	expected, _ := strconv.ParseUint(p.Created, 10, 64)
	actual := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	if actual != expected {
		return fmt.Errorf("占用进程已变化，请重新检查")
	}
	if err = windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	_, err = windows.WaitForSingleObject(h, 3000)
	return err
}
