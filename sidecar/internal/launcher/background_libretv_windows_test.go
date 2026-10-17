//go:build windows

package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/store"
)

func TestLiveLibreTVOriginalBackgroundScript(t *testing.T) {
	root := os.Getenv("RUNDOCK_LIBRETV_ROOT")
	if root == "" {
		t.Skip("opt-in real build acceptance")
	}
	source, err := os.ReadFile(filepath.Join(root, "Start-Local.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	// Keep the original launch/detach/readiness behavior. Isolate only port and
	// log location, and reuse the real production build without copying secrets.
	text := string(source)
	for from, to := range map[string]string{
		"$taskRoot = $PSScriptRoot":                      "$taskRoot = '" + strings.ReplaceAll(root, "'", "''") + "'",
		"$taskRuntimeDir = Join-Path $taskRoot '.local'": "$taskRuntimeDir = '" + strings.ReplaceAll(dir, "'", "''") + "'",
	} {
		if strings.Count(text, from) != 1 {
			t.Fatal("original script changed; review fixture adaptation")
		}
		text = strings.Replace(text, from, to, 1)
	}
	text = strings.ReplaceAll(text, "8080", fmt.Sprint(port))
	ps := filepath.Join(dir, "original-start.ps1")
	if err = os.WriteFile(ps, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "start.cmd")
	if err = os.WriteFile(script, []byte(fmt.Sprintf("@echo off\r\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File \"%s\" -NoBrowser\r\nexit /b %%errorlevel%%\r\n", ps)), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetSetting("grace_period_seconds", "1")
	if err = db.CreateApp(&store.App{ID: "libretv-test", Name: "Isolated LibreTV", EntryScript: script, Cwd: root, AdapterType: "batch", PortHints: []int{port}}); err != nil {
		t.Fatal(err)
	}
	reg := adapter.NewRegistry()
	reg.Register(adapter.BatchAdapter{})
	l := New(db, app.NewManager(db), nil, reg)
	defer l.Stop("libretv-test")
	if err = l.Start(context.Background(), "libretv-test"); err != nil {
		t.Fatal(err)
	}
	rt, _ := l.Manager.Registry.Get("libretv-test")
	waitBackground(t, 45*time.Second, func() bool { return isProcessGone(rt.RootPID) && rt.GetStatus() == app.StatusRunning })
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", port))
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Version string `json:"version"`
	}
	err = json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	if err != nil || status.Version == "" {
		t.Fatal("version endpoint failed", err)
	}
	members, _, err := ownedRunProcesses(rt)
	if err != nil || len(members) == 0 {
		t.Fatal("missing background members", err)
	}
	if err = l.Stop("libretv-test"); err != nil {
		t.Fatal(err)
	}
	for pid, created := range members {
		if !processIdentityGone(pid, created) {
			t.Errorf("test child still alive: %d", pid)
		}
	}
	// The user's original instance must remain reachable on 8080.
	real, err := client.Get("http://127.0.0.1:8080/api/status")
	if err != nil {
		t.Fatal("existing LibreTV unavailable", err)
	}
	real.Body.Close()
	if real.StatusCode != 200 {
		t.Fatal(real.Status)
	}
	t.Logf("Original LibreTV background launch v%s on isolated port %d: entry exited, run stayed running, owned children stopped; existing 8080 untouched", status.Version, port)
}
