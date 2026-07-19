package logbus

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

// WSType WebSocket 消息类型。
type WSType string

const (
	WSLog     WSType = "app:log"
	WSEvent   WSType = "app:event"
	WSStatus  WSType = "app:status"
	WSURL     WSType = "app:url"
	WSServices WSType = "app:services" // 多服务状态变更
	WSHello   WSType = "hello"
)

// WSMessage 推给前端的 WebSocket 消息统一信封。
type WSMessage struct {
	Type     WSType             `json:"type"`
	Time     string             `json:"time"`
	App      string             `json:"appId,omitempty"`
	Run      string             `json:"runId,omitempty"`
	Log      *store.LogEntry    `json:"log,omitempty"`
	Event    *Event             `json:"event,omitempty"`
	Status   string             `json:"status,omitempty"`
	Old      string             `json:"old,omitempty"`
	URL      string             `json:"url,omitempty"`
	Ports    []int              `json:"ports,omitempty"`
	Services []*store.AppService `json:"services,omitempty"` // 多服务列表
}

// Client 一个 WebSocket 订阅者。Write 由 WS handler 注入。
type Client struct {
	id     int64
	send   chan []byte
	closed atomic.Bool
}

func (c *Client) Send(b []byte) {
	if c.closed.Load() {
		return
	}
	select {
	case c.send <- b:
	default:
		// 队列满（前端慢/断开）则丢弃，避免阻塞广播。前端可从历史日志补全。
	}
}

func (c *Client) Receive() <-chan []byte { return c.send }

func (c *Client) Close() {
	if c.closed.CompareAndSwap(false, true) {
		close(c.send)
	}
}

// Hub 维护所有 WebSocket 客户端，向它们广播消息。
type Hub struct {
	mu       sync.RWMutex
	clients  map[int64]*Client
	nextID   int64
}

func NewHub() *Hub {
	return &Hub{clients: map[int64]*Client{}}
}

// Register 注册一个客户端，返回该客户端与卸载函数。
func (h *Hub) Register() (*Client, func()) {
	id := atomic.AddInt64(&h.nextID, 1)
	c := &Client{id: id, send: make(chan []byte, 256)}
	h.mu.Lock()
	h.clients[id] = c
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		delete(h.clients, id)
		h.mu.Unlock()
		c.Close()
	}
}

// BroadcastLog 广播一条原始日志（必须带 DB id，否则前端按 id 去重会互相覆盖）。
func (h *Hub) BroadcastLog(appID string, entry *store.LogEntry) {
	if entry == nil {
		return
	}
	// 拷贝一份，避免调用方后续改动影响已入队消息。
	e := *entry
	msg := WSMessage{
		Type: WSLog, Time: nowRFC3339(), App: appID, Run: e.AppRunID,
		Log: &e,
	}
	h.send(msg)
}

// BroadcastEvent 广播一条结构化事件。
func (h *Hub) BroadcastEvent(appID, runID string, ev Event) {
	ev2 := ev
	h.send(WSMessage{Type: WSEvent, Time: nowRFC3339(), App: appID, Run: runID, Event: &ev2})
}

// BroadcastStatus 广播状态变更。
func (h *Hub) BroadcastStatus(appID, runID, old, new string) {
	h.send(WSMessage{Type: WSStatus, Time: nowRFC3339(), App: appID, Run: runID, Old: old, Status: new})
}

// BroadcastURL 广播识别到的 URL。
func (h *Hub) BroadcastURL(appID, url string, ports []int) {
	h.send(WSMessage{Type: WSURL, Time: nowRFC3339(), App: appID, URL: url, Ports: ports})
}

// BroadcastServices 广播多服务状态（每个端口的健康情况）。
func (h *Hub) BroadcastServices(appID, runID string, services []*store.AppService) {
	h.send(WSMessage{Type: WSServices, Time: nowRFC3339(), App: appID, Run: runID, Services: services})
}

// send 把消息序列化后推给所有客户端。
func (h *Hub) send(msg WSMessage) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()
	for _, c := range clients {
		c.Send(b)
	}
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
