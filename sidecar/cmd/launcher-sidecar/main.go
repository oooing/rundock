// launcher-sidecar 是 Windows AI 启动平台的核心后端进程。
// 由 Tauri 壳 spawn，监听本地端口，提供 HTTP REST + WebSocket API。
// 启动后把实际端口写入 %APPDATA%\launcher-sidecar\sidecar.port 供壳读取。
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/api"
	"github.com/launcher-sidecar/internal/config"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

func main() {
	addrFlag := flag.String("addr", "", "监听地址（覆盖默认/环境变量）")
	portFlag := flag.Int("port", 0, "固定端口（0=随机）；非 0 时覆盖 addr")
	flag.Parse()

	cfg := config.Default()
	if *addrFlag != "" {
		cfg.HTTPAddr = *addrFlag
	}
	if *portFlag > 0 {
		cfg.HTTPAddr = "127.0.0.1:" + strconv.Itoa(*portFlag)
	}

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("launcher-sidecar starting: %s", cfg)

	// 打开数据库
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// 新 sidecar 不继承上次进程的运行态；端口不能证明项目身份。
	if n, err := st.ResetRuntimeState(); err != nil {
		log.Printf("warn: reset app runtime state: %v", err)
	} else if n > 0 {
		log.Printf("startup: reset %d app(s) to stopped", n)
	}

	// 事件总线
	hub := logbus.NewHub()

	// 适配器注册表（batch/ps1/npm/yarn/pnpm）
	registry := adapter.NewRegistry()
	registry.Register(adapter.BatchAdapter{})
	registry.Register(adapter.PS1Adapter{})
	registry.Register(adapter.NewNPMAdapter())
	registry.Register(adapter.NewYarnAdapter())
	registry.Register(adapter.NewPnpmAdapter())

	// API server（内部组装 manager + launcher）
	server := api.New(st, hub, registry)

	// 启动 HTTP
	port, err := server.ListenAndServe(cfg.HTTPAddr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	cfg.HTTPPort = port
	log.Printf("listening on http://127.0.0.1:%d (ws: ws://127.0.0.1:%d/ws)", port, port)

	// 写端口发现文件：壳与前端据此连接
	writePortFile(cfg.DataDir, port)

	// 优雅退出：SIGINT/SIGTERM；退出时 Job Object 关闭即回收所有托管进程。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Printf("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		<-ctx.Done()
		os.Exit(0)
	}()
	_ = server.Shutdown()
	removePortFile(cfg.DataDir)
	log.Printf("bye")
}

// 端口发现文件
func writePortFile(dataDir string, port int) {
	p := filepath.Join(dataDir, "sidecar.port")
	_ = os.WriteFile(p, []byte(strconv.Itoa(port)), 0o644)
}
func removePortFile(dataDir string) {
	_ = os.Remove(filepath.Join(dataDir, "sidecar.port"))
}
