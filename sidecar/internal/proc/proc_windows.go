//go:build windows

package proc

import (
	"fmt"
	"os/exec"
	"syscall"
)

// configureHidden 在 Windows 上：隐藏控制台窗口 + CREATE_NO_WINDOW。
// 关键：不用 DETACHED_PROCESS（detached 会让子进程新开一个控制台窗口，违背"无窗口"目标）。
func configureHidden(cmd *exec.Cmd) {
	// CREATE_NO_WINDOW = 0x08000000：不创建控制台窗口
	// CREATE_NEW_PROCESS_GROUP = 0x00000200：使进程成为新进程组首，
	//   这样后续可用 GenerateConsoleCtrlEvent 向它发 CTRL_BREAK_EVENT（优雅停止需要）。
	const (
		CREATE_NO_WINDOW         = 0x08000000
		CREATE_NEW_PROCESS_GROUP = 0x00000200
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:       true,
		CreationFlags:    CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP,
		NoInheritHandles: false,
	}
}

// assignJob remains for legacy low-level ConPTY tests.
func assignJob(pid int) func() error {
	group, err := newProcessGroup(pid)
	if err != nil {
		return nil
	}
	return group.Close
}

// sendCtrlBreak 向进程组发送 CTRL_BREAK_EVENT，触发优雅停止（仅对同控制台进程组有效）。
// 这对 node/python 等监听信号的服务最友好，能让它们走清理流程。
func sendCtrlBreak(h *Handle) error {
	if h.rootPID == 0 {
		return nil
	}
	// GenerateConsoleCtrlEvent 需要进程组 ID（= 进程组首 PID）。
	// 启动时设了 CREATE_NEW_PROCESS_GROUP，故 rootPID 即组 ID。
	dll := syscall.NewLazyDLL("kernel32.dll")
	proc := dll.NewProc("GenerateConsoleCtrlEvent")
	const CTRL_BREAK_EVENT = 1
	r1, _, err := proc.Call(uintptr(CTRL_BREAK_EVENT), uintptr(h.rootPID))
	if r1 == 0 {
		return fmt.Errorf("GenerateConsoleCtrlEvent: %w", err)
	}
	return nil
}

// terminateTree 强制终止整棵进程树。
// 策略：依赖 Job Object 的 KILL_ON_JOB_CLOSE 不适合主动终止（需关闭句柄），这里直接用
// taskkill /pid <root> /T /F：/T 连同子进程，/F 强制。简单可靠，是 Job Object 的兜底。
func terminateTree(h *Handle) error {
	if h.rootPID == 0 {
		return nil
	}
	kill := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", h.rootPID), "/T", "/F")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := kill.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill failed: %w; output: %s", err, string(out))
	}
	return nil
}
