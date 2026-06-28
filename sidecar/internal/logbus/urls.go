// Package logbus 采集子进程 stdout/stderr，落库为原始日志，
// 并解析出结构化事件（ready/port_listen/url_detected）。
//
// 报告建议的"日志三层"：
//   1. 原始流：全量落 SQLite logs 表（本包负责）。
//   2. 事件流：抽取 ready/port_listen/url_detected/health_unhealthy（本包负责）。
//   3. 索引归档：搜索/导出（api 层基于 store 查询）。
package logbus

import (
	"regexp"
	"strconv"
	"strings"
)

// EventKind 事件类型。
type EventKind string

const (
	EventURLListen  EventKind = "url_detected"      // 日志中出现的本地 URL
	EventPortListen EventKind = "port_listen"       // 日志中出现的端口
	EventReady      EventKind = "ready"             // 框架就绪特征
	EventBuildDone  EventKind = "build_finished"    // 构建完成
	EventErrWait    EventKind = "dependency_waiting"// 依赖等待（DB 等）
)

// Event 一条结构化事件。
type Event struct {
	Kind EventKind `json:"kind"`
	Port int       `json:"port,omitempty"`
	URL  string    `json:"url,omitempty"`
	Text string    `json:"text"` // 触发该事件的原文
}

// urlPatterns 来自报告：混合框架特征 + URL 正则。
// 比硬编码"Vite 就是 5173"更稳，因为 Vite 端口占用会自动漂移。
// 每条正则带命名捕获 url（真正的 URL 片段）；匹配后用该捕获组归一化。
var urlPatterns = []*regexp.Regexp{
	// http://localhost:5173 / https://127.0.0.1:3000 / http://[::1]:8080
	regexp.MustCompile(`(?i)(?P<url>https?://(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?[^\s'"<>]*)`),
	// Vite/Next: "Local:   http://localhost:5173/"
	regexp.MustCompile(`(?i)\bLocal:\s*(?P<url>https?://[^\s]+)`),
	// Node: "started server on http://..." 或 "0.0.0.0:3000"
	regexp.MustCompile(`(?i)\bstarted server on\s+(?P<url>\S+)`),
	// 通用 "listening on http://... | host:port"
	regexp.MustCompile(`(?i)\blistening on\s+(?P<url>https?://[^\s]+|[0-9.]{7,}:\d+)`),
}

var (
	reReady      = regexp.MustCompile(`(?i)\b(ready in|ready -|started server|listening on|now listening|compiled successfully|vite v\d|Local:\s*http)\b`)
	rePort       = regexp.MustCompile(`(?i)\b(?:port|on)\s+(\d{2,5})\b`)
	reBuildDone  = regexp.MustCompile(`(?i)\b(✓ built in|build complete|webpack compiled)\b`)
	reDepWait    = regexp.MustCompile(`(?i)\b(ECONNREFUSED|connection refused|waiting for|cannot connect|database|redis).{0,40}(?:refused|timeout|retry)\b`)
)

// ParseLine 解析一行日志，返回从中抽取的事件（可能为空）。
// 同一行可能产生多个 URL/端口事件。
func ParseLine(line string) []Event {
	var events []Event

	// URL 事件
	urls := ExtractURLs(line)
	for _, u := range urls {
		events = append(events, Event{Kind: EventURLListen, URL: u, Text: line})
		if p := portFromURL(u); p > 0 {
			events = append(events, Event{Kind: EventPortListen, Port: p, Text: line})
		}
	}
	// 裸端口（无 URL 上下文）
	if m := rePort.FindStringSubmatch(line); len(m) > 1 {
		if p, _ := strconv.Atoi(m[1]); p > 0 && p < 65536 {
			events = append(events, Event{Kind: EventPortListen, Port: p, Text: line})
		}
	}
	// ready
	if reReady.MatchString(line) {
		events = append(events, Event{Kind: EventReady, Text: line})
	}
	// build done
	if reBuildDone.MatchString(line) {
		events = append(events, Event{Kind: EventBuildDone, Text: line})
	}
	// dependency waiting
	if reDepWait.MatchString(line) {
		events = append(events, Event{Kind: EventErrWait, Text: line})
	}
	return events
}

// ExtractURLs 从一行文本中抽取候选 URL，去重。
// 供 logbus 与 probe 复用（probe 还会做 HTTP 健康检查）。
func ExtractURLs(line string) []string {
	seen := map[string]bool{}
	var out []string
	for _, pat := range urlPatterns {
		for _, m := range pat.FindAllStringSubmatch(line, -1) {
			raw := ""
			if len(m) > 1 {
				raw = m[1]
			}
			u := normalizeURL(raw)
			if u != "" && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

// normalizeURL 规整 URL：去尾部标点，host:port 补成 http://localhost:port。
func normalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// 处理 "Local: http://..." 这类残留前缀
	if idx := strings.Index(strings.ToLower(s), "http"); idx >= 0 {
		s = s[idx:]
	}
	s = strings.TrimRight(s, ".,;)]}>'\"")
	// 形如 0.0.0.0:3000 / 127.0.0.1:3000 -> http://localhost:3000
	if !strings.Contains(strings.ToLower(s), "://") && looksLikeHostPort(s) {
		port := portFromHostPort(s)
		if port > 0 {
			return "http://localhost:" + strconv.Itoa(port)
		}
	}
	// 补全协议（localhost 无 scheme 的情况）
	if strings.HasPrefix(s, "localhost") || strings.HasPrefix(s, "127.0.0.1") {
		s = "http://" + s
	}
	return s
}

// looksLikeHostPort 判断 "host:port" 形态（用于 started server on 0.0.0.0:3000）。
func looksLikeHostPort(s string) bool {
	ci := strings.LastIndex(s, ":")
	if ci <= 0 {
		return false
	}
	for i := ci + 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return ci > 0
}

// portFromHostPort 从 "0.0.0.0:3000" 抽端口。
func portFromHostPort(s string) int {
	ci := strings.LastIndex(s, ":")
	if ci < 0 {
		return 0
	}
	p, err := strconv.Atoi(s[ci+1:])
	if err != nil {
		return 0
	}
	return p
}

// portFromURL 从 URL 中抽端口，无端口返回 0。
func portFromURL(u string) int {
	// 取 host:port 部分
	idx := strings.Index(u, "://")
	if idx < 0 {
		return 0
	}
	rest := u[idx+3:]
	// 去掉 path
	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[:slash]
	}
	if colon := strings.LastIndex(rest, ":"); colon >= 0 {
		p, err := strconv.Atoi(rest[colon+1:])
		if err == nil {
			return p
		}
	}
	return 0
}

// InferLevel 根据文本粗略推断日志级别（stdout 默认 info，stderr 默认 error，含 error/warn 关键字调整）。
func InferLevel(stream, text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fail") || strings.Contains(lower, "exception"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warn"
	case stream == "stderr":
		return "error"
	}
	return "info"
}
