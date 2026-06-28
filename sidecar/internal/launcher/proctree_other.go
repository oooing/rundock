//go:build !windows

package launcher

// collectProcessTree 非 Windows：用 ps 拿 PPID 构建（实现略，退化为只返回 root）。
func collectProcessTree(rootPID int) []int {
	return []int{rootPID}
}
