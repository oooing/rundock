package logbus

import (
	"sync"
	"time"

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

	// OnURL 每发现一个本地 URL 回调一次（launcher 收集 candidateURLs 做多端口发现）。
	OnURL func(*Collector, string)

	// OnLog 在日志完成 SQLite 持久化和广播后收到一份副本。它用于把需要
	// 长期保留的错误/性能事件异步写入项目诊断档案；回调不得阻塞日志管道。
	OnLog func(*Collector, string, string, string)

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
	c.writeAndBroadcast(stream, level, line)

	// 解析事件
	events := ParseLine(line)
	for _, ev := range events {
		if c.hub != nil {
			c.hub.BroadcastEvent(c.AppID, c.RunID, ev)
		}
		if c.OnEvent != nil {
			c.OnEvent(c, ev)
		}
		// 每个本地 URL 都回调（多服务：backend+frontend 不能只收第一个）。
		// last_url 等“只取首次”的语义由 launcher 侧自行去重。
		if ev.Kind == EventURLListen && ev.URL != "" {
			if c.OnURL != nil {
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

// EmitLog 允许外部写入一条带级别标注的日志（如 sidecar 自身的诊断/提示）。
func (c *Collector) EmitLog(stream, level, text string) {
	if text == "" {
		return
	}
	c.writeAndBroadcast(stream, level, text)
}

// Info / Warn / Error / Debug 是 event 流的便捷方法，便于 launcher 写诊断日志。
func (c *Collector) Info(text string)  { c.EmitLog("event", "info", text) }
func (c *Collector) Warn(text string)  { c.EmitLog("event", "warn", text) }
func (c *Collector) Error(text string) { c.EmitLog("event", "error", text) }
func (c *Collector) Debug(text string) { c.EmitLog("event", "debug", text) }

func (c *Collector) writeAndBroadcast(stream, level, text string) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	id, err := c.store.InsertLog(c.RunID, stream, level, text)
	if err != nil {
		// 落库失败仍尝试广播（id=0），前端可用 ts+text 兜底去重。
		id = 0
	}
	if c.hub != nil {
		c.hub.BroadcastLog(c.AppID, &store.LogEntry{
			ID:       id,
			AppRunID: c.RunID,
			Ts:       ts,
			Stream:   stream,
			Level:    level,
			Text:     text,
		})
	}
	if c.OnLog != nil {
		c.OnLog(c, stream, level, text)
	}
}
