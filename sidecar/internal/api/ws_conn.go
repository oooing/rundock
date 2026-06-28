package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"
)

// 使用 gorilla/websocket（业界事实标准）实现 WebSocket 升级与读写。
// 仅用于本地 127.0.0.1 loopback，CheckOrigin 放开（前端由 Tauri 壳加载，Origin 不固定）。

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// wsConn 包装 *websocket.Conn，提供 ReadMessage/WriteText 抽象。
type wsConn struct {
	c *websocket.Conn
}

func upgradeWS(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return &wsConn{c: c}, nil
}

func (w *wsConn) ReadMessage(buf []byte) (int, error) {
	_, _, err := w.c.ReadMessage()
	return 0, err
}

func (w *wsConn) WriteText(msg []byte) error {
	return w.c.WriteMessage(websocket.TextMessage, msg)
}

func (w *wsConn) Close() error { return w.c.Close() }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
