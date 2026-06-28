// Package probe 负责端口/URL/健康的"观测式"发现，是日志解析之外的兜底与确认。
//
// 报告建议的三段式：
//   1. 日志正则抽 URL（logbus 已实现，probe 复用 ExtractURLs）。
//   2. 端口观测：启动前后监听端口差异（本包 snapshot 对比）。
//   3. HTTP/HTTPS 健康检查：候选 URL GET / / /health，分析状态码/标题。
package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ----- 端口快照 -----

// PortListener 描述一个监听端口。
type PortListener struct {
	Port int    `json:"port"`
	Addr string `json:"addr"`  // 监听地址，如 0.0.0.0:3000
	PID  int    `json:"pid"`   // 拥有该端口的进程（尽力获取）
}

// SnapshotListeners 获取当前所有 TCP 监听端口。
// Windows 实现：解析 netstat -ano（无需管理员，比 GetExtendedTcpTable 的 cgo 路径更简单可靠）。
// 非 Windows：用 net.Listen 试探（精度低但能跑）。
func SnapshotListeners() []PortListener { return snapshotListenersOS() }

// DiffListeners 返回 after 中有而 before 中没有的端口（新增监听）。
// 用于"启动前快照 -> 启动后快照 -> 看本进程树新开了哪些端口"。
func DiffListeners(before, after []PortListener) []PortListener {
	have := map[int]bool{}
	for _, p := range before {
		have[p.Port] = true
	}
	var out []PortListener
	for _, p := range after {
		if !have[p.Port] {
			out = append(out, p)
		}
	}
	return out
}

// FilterByPID 只保留指定 PID 集合拥有的端口。
func FilterByPID(list []PortListener, pids map[int]bool) []PortListener {
	var out []PortListener
	for _, p := range list {
		if pids[p.PID] {
			out = append(out, p)
		}
	}
	return out
}

// ----- HTTP 健康检查 -----

// HealthResult 健康检查结果。
type HealthResult struct {
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode"`
	Reachable  bool   `json:"reachable"`
	Title      string `json:"title"`
	Server     string `json:"server"`
}

var httpClient = &http.Client{Timeout: 3 * time.Second}

// CheckHealth 对候选 URL 做健康检查。
// 路径优先级：原 URL -> /health -> /api/health -> /。
// 第一个返回 2xx/3xx 的即视为可达。
func CheckHealth(ctx context.Context, baseURL string) *HealthResult {
	candidates := healthPaths(baseURL)
	for _, u := range candidates {
		if r := probeURL(ctx, u); r != nil && r.Reachable {
			return r
		}
	}
	// 没有一个可达，返回最后探测结果（含状态码供诊断）或空
	for _, u := range candidates {
		if r := probeURL(ctx, u); r != nil {
			return r
		}
	}
	return &HealthResult{URL: baseURL}
}

func healthPaths(base string) []string {
	base = strings.TrimRight(base, "/")
	// 探测顺序：优先专用健康端点（更可靠，/ 常返回 404/重定向），最后才试 /
	var out []string
	out = append(out, base+"/health", base+"/api/health", base+"/healthz")
	// 若 base 已带 path（非根），也单独试一次
	if u, err := url.Parse(base); err == nil && u.Path != "" && u.Path != "/" {
		out = append(out, base)
	}
	out = append(out, base+"/")
	return out
}

func probeURL(ctx context.Context, u string) *HealthResult {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return &HealthResult{URL: u}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	r := &HealthResult{
		URL:        u,
		StatusCode: resp.StatusCode,
		Reachable:  resp.StatusCode >= 200 && resp.StatusCode < 400,
		Server:     resp.Header.Get("Server"),
		Title:      extractTitle(string(body)),
	}
	return r
}

// extractTitle 从 HTML 抽 <title>（粗匹配）。
func extractTitle(html string) string {
	lower := strings.ToLower(html)
	start := strings.Index(lower, "<title")
	if start < 0 {
		return ""
	}
	rest := html[start:]
	gt := strings.Index(rest, ">")
	if gt < 0 {
		return ""
	}
	rest = rest[gt+1:]
	end := strings.Index(strings.ToLower(rest), "</title>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// ConfirmReachable 对一组候选 URL 做探测，返回第一个可达的 URL（用于"确认"日志里发现的 URL 真的可打开）。
func ConfirmReachable(ctx context.Context, urls []string, timeout time.Duration) (string, *HealthResult) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, u := range urls {
		if r := CheckHealth(cctx, u); r != nil && r.Reachable {
			return u, r
		}
	}
	return "", nil
}

// JoinHostPort 工具：把 host 与 port 拼成 URL（默认 http）。
func JoinHostPort(host string, port int) string {
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, strconv.Itoa(port)))
}
