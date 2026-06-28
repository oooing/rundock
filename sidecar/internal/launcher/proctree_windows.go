//go:build windows

package launcher

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// collectProcessTree 收集 root PID 及其所有子孙 PID（递归，BFS）。
// Windows：用 PowerShell Get-CimInstance 拿 ParentProcessId（wmic 在 Win11 已弃用）。
// 不需要管理员权限。
func collectProcessTree(rootPID int) []int {
	if rootPID <= 0 {
		return nil
	}
	// 用 PowerShell 拿全部进程的 PID,ParentProcessId（CSV）
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId | ConvertTo-Csv -NoTypeInformation")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return []int{rootPID}
	}
	parentOf := parseCSV(string(out))
	if len(parentOf) == 0 {
		return []int{rootPID}
	}

	// BFS 收集 root 及其子孙
	seen := map[int]bool{rootPID: true}
	queue := []int{rootPID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for pid, ppid := range parentOf {
			if ppid == cur && !seen[pid] {
				seen[pid] = true
				queue = append(queue, pid)
			}
		}
	}
	out2 := make([]int, 0, len(seen))
	for pid := range seen {
		out2 = append(out2, pid)
	}
	return out2
}

// parseCSV 解析 PowerShell ConvertTo-Csv 输出，返回 pid->parentPid 映射。
// 输出形如："ProcessId","ParentProcessId"\n"5678","1234"
func parseCSV(text string) map[int]int {
	m := map[int]int{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "ProcessId") {
			continue
		}
		// 去引号后按逗号分割
		clean := strings.ReplaceAll(line, `"`, "")
		fields := strings.Split(clean, ",")
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(strings.TrimSpace(fields[0]))
		ppid, err2 := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err1 == nil && err2 == nil && pid > 0 {
			m[pid] = ppid
		}
	}
	return m
}
