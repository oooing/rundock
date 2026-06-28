//go:build !windows

package probe

import "strings"

// snapshotListenersOS 非 Windows 兜底：解析 netstat -tlnp（若有）。
func snapshotListenersOS() []PortListener {
	out, err := execCombinedOutput("netstat", "-tlnp")
	if err != nil {
		return nil
	}
	var list []PortListener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		if strings.HasPrefix(f[0], "tcp") && strings.Contains(f[len(f)-1], "LISTEN") {
			port := portFromAddr(f[3])
			if port > 0 {
				list = append(list, PortListener{Port: port, Addr: f[3]})
			}
		}
	}
	return list
}

func portFromAddr(addr string) int {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return 0
	}
	p := 0
	for i := idx + 1; i < len(addr); i++ {
		ch := addr[i]
		if ch < '0' || ch > '9' {
			break
		}
		p = p*10 + int(ch-'0')
	}
	return p
}
