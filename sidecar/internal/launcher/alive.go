package launcher

import "os"

// isProcessGone 通过尝试给进程发 signal 0（null signal）判断其是否存活。
// 通用实现：os.FindProcess + Signal(0)。Windows 上 Signal 不完全支持，
// 单独的 alive_windows.go 提供更准的 OpenProcess 判断。
func isProcessGone(pid int) bool {
	if pid <= 0 {
		return true
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	// sigzero 跨平台：非 Windows 用 Signal(0)；Windows 见 alive_windows.go 覆盖。
	return !processAlive(p, pid)
}
