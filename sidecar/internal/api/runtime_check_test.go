package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

func TestRuntimeChecksRecoverWithoutMutatingHistoryOrGrantingControl(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, logbus.NewHub(), adapter.NewRegistry())
	defer s.Diagnostics.Close()
	root := t.TempDir()
	a := &store.App{ID: "app", Name: "Fixture", Cwd: root, EntryScript: filepath.Join(root, "start.cmd"), LastStatus: "stopped", HealthURL: "http://localhost:3000", LastURL: "http://localhost:3001", PortHints: []int{3000}}
	if err := st.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRun(&store.AppRun{ID: "history", AppID: a.ID, Status: "stopped", StartedAt: "2026-09-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	snapshot := recovery.RuntimeSnapshot{Processes: []recovery.Process{{PID: 100, Created: "1000", Executable: filepath.Join(root, "server.exe")}}, Listeners: []probe.PortListener{{PID: 100, Port: 3000}}}
	var readErr error
	s.runtimeMonitor = &runtimeMonitor{gate: make(chan struct{}, 1), read: func() (recovery.RuntimeSnapshot, error) { return snapshot, readErr }}
	s.Launcher.BeforeStart = s.checkBeforeStart
	if got := appView(a, s)["status"]; got != "checking" {
		t.Fatal("unverified state flashed stopped", got)
	}
	if err := s.checkBeforeStart(context.Background(), a); err == nil {
		t.Fatal("duplicate start allowed")
	}
	row := appView(a, s)
	if row["status"] != "running" || row["runId"] != "" || row["pid"] != 100 {
		t.Fatal(row)
	}
	if row["lastUrl"] != a.HealthURL {
		t.Fatal("stale non-listening address shown as current", row["lastUrl"])
	}
	if len(s.Manager.Registry.All()) != 0 {
		t.Fatal("observer gained managed process ownership")
	}
	for _, operation := range []string{"stop", "restart"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/apps/app/"+operation, nil)
		s.Router().ServeHTTP(w, r)
		if w.Code != 409 {
			t.Fatal(operation, w.Code, w.Body.String())
		}
	}
	history, _ := st.GetLatestRun(a.ID)
	if history.ID != "history" || history.Status != "stopped" {
		t.Fatal("history was rewritten", history)
	}
	// Temporary network/permission failure must neither report stopped nor allow start.
	readErr = errors.New("process query failed")
	if err := s.checkBeforeStart(context.Background(), a); err == nil {
		t.Fatal("unverified start allowed")
	}
	if got := appView(a, s)["status"]; got != "unknown" {
		t.Fatal(got)
	}
	// A successful recheck clears a departed process without relaunching it.
	readErr, snapshot = nil, recovery.RuntimeSnapshot{}
	if err := s.checkBeforeStart(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if got := appView(a, s)["status"]; got != "stopped" {
		t.Fatal(got)
	}
	// A new owner appears after the card's clear snapshot; fresh start check blocks it.
	snapshot.Listeners = []probe.PortListener{{PID: 999, Port: 3000}}
	if err := s.checkBeforeStart(context.Background(), a); err == nil {
		t.Fatal("stale clear snapshot allowed conflicting start")
	}
	if got := s.observeRuntime(a); got.State != "conflict" {
		t.Fatal(got)
	}
	// Existing managed state wins over process-scan errors and keeps the true run.
	s.Manager.Registry.Set(a.ID, &app.Runtime{AppID: a.ID, RunID: "managed", PID: 456, Status: "running"})
	readErr = errors.New("offline")
	_ = s.runtimeMonitor.refresh(context.Background())
	row = appView(a, s)
	if row["status"] != "running" || row["runId"] != "managed" || row["runtimeCheck"] != nil {
		t.Fatal(row)
	}
	duplicate := *a
	duplicate.ID = "duplicate-card"
	if err := s.checkBeforeStart(context.Background(), &duplicate); err == nil {
		t.Fatal("duplicate project card allowed a second process")
	}
}

func TestRuntimeCheckBusyTimeoutAndStartupSerialization(t *testing.T) {
	m := &runtimeMonitor{gate: make(chan struct{}, 1), read: func() (recovery.RuntimeSnapshot, error) {
		t.Fatal("busy scan must not start another query")
		return recovery.RuntimeSnapshot{}, nil
	}}
	m.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := m.refresh(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	s := &Server{}
	s.startupMu.Lock()
	defer s.startupMu.Unlock()
	for _, operation := range []string{"start", "restart"} {
		w := httptest.NewRecorder()
		s.Router().ServeHTTP(w, httptest.NewRequest("POST", "/api/apps/app/"+operation, nil))
		if w.Code != 409 {
			t.Fatal(operation, w.Code)
		}
	}
}
