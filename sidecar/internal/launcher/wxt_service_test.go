package launcher

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

func TestWXTDiscoveryTransitionsToRunningAndMonitorsFailures(t *testing.T) {
	var clientStatus atomic.Int32
	clientStatus.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@vite/client" {
			w.Header().Set("Content-Type", "text/javascript")
			w.WriteHeader(int(clientStatus.Load()))
			_, _ = w.Write([]byte(`console.debug('[vite]'); new WebSocket(url, 'vite-hmr'); export function createHotContext() {}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	l, rt := healthTestLauncher(t)
	rt.RootPID = os.Getpid()

	// Reproduce the real corrupted WXT line before any service discovery.
	urls := logbus.ExtractURLs(fmt.Sprintf("√ Started dev server @ http://localhost:%d13:01:58", port))
	if len(urls) != 1 || urls[0] != fmt.Sprintf("http://localhost:%d", port) {
		t.Fatalf("WXT log not parsed: %v", urls)
	}
	// No log or port hints: process ownership + the actual WXT endpoint suffice.
	l.discoverServices(rt.AppID, rt, nil, nil, nil, nil, map[int]int{}, nil, 1, nil)
	if !l.Store.HasService(rt.RunID, port) {
		t.Fatal("WXT service with no homepage was rejected")
	}
	checks := map[string]*serviceHealthCheck{}
	l.recheckAndAggregate(rt.AppID, rt, nil, checks, time.Now(), nil)
	if rt.GetStatus() != app.StatusRunning {
		t.Fatalf("ready WXT stuck in %s", rt.GetStatus())
	}
	services, err := l.Store.ListServicesByRun(rt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var svc *store.AppService
	for _, item := range services {
		if item.Port == port {
			svc = item
			break
		}
	}
	if svc == nil || svc.Health != "healthy" || svc.Role != store.RoleFrontend || svc.HealthProbeURL != fmt.Sprintf("http://localhost:%d/@vite/client", port) {
		t.Fatalf("WXT service details not persisted: %+v", svc)
	}
	due := func() {
		l.recheckAndAggregate(rt.AppID, rt, nil, checks, checks[svc.ID].nextCheck.Add(time.Millisecond), nil)
	}
	clientStatus.Store(http.StatusServiceUnavailable)
	due()
	if rt.GetStatus() != app.StatusRunning {
		t.Fatal("single transient failure must be tolerated")
	}
	due()
	if rt.GetStatus() != app.StatusDegraded {
		t.Fatal("real WXT failure was hidden")
	}
	if got := healthTestSnapshot(t, l, rt, svc.ID); got.HealthReason != "http_status:503" {
		t.Fatalf("wrong failure reason: %s", got.HealthReason)
	}
	clientStatus.Store(http.StatusOK)
	due()
	if rt.GetStatus() != app.StatusRunning {
		t.Fatal("WXT recovery not recognized")
	}
}

func TestWXTEndpointWithoutProcessOwnershipIsNotDiscovered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@vite/client" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`'[vite]'; 'vite-hmr'; function createHotContext() {}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	l, rt := healthTestLauncher(t)
	rt.RootPID = 999999
	l.discoverServices(rt.AppID, rt, nil, nil, nil, map[int]bool{port: true}, map[int]int{}, nil, 1, nil)
	if l.Store.HasService(rt.RunID, port) {
		t.Fatal("another project's WXT endpoint was incorrectly adopted")
	}
}
