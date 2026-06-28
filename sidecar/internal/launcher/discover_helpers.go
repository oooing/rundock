package launcher

import (
	"sort"
	"strconv"
	"strings"
)

// portFromURLStr 从 URL 字符串提取端口，如 http://localhost:8765/ -> 8765。
func portFromURLStr(u string) int {
	// 去 scheme
	idx := strings.Index(u, "://")
	if idx >= 0 {
		u = u[idx+3:]
	}
	// 去 path
	if slash := strings.Index(u, "/"); slash >= 0 {
		u = u[:slash]
	}
	// 取端口
	ci := strings.LastIndex(u, ":")
	if ci < 0 {
		return 0
	}
	p, err := strconv.Atoi(u[ci+1:])
	if err != nil {
		return 0
	}
	return p
}

// keysOfInt 返回 map 的 key 切片（排序，便于日志可读）。
func keysOfInt(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
