//go:build windows

package probe

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// snapshotListenersOS 解析 `netstat -ano` 输出，提取 LISTENING 状态的 TCP 端口。
// 不需要管理员权限；比 GetExtendedTcpTable 的实现路径简单可靠。
// 报告指出 netstat -b（显示可执行文件）会慢且需权限，故这里只用 -ano。
func snapshotListenersOS() []PortListener {
	cmd := exec.Command("netstat", "-ano")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	return parseNetstat(string(out))
}

func parseNetstat(text string) []PortListener {
	var out []PortListener
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "TCP") && !strings.HasPrefix(line, "TCPv6") {
			continue
		}
		fields := strings.Fields(line)
		// TCP  <local> <foreign> <state> [<pid>]
		if len(fields) < 5 {
			continue
		}
		state := fields[3]
		if state != "LISTENING" {
			continue
		}
		local := fields[1]
		port := portFromAddr(local)
		if port <= 0 {
			continue
		}
		pid, _ := strconv.Atoi(fields[4])
		out = append(out, PortListener{Port: port, Addr: local, PID: pid})
	}
	return out
}

// portFromAddr 从 "0.0.0.0:3000" / "[::]:3000" / "127.0.0.1:3000" 抽端口。
func portFromAddr(addr string) int {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return 0
	}
	p, err := strconv.Atoi(addr[idx+1:])
	if err != nil {
		return 0
	}
	return p
}
