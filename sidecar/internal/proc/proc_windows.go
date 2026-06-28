//go:build windows

package proc

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configureHidden 在 Windows 上：隐藏控制台窗口 + CREATE_NO_WINDOW。
// 关键：不用 DETACHED_PROCESS（detached 会让子进程新开一个控制台窗口，违背"无窗口"目标）。
func configureHidden(cmd *exec.Cmd) {
	// CREATE_NO_WINDOW = 0x08000000：不创建控制台窗口
	// CREATE_NEW_PROCESS_GROUP = 0x00000200：使进程成为新进程组首，
	//   这样后续可用 GenerateConsoleCtrlEvent 向它发 CTRL_BREAK_EVENT（优雅停止需要）。
	const (
		CREATE_NO_WINDOW           = 0x08000000
		CREATE_NEW_PROCESS_GROUP   = 0x00000200
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:        true,
		CreationFlags:     CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP,
		NoInheritHandles:  false,
	}
}

// assignJob 为指定 PID 创建一个 Job Object（KILL_ON_JOB_CLOSE），并把进程加入其中。
// 返回 closer：调用时关闭 Job Object 句柄；当 sidecar 进程退出时，所有 Job 内进程被自动终止。
// 参考：golang.org/x/sys/windows 的 CreateJobObject / AssignProcessToJobObject，
// 以及 GitLab Runner 的 job_windows.go 实现。
func assignJob(pid int) func() error {
	jobHandle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil
	}

	// 设置扩展限制：JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | JOB_OBJECT_LIMIT_BREAKAWAY_OK
	// KILL_ON_JOB_CLOSE：最后一个句柄关闭时杀掉所有进程
	// BREAKAWAY_OK：允许子进程脱离（兼容部分框架），但仍受 Job 约束
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK,
		},
	}
	if _, err := windows.SetInformationJobObject(
		jobHandle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(jobHandle)
		return nil
	}

	// 打开目标进程句柄并加入 Job
	hProc, err := windows.OpenProcess(windows.PROCESS_ALL_ACCESS, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(jobHandle)
		return nil
	}
	defer windows.CloseHandle(hProc)

	if err := windows.AssignProcessToJobObject(jobHandle, hProc); err != nil {
		// Windows 7 之前嵌套 Job 会失败；现代 Windows 支持，失败时仅记录不阻断启动
		windows.CloseHandle(jobHandle)
		return nil
	}

	return func() error {
		return windows.CloseHandle(jobHandle)
	}
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

// 消除未使用导入警告（保留 unsafe/windows 供 Job Object 使用）
var _ = unsafe.Sizeof(0)
