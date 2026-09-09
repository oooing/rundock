package launcher

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

// Optional entry-script comments: rundock:open/ready <local URL> and
// rundock:timeout <seconds>. Guessed port hints are never requirements.
type startupReadiness struct {
	openURL  string
	urls     map[int]string
	timeout  time.Duration
	deadline time.Time
	reached  bool
}

func readStartupReadiness(path string, timeout time.Duration) (*startupReadiness, error) {
	r := &startupReadiness{urls: map[int]string{}, timeout: timeout}
	if path == "" {
		return r, nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat", ".ps1":
	default:
		return r, nil // JSON, executables and other adapters retain existing behavior.
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scan := bufio.NewScanner(io.LimitReader(f, 1024*1024+1))
	scan.Buffer(make([]byte, 4096), 1024*1024+1)
	for scan.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scan.Text(), "\ufeff"))
		switch {
		case strings.HasPrefix(line, "#"):
			line = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "::"):
			line = strings.TrimSpace(line[2:])
		case strings.HasPrefix(strings.ToLower(line), "rem "):
			line = strings.TrimSpace(line[4:])
		default:
			continue
		}
		if !strings.HasPrefix(strings.ToLower(line), "rundock:") {
			continue
		}
		fields := strings.Fields(line)
		switch strings.ToLower(fields[0]) {
		case "rundock:ready", "rundock:open":
			if len(fields) != 2 {
				return nil, fmt.Errorf("%s 每行需要一个本机 HTTP 地址", fields[0])
			}
			u, err := url.Parse(fields[1])
			if err != nil {
				return nil, fmt.Errorf("无效就绪地址: %s", fields[1])
			}
			host := u.Hostname()
			ip := net.ParseIP(host)
			if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
				return nil, fmt.Errorf("就绪地址必须为本机 HTTP/HTTPS 地址: %s", fields[1])
			}
			port, err := strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("就绪地址需要明确端口: %s", fields[1])
			}
			if strings.EqualFold(fields[0], "rundock:open") {
				if r.openURL != "" {
					return nil, fmt.Errorf("主打开地址只能声明一次")
				}
				r.openURL = fields[1]
				continue
			}
			if _, ok := r.urls[port]; ok {
				return nil, fmt.Errorf("就绪端口 %d 重复声明", port)
			}
			r.urls[port] = fields[1]
		case "rundock:timeout":
			if len(fields) != 2 {
				return nil, fmt.Errorf("rundock:timeout 需要 1–600 秒")
			}
			secs, err := strconv.Atoi(fields[1])
			if err != nil || secs < 1 || secs > 600 {
				return nil, fmt.Errorf("rundock:timeout 需要 1–600 秒")
			}
			r.timeout = time.Duration(secs) * time.Second
		default:
			return nil, fmt.Errorf("未知启动声明: %s", fields[0])
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("读取启动声明: %w", err)
	}
	return r, nil
}

// Only services discovered for this run can satisfy requirements. A stranger
// already listening on the requested port must not make this project ready.
func (r *startupReadiness) pending(svcs []*store.AppService) []string {
	known := map[int]bool{}
	for _, svc := range svcs {
		if svc.Health == "healthy" {
			known[svc.Port] = true
		}
	}
	out := []string{}
	ports := make([]int, 0, len(r.urls))
	for port := range r.urls {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	for _, port := range ports {
		if !known[port] {
			out = append(out, r.urls[port])
		}
	}
	return out
}

// Return true while explicit startup requirements block normal aggregation.
func (l *Launcher) waitForReadiness(rt *app.Runtime, r *startupReadiness, svcs []*store.AppService, now time.Time, col *logbus.Collector) bool {
	if r == nil || len(r.urls) == 0 {
		return false
	}
	pending := r.pending(svcs)
	if len(pending) == 0 {
		r.reached = true
		return false
	}
	// Once ready, normal health aggregation (including retry debouncing) applies.
	if r.reached {
		return false
	}
	status := app.StatusStarting
	if !now.Before(r.deadline) {
		status = app.StatusDegraded
	}
	if rt.GetStatus() != status {
		if col != nil {
			if status == app.StatusDegraded {
				col.Warn("[就绪] 启动等待超时，尚未就绪: " + strings.Join(pending, ", ") + "；继续检查，可自动恢复")
			}
		}
		l.Manager.Transition(rt, status, nil)
	}
	return true
}
