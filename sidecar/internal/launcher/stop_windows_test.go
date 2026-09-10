//go:build windows

package launcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/proc"
	"github.com/launcher-sidecar/internal/store"
)

func TestStopBatchWaitingForConfirmation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "wait.bat")
	if err := os.WriteFile(script, []byte("@echo off\r\n:loop\r\ntimeout /t 30 /nobreak >nul\r\ngoto loop\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSetting("grace_period_seconds", "10"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateApp(&store.App{ID: "app", Name: "app", EntryScript: script, Cwd: dir, AdapterType: "batch"}); err != nil {
		t.Fatal(err)
	}

	reg := adapter.NewRegistry()
	reg.Register(adapter.BatchAdapter{})
	l := New(s, app.NewManager(s), nil, reg)
	// Settings changed after creating the launcher must apply without restarting it.
	for key, value := range map[string]string{"grace_period_seconds": "1", "url_discover_timeout_seconds": "45"} {
		if err := s.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	l.startProcess = func(ctx context.Context, command *proc.PreparedCommand, onLine func(string)) (*proc.Handle, error) {
		return proc.Start(ctx, command, onLine, onLine)
	}
	if err := l.Start(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Stop("app") }()
	if err := s.SetSetting("grace_period_seconds", "2"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := l.Stop("app"); err != nil {
		run, _ := s.GetLatestRun("app")
		if run != nil {
			logs, _ := s.RecentLogs(run.ID, 0, 1000)
			for _, entry := range logs {
				t.Log(entry.Text)
			}
		}
		t.Fatal(err)
	}
	run, err := s.GetLatestRun("app")
	if err != nil || run == nil || run.Status != app.StatusStopped {
		t.Fatalf("run after Stop: %#v, err=%v", run, err)
	}
	logs, err := s.RecentLogs(run.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, entry := range logs {
		text.WriteString(entry.Text + "\n")
	}
	if !strings.Contains(text.String(), "urlDiscoverTimeout=45s grace=1s") || !strings.Contains(text.String(), "[停止] 开始停止") || !strings.Contains(text.String(), "grace=2s") {
		t.Fatalf("start/stop used stale settings:\n%s", text.String())
	}
}
