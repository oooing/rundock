//go:build windows

package launcher

import (
	"context"
	"os"
	"path/filepath"
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
	if err := s.SetSetting("grace_period_seconds", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateApp(&store.App{ID: "app", Name: "app", EntryScript: script, Cwd: dir, AdapterType: "batch"}); err != nil {
		t.Fatal(err)
	}

	reg := adapter.NewRegistry()
	reg.Register(adapter.BatchAdapter{})
	l := New(s, app.NewManager(s), nil, reg)
	l.startProcess = func(ctx context.Context, command *proc.PreparedCommand, onLine func(string)) (*proc.Handle, error) {
		return proc.Start(ctx, command, onLine, onLine)
	}
	if err := l.Start(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := l.Stop("app"); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetLatestRun("app")
	if err != nil || run == nil || run.Status != app.StatusStopped {
		t.Fatalf("run after Stop: %#v, err=%v", run, err)
	}
}
