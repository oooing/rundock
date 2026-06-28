//go:build windows

package launcher

import (
	"os"

	"golang.org/x/sys/windows"
)

// processAlive Windows：用 OpenProcess(SYNCHRONIZE) 判断进程是否还在。
// 进程已退出时 OpenProcess 仍可能成功（直到句柄全关），故进一步用
// WaitForSingleObject(0) 看是否已 signaled（已退出）。
func processAlive(_ *os.Process, pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false // 打不开 = 已退出或无权限，按"已退出"处理
	}
	defer windows.CloseHandle(h)
	// WaitForSingleObject(handle, 0)：返回 WAIT_OBJECT_0(0)=已 signaled(退出)，WAIT_TIMEOUT(258)=仍在运行
	const WAIT_OBJECT_0 = 0
	const WAIT_TIMEOUT = 258
	r, _ := windows.WaitForSingleObject(h, 0)
	return r == WAIT_TIMEOUT || (r != WAIT_OBJECT_0)
}
