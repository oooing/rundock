package launcher

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/app"
)

func TestReadinessCommentsAreExplicitAndValidated(t *testing.T) {
	cases := []struct {
		name, text string
		wantError  bool
	}{
		{"batch", "rem rundock:ready http://127.0.0.1:4310/library\r\nREM rundock:ready http://localhost:4311/api/health\r\nrem rundock:timeout 60", false},
		{"powershell", "# rundock:ready http://[::1]:4310/ready\n# rundock:timeout 10", false},
		{"not a directive", "echo http://localhost:4310\nset FRONTEND_PORT=4310", false},
		{"invalid url", "rem rundock:ready not-a-url", true},
		{"remote host", "rem rundock:ready https://example.com:443/", true},
		{"missing port", "rem rundock:ready http://localhost/", true},
		{"invalid port", "rem rundock:ready http://localhost:70000/", true},
		{"duplicate port", "rem rundock:ready http://localhost:4310/a\nrem rundock:ready http://localhost:4310/b", true},
		{"bad timeout", "rem rundock:timeout 0", true},
		{"typo", "rem rundock:read http://localhost:4310/", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "start.cmd")
			if err := os.WriteFile(p, []byte(c.text), 0600); err != nil {
				t.Fatal(err)
			}
			r, err := readStartupReadiness(p, 30*time.Second)
			if (err != nil) != c.wantError {
				t.Fatalf("read: %v, want error %v", err, c.wantError)
			}
			if c.name == "batch" && (len(r.urls) != 2 || r.timeout != 60*time.Second) {
				t.Fatalf("wrong requirements: %#v", r)
			}
			if c.name == "not a directive" && len(r.urls) != 0 {
				t.Fatal("guessed ports became requirements")
			}
		})
	}
}

func TestReadinessWaitsForOwnedServicesAndRecoversAfterTimeout(t *testing.T) {
	var frontReady atomic.Bool
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The ordinary page can be 200 while the explicit ready endpoint is 503.
		if r.URL.Path == "/ready" && !frontReady.Load() {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer front.Close()
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer back.Close()
	l, rt := healthTestLauncher(t)
	now := time.Now()
	rt.StartedAt = now
	r := &startupReadiness{urls: map[int]string{
		front.Listener.Addr().(*net.TCPAddr).Port: front.URL + "/ready",
		back.Listener.Addr().(*net.TCPAddr).Port:  back.URL + "/health",
	}, timeout: 30 * time.Second, deadline: now.Add(30 * time.Second)}
	checks := map[string]*serviceHealthCheck{}
	tick := func(seconds int, want string) {
		t.Helper()
		l.recheckAndAggregate(rt.AppID, rt, nil, checks, now.Add(time.Duration(seconds)*time.Second), r)
		if rt.GetStatus() != want {
			t.Fatalf("at %ds: %s, want %s", seconds, rt.GetStatus(), want)
		}
	}
	tick(0, app.StatusStarting) // No discovered services yet.
	addHealthTestService(t, l, rt, "back", back)
	tick(1, app.StatusStarting) // Front port exists but is not owned/discovered.
	tick(31, app.StatusDegraded)
	services, err := l.Store.ListServicesByRun(rt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	pending := r.pending(services)
	if len(pending) != 1 || !strings.HasSuffix(pending[0], "/ready") {
		t.Fatalf("wrong missing service: %v", pending)
	}
	addHealthTestService(t, l, rt, "front", front)
	tick(35, app.StatusDegraded) // Root page 200 must not mask ready=503.
	frontReady.Store(true)
	tick(40, app.StatusRunning)
	frontReady.Store(false)
	tick(60, app.StatusRunning) // Retain first-step transient failure protection.
	tick(65, app.StatusDegraded)
	frontReady.Store(true)
	tick(70, app.StatusRunning)
}
