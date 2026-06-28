package api

import (
	"net/http"
	"time"

	"github.com/launcher-sidecar/internal/logbus"
)

// handleWS 升级 WebSocket，注册到 Hub，持续把 Hub 推来的消息写给前端。
// 前端订阅后可收到 app:log / app:event / app:status / app:url。
// 这里用最小依赖的 golang.org/x/net/websocket? 不，标准库没有 ws；
// 为避免新增依赖，用一个极简的 RFC6455 服务端升级（仅满足本地 loopback 场景）。
// 实际项目可换 gorilla/websocket；这里为了零依赖用标准库手写关键帧。
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	conn, err := upgradeWS(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ws upgrade failed: "+err.Error())
		return
	}
	defer conn.Close()

	client, unregister := s.Hub.Register()
	defer unregister()

	// 发送 hello
	client.Send(mustMarshal(logbus.WSMessage{Type: logbus.WSHello, Time: time.Now().UTC().Format(time.RFC3339Nano)}))

	// 读循环：丢弃前端发来的内容（无需双向消息），只保持连接 + 处理 ping。
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := conn.ReadMessage(buf); err != nil {
				return
			}
		}
	}()

	// 写循环：把 channel 里的消息写成 ws 文本帧
	for msg := range client.Receive() {
		if err := conn.WriteText(msg); err != nil {
			return
		}
	}
}

func mustMarshal(v any) []byte {
	b, _ := jsonMarshal(v)
	return b
}
