package api

import (
	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeViewUsesCurrentRunAndClearsTerminalData(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entry := filepath.Join(t.TempDir(), "start.cmd")
	if err := os.WriteFile(entry, []byte("rem rundock:open http://127.0.0.1:4310/library\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &store.App{ID: "app", Name: "Test", EntryScript: entry, LastStatus: "stopped", LastURL: "http://localhost:4311"}
	if err := st.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "new"} {
		if err := st.CreateRun(&store.AppRun{ID: id, AppID: a.ID, PID: 123, Status: "running", StartedAt: "2026-09-09T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertService(&store.AppService{ID: id, AppID: a.ID, AppRunID: id, Port: 4311, URL: a.LastURL, Health: "healthy", DetectedAt: "2026-09-09T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	s := New(st, logbus.NewHub(), adapter.NewRegistry())
	rt := &app.Runtime{AppID: a.ID, RunID: "new", PID: 456, Status: "starting"}
	s.Manager.Registry.Set(a.ID, rt)
	response := s.startResponse(a.ID, outcomePass, "started")
	row := response["app"].(map[string]any)
	if response["configUpdated"] != false || row["pid"] != 456 || row["runId"] != "new" {
		t.Fatal(response)
	}
	if row["lastUrl"] != "http://127.0.0.1:4310/library" {
		t.Fatal("backend discovery replaced main URL", row)
	}
	services := row["services"].([]*store.AppService)
	if len(services) != 1 || services[0].AppRunID != "new" {
		t.Fatal(services)
	}
	for _, terminal := range []string{"stopped", "failed"} {
		rt.SetStatus(terminal) // Also covers the transition before registry cleanup.
		row = appView(a, s)
		if row["pid"] != 0 || row["runId"] != "" || len(row["services"].([]*store.AppService)) != 0 {
			t.Fatal(row)
		}
		known := row["knownServices"].([]*store.AppService)
		if len(known) != 1 || known[0].AppRunID != "new" {
			t.Fatal("last known service must survive without becoming a live service", known)
		}
	}
	s.Manager.Registry.Remove(a.ID)
	if len(appView(a, s)["services"].([]*store.AppService)) != 0 {
		t.Fatal("historical services leaked")
	}
	historical, err := st.ListServicesByApp(a.ID)
	if err != nil || len(historical) != 2 {
		t.Fatal("history should be retained", err)
	}
}
