package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/store"
)

func TestStatusTransitionWritesProjectDiagnosticArchive(t *testing.T) {
	project := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "launcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	managed := &store.App{ID: "diagnostic-app", Name: "diagnostic demo", Cwd: project,
		EntryScript: filepath.Join(project, "start.bat"), AdapterType: "batch",
		Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: app.StatusStarting}
	if err := st.CreateApp(managed); err != nil {
		t.Fatal(err)
	}

	server := New(st, logbus.NewHub(), adapter.NewRegistry())
	defer server.Shutdown()
	runtimeState := &app.Runtime{AppID: managed.ID, RunID: "run-status", Status: app.StatusStarting, StartedAt: time.Now().Add(-1500 * time.Millisecond)}
	server.Manager.Registry.Set(managed.ID, runtimeState)
	server.Manager.Transition(runtimeState, app.StatusRunning, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Diagnostics.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(project, ".launcher", "diagnostics")
	latestRaw, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var latest struct {
		EventFile string `json:"eventFile"`
		Notice    string `json:"notice"`
	}
	if err := json.Unmarshal(latestRaw, &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Notice != diagnostics.UntrustedDataNotice {
		t.Fatalf("missing untrusted data notice: %+v", latest)
	}
	eventsRaw, err := os.ReadFile(filepath.Join(dir, latest.EventFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eventsRaw), `"operation":"status.transition"`) ||
		!strings.Contains(string(eventsRaw), `"status":"running"`) ||
		!strings.Contains(string(eventsRaw), `"durationMs":`) {
		t.Fatalf("status diagnostic missing: %s", eventsRaw)
	}
}
