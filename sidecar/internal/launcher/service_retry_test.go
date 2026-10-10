package launcher

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/store"
)

func TestDiscoverServicesRetriesPortAfterHTTPBecomesReady(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	var ready atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})}
	go server.Serve(ln)
	defer server.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rt := &app.Runtime{AppID: "app", RunID: "run", RootPID: os.Getpid(), Status: app.StatusStarting}
	l := &Launcher{Store: st}
	rejected := map[int]int{}

	// 只有进程树归属、所有路径返回 400 的端口不能当作项目服务。
	l.discoverServices(rt.AppID, rt, nil, nil, nil, nil, rejected, nil, 1, nil)
	if st.HasService(rt.RunID, port) {
		t.Fatal("unready port should not be registered")
	}

	ready.Store(true)

	// 同一 PID 的端口就绪后必须允许重新探测并登记。
	l.discoverServices(rt.AppID, rt, nil, nil, nil, nil, rejected, nil, 2, nil)
	if !st.HasService(rt.RunID, port) {
		t.Fatal("ready port was permanently rejected")
	}
}

func TestDiscoverServicesRejectsHintWithoutOwnership(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rt := &app.Runtime{AppID: "app", RunID: "run", RootPID: 999999, Status: app.StatusStarting}
	l := &Launcher{Store: st}
	l.discoverServices(rt.AppID, rt, nil, nil, nil, map[int]bool{port: true}, map[int]int{}, nil, 1, nil)

	if st.HasService(rt.RunID, port) {
		t.Fatal("port hint without process or current-run log ownership must not be registered")
	}
}
