//go:build !windows

package proc

import "os/exec"

// configureHidden 非 Windows：标准进程，无窗口隐藏需求。
func configureHidden(cmd *exec.Cmd) {}

// assignJob 非 Windows：无 Job Object，返回 nil closer。
func assignJob(pid int) func() error { return nil }

// sendCtrlBreak 非 Windows：向进程发送 SIGINT。
func sendCtrlBreak(h *Handle) error {
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Signal(interruptSignal())
}

// terminateTree 非 Windows：kill 根进程。
func terminateTree(h *Handle) error {
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}
