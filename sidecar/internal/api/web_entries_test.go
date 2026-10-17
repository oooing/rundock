package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

func TestWebEntriesChooseRealPageNotHealthyExtension(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer ext.Close()
	var status atomic.Int32
	status.Store(200)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte("<html>app</html>"))
	}))
	defer web.Close()
	extSvc := &store.AppService{ID: "ext", Port: ext.Listener.Addr().(*net.TCPAddr).Port, URL: ext.URL, Role: "frontend", Health: "healthy", HealthProbeURL: ext.URL + "/@vite/client"}
	webSvc := &store.AppService{ID: "web", Port: web.Listener.Addr().(*net.TCPAddr).Port, URL: web.URL, Role: "unknown"}
	s := &Server{webEntries: newWebEntryCache()}
	a := &store.App{ID: "test"}
	view := func(run string) map[string]any {
		row := map[string]any{"status": "running", "runId": run, "pid": 123, "lastUrl": ext.URL, "services": []*store.AppService{extSvc, webSvc}}
		s.applyWebEntries(a, row)
		return row
	}
	if got := view("first")["lastUrl"]; got != "" {
		t.Fatal("unverified URL escaped", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for view("first")["lastUrl"] != web.URL {
		if time.Now().After(deadline) {
			t.Fatal("web not selected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows := view("first")["services"].([]*store.AppService)
	if rows[0].WebState != "not_page" || rows[0].WebURL != "" {
		t.Fatal(rows[0])
	}
	if extSvc.WebState != "" || webSvc.WebState != "" {
		t.Fatal("mutated source records")
	}
	if got := view("new-run")["lastUrl"]; got != "" {
		t.Fatal("old run evidence reused", got)
	}
	// A click checks again even while the view cache still says available.
	status.Store(503)
	if got := probe.CheckWebEntry(context.Background(), web.URL); got.State != "unavailable" {
		t.Fatal(got)
	}
}

func TestWebEntriesNeverExposeHistoricalURLsForUncertainOrStoppedRuntime(t *testing.T) {
	s := &Server{webEntries: newWebEntryCache()}
	for _, status := range []string{"unknown", "checking", "stopped", "failed", "stopping"} {
		row := map[string]any{"status": status, "lastUrl": "http://localhost:3000"}
		s.applyWebEntries(&store.App{ID: "app"}, row)
		if row["lastUrl"] != "" {
			t.Fatal(status, row)
		}
	}
}

func TestWebEntryCacheIsNonblockingDeduplicatedAndRetries(t *testing.T) {
	var count atomic.Int32
	var ready atomic.Bool
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		<-release
		if !ready.Load() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html/>"))
	}))
	defer srv.Close()
	c := newWebEntryCache()
	start := time.Now()
	for i := 0; i < 50; i++ {
		if c.inspect("same", srv.URL, nil).State != "checking" {
			t.Fatal("unexpected cached result")
		}
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("render blocked on network")
	}
	close(release)
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for c.inspect("same", srv.URL, nil).State != want {
			if time.Now().After(deadline) {
				t.Fatal("timeout", want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("not_page")
	if count.Load() != 1 {
		t.Fatal("duplicate probes", count.Load())
	}
	ready.Store(true)
	c.mu.Lock()
	c.items["same"].expires = time.Time{}
	c.mu.Unlock()
	wait("available")
	if count.Load() != 2 {
		t.Fatal(count.Load())
	}
}

func TestOpenURLRejectsBrokenOrNonPageAddressBeforeOpeningBrowser(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateApp(&store.App{ID: "app", Name: "test"}); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: db}
	for _, status := range []int{404, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
		r := httptest.NewRequest("POST", "/api/apps/app/open-url", strings.NewReader(`{"url":"`+srv.URL+`"}`))
		w := httptest.NewRecorder()
		s.handleOpenURL(w, r, "app")
		srv.Close()
		if w.Code != 409 || strings.Contains(w.Body.String(), `"opened"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		want := "没有网页页面"
		if status == 503 {
			want = "暂时无法连接"
		}
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal(w.Body.String())
		}
	}
}

// Read-only acceptance against already running DuckyPlay services. No project
// restart, database write, installer or publishing operation is performed.
func TestLiveDuckyPlayBrowserEntries(t *testing.T) {
	if os.Getenv("RUNDOCK_WEB_ENTRY_LIVE") != "1" {
		t.Skip("opt-in live check")
	}
	extensionURL := os.Getenv("RUNDOCK_EXTENSION_TEST_URL")
	if extensionURL == "" {
		extensionURL = "http://localhost:11785"
	}
	for raw, want := range map[string]string{extensionURL: "not_page", "http://127.0.0.1:11784": "available", "http://127.0.0.1:17656": "available"} {
		got := probe.CheckWebEntry(context.Background(), raw)
		t.Logf("%s: %+v", raw, got)
		if got.State != want {
			t.Fatal(raw, got)
		}
	}
}
