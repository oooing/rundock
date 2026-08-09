// Package api 提供 HTTP REST + WebSocket 端点，前端通过 127.0.0.1 直连本 sidecar。
package api

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/launcher"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

// Server 汇总所有依赖。
type Server struct {
	Store    *store.Store
	Manager  *app.Manager
	Hub      *logbus.Hub
	Launcher *launcher.Launcher
	Registry *adapter.Registry
	httpSrv  *http.Server
}

// New 组装 server。Launcher 由外部创建后注入（依赖 store/hub/registry）。
func New(s *store.Store, hub *logbus.Hub, reg *adapter.Registry) *Server {
	mgr := app.NewManager(s)
	// 状态变更回调：广播
	mgr.OnStatus = func(appID, runID, old, new string) {
		hub.BroadcastStatus(appID, runID, old, new)
	}
	l := launcher.New(s, mgr, hub, reg)
	return &Server{Store: s, Manager: mgr, Hub: hub, Launcher: l, Registry: reg}
}

// Router 构建路由（含 CORS 中间件）。
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/import", s.handleImport)
	mux.HandleFunc("/api/apps", s.handleApps)
	mux.HandleFunc("/api/apps/reorder", s.handleAppsReorder)
	mux.HandleFunc("/api/apps/", s.handleAppDetail) // /api/apps/{id}...
	mux.HandleFunc("/api/groups", s.handleGroups)
	mux.HandleFunc("/api/groups/", s.handleGroupDetail)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/export", s.handleExport)
	mux.HandleFunc("/api/import-config", s.handleImportConfig)
	mux.HandleFunc("/ws", s.handleWS)

	return s.withCORS(logging(mux))
}

// ListenAndServe 在 addr 上监听。addr 含 :0 时回填实际端口到返回值。
func (s *Server) ListenAndServe(addr string) (int, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s.httpSrv = &http.Server{Handler: s.Router()}
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http serve error: %v", err)
		}
	}()
	return port, nil
}

// Shutdown 优雅关闭 HTTP 服务。
func (s *Server) Shutdown() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(ctx)
}

// ----- 中间件 -----

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).String())
	})
}

// ----- JSON helpers -----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// pathID 从 /api/apps/{id}/... 提取 id 与剩余子路径。
func pathTail(prefix, fullPath string) (id, rest string) {
	rest = strings.TrimPrefix(fullPath, prefix)
	rest = strings.TrimPrefix(rest, "/")
	if slash := strings.Index(rest, "/"); slash >= 0 {
		return rest[:slash], rest[slash+1:]
	}
	return rest, ""
}
