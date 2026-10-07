//go:build windows

package api

import (
	"context"
	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestartTreeIdentityBoundaries(t *testing.T) {
	a := &store.App{Cwd: `C:\fixture`, EntryScript: `C:\fixture\start.cmd`, HealthURL: "http://127.0.0.1:3333"}
	base := []recovery.Process{
		{PID: 100, ParentPID: 10, Created: "1000", Executable: `C:\Windows\System32\cmd.exe`, CommandLine: `cmd.exe /c C:\fixture\start.cmd`},
		{PID: 101, ParentPID: 100, Created: "1100", Executable: `C:\tools\node.exe`, CommandLine: `node C:\fixture\server.js`},
	}
	for _, test := range []struct {
		name   string
		mutate func(*recovery.RuntimeSnapshot)
		self   int
		allow  bool
	}{
		{"exact tree", nil, 999, true},
		{"editor mention", func(s *recovery.RuntimeSnapshot) { s.Processes[0].Executable = `C:\editor.exe` }, 999, false},
		{"echo is not execution", func(s *recovery.RuntimeSnapshot) { s.Processes[0].CommandLine = `cmd /c echo C:\fixture\start.cmd` }, 999, false},
		{"reused parent", func(s *recovery.RuntimeSnapshot) { s.Processes[0].Created = "9999" }, 999, false},
		{"missing child identity", func(s *recovery.RuntimeSnapshot) { s.Processes[1].Created = "" }, 999, false},
		{"self protection", nil, 101, false},
		{"ambiguous launches", func(s *recovery.RuntimeSnapshot) {
			p := s.Processes[0]
			p.PID = 200
			s.Processes = append(s.Processes, p)
		}, 999, false},
		{"foreign listener", func(s *recovery.RuntimeSnapshot) { s.Listeners[0].PID = 444 }, 999, false},
		{"shared browser child", func(s *recovery.RuntimeSnapshot) {
			s.Processes = append(s.Processes, recovery.Process{PID: 104, ParentPID: 100, Created: "1200", Executable: `C:\Program Files\Chrome\chrome.exe`})
		}, 999, false},
		{"detached project listener", func(s *recovery.RuntimeSnapshot) { s.Processes[1].ParentPID = 10 }, 999, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			snap := recovery.RuntimeSnapshot{Processes: append([]recovery.Process{}, base...), Listeners: []probe.PortListener{{PID: 101, Port: 3333}}}
			if test.mutate != nil {
				test.mutate(&snap)
			}
			targets, err := restartTree(a, snap, test.self)
			if (err == nil) != test.allow {
				t.Fatalf("targets=%v err=%v", targets, err)
			}
			if test.allow && (len(targets) != 2 || targets[0].PID != 100) {
				t.Fatal(targets)
			}
		})
	}
}

func restartTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, logbus.NewHub(), adapter.NewRegistry())
	t.Cleanup(func() { s.Diagnostics.Close(); st.Close() })
	return s
}

func TestRestartConfirmationScopeAndExpiry(t *testing.T) {
	s := restartTestServer(t)
	for _, test := range []struct {
		name, app, origin string
		expiry            time.Time
	}{
		{"expired", "app", "http://127.0.0.1:17656", time.Now().Add(-time.Minute)},
		{"different app", "other", "http://127.0.0.1:17656", time.Now().Add(time.Minute)},
		{"different origin", "app", "http://localhost:17656", time.Now().Add(time.Minute)},
	} {
		t.Run(test.name, func(t *testing.T) {
			s.restartPlans = map[string]portPlan{"token": {AppID: test.app, Origin: test.origin, Expires: test.expiry}}
			r := httptest.NewRequest("POST", "/api/apps/app/restart-confirm", strings.NewReader(`{"confirmationToken":"token"}`))
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Origin", "http://127.0.0.1:17656")
			w := httptest.NewRecorder()
			s.Router().ServeHTTP(w, r)
			if w.Code != 409 || len(s.restartPlans) != 0 {
				t.Fatal(w.Code, w.Body.String(), s.restartPlans)
			}
		})
	}
	for _, endpoint := range []string{"restart-plan", "restart-confirm"} {
		r := httptest.NewRequest("POST", "/api/apps/app/"+endpoint, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "https://untrusted.example")
		w := httptest.NewRecorder()
		s.Router().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}

func TestManagedRestartRefusesConcurrentPublisherActivity(t *testing.T) {
	s := restartTestServer(t)
	s.Manager.Registry.Set("app", &app.Runtime{AppID: "app", Status: "running"})
	resume, err := s.Publisher.BeginRestart()
	if err != nil {
		t.Fatal(err)
	}
	defer resume()
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, httptest.NewRequest("POST", "/api/apps/app/restart", nil))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "构建") {
		t.Fatal(w.Code, w.Body.String())
	}
	if !s.Manager.Registry.IsRunning("app") {
		t.Fatal("managed run was stopped")
	}
}

func TestSelfRestartWithoutSupervisorDoesNotExit(t *testing.T) {
	s := restartTestServer(t)
	t.Setenv("LAUNCHER_SUPERVISOR_PID", "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	a := &store.App{ID: "self", Name: "RunDock", Cwd: cwd, EntryScript: filepath.Join(cwd, "start.cmd")}
	if err = s.Store.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	s.runtimeMonitor = &runtimeMonitor{gate: make(chan struct{}, 1), read: func() (recovery.RuntimeSnapshot, error) {
		return recovery.RuntimeSnapshot{Processes: []recovery.Process{{PID: os.Getpid(), ParentPID: os.Getppid(), Created: "1000", Executable: filepath.Join(cwd, "sidecar.exe")}}}, nil
	}}
	v, _, _, err := s.restartEvidence(context.Background(), a.ID)
	if err != nil || v.Kind != "self" || v.CanRestart || !strings.Contains(v.Message, "旧启动器") {
		t.Fatal(v, err)
	}
	if s.RestartRequested() || s.closing.Load() {
		t.Fatal("old supervisor caused destructive shutdown")
	}
}
