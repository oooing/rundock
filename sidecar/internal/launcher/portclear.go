package launcher

import (
	"fmt"
	"log"
	"os/exec"
	"syscall"

	"github.com/launcher-sidecar/internal/probe"
)

// clearPortsIfOccupied 启动前清理脚本声明的端口。
// 这是解决"项目没被正确关闭导致端口占用"痛点的核心：
// 启动前检查 portHints 里的端口是否被占用，若是则杀掉占用进程。
//
// 安全策略：只清脚本声明的端口（portHints），不乱杀；
// 杀之前记录日志，方便审计。
func clearPortsIfOccupied(ports []int) []string {
	if len(ports) == 0 {
		return nil
	}
	listeners := probe.SnapshotListeners()
	occupied := map[int]int{} // port -> pid
	for _, l := range listeners {
		for _, p := range ports {
			if l.Port == p {
				occupied[p] = l.PID
			}
		}
	}
	var cleared []string
	for port, pid := range occupied {
		if pid <= 0 {
			continue
		}
		// 杀掉占用端口的进程（及其子进程 /T）
		kill := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if err := kill.Run(); err != nil {
			log.Printf("[portclear] 杀进程 %d (端口 %d) 失败: %v", pid, port, err)
			continue
		}
		msg := fmt.Sprintf("端口 %d 被进程 %d 占用，已自动清理", port, pid)
		log.Printf("[portclear] %s", msg)
		cleared = append(cleared, msg)
	}
	return cleared
}
