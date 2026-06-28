package logbus

import (
	"sync"

	"github.com/launcher-sidecar/internal/store"
)

// Collector 绑定到一个 app run，采集其 stdout/stderr 并落库 + 解析事件。
// 它提供 OnStdout/OnStderr 两个回调，可直接传给 proc.Start。
type Collector struct {
	AppID string
	RunID string
	store *store.Store
	hub   *Hub // 可为 nil（无订阅者时不广播）

	// OnEvent 在解析出事件时回调（由 launcher 注入，用于触发状态机/URL 识别）。
	OnEvent func(*Collector, Event)

	// OnURL 首次发现 URL 时回调（launcher 据此更新 app.last_url）。
	OnURL func(*Collector, string)
	urlSeen bool

	mu sync.Mutex
}

// NewCollector 创建采集器。
func NewCollector(st *store.Store, hub *Hub, appID, runID string) *Collector {
	return &Collector{store: st, hub: hub, AppID: appID, RunID: runID}
}

// OnStdout 处理一行 stdout。落库 + 解析事件 + 广播。
func (c *Collector) OnStdout(line string) {
	c.ingest("stdout", line)
}

// OnStderr 处理一行 stderr。
func (c *Collector) OnStderr(line string) {
	c.ingest("stderr", line)
}

func (c *Collector) ingest(stream, line string) {
	if line == "" {
		return
	}
	level := InferLevel(stream, line)

	// 落库（原始流）
	_ = c.store.InsertLog(c.RunID, stream, level, line)

	// 广播原始日志
	if c.hub != nil {
		c.hub.BroadcastLog(c.AppID, c.RunID, stream, level, line)
	}

	// 解析事件
	events := ParseLine(line)
	for _, ev := range events {
		if c.hub != nil {
			c.hub.BroadcastEvent(c.AppID, c.RunID, ev)
		}
		if c.OnEvent != nil {
			c.OnEvent(c, ev)
		}
		// URL 首次发现
		if ev.Kind == EventURLListen && ev.URL != "" {
			c.mu.Lock()
			first := !c.urlSeen
			c.urlSeen = true
			c.mu.Unlock()
			if first && c.OnURL != nil {
				c.OnURL(c, ev.URL)
			}
		}
	}
}

// EmitEvent 允许外部（如 probe 的健康检查结果）注入事件。
func (c *Collector) EmitEvent(ev Event) {
	if c.hub != nil {
		c.hub.BroadcastEvent(c.AppID, c.RunID, ev)
	}
	if c.OnEvent != nil {
		c.OnEvent(c, ev)
	}
}

// EmitLog 允许外部写入一条带级别标注的日志（如 sidecar 自身的提示）。
func (c *Collector) EmitLog(stream, level, text string) {
	if text == "" {
		return
	}
	_ = c.store.InsertLog(c.RunID, stream, level, text)
	if c.hub != nil {
		c.hub.BroadcastLog(c.AppID, c.RunID, stream, level, text)
	}
}
