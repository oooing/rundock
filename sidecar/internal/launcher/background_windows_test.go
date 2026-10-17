//go:build windows

package launcher

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/store"
)

// Real Windows launches in disposable directories/databases. No user projects
// or fixed service ports are stopped, reused or reconfigured by this test.
func TestBackgroundLaunchSurvivesEntryAndStopsOnlyOwnGroup(t *testing.T) {
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Skip("Node fixture unavailable")
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "unrelated") }))
	defer other.Close()
	for _, mode := range []string{"start-b", "powershell"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := ln.Addr().(*net.TCPAddr).Port
			ln.Close()
			worker := filepath.Join(dir, "worker.js")
			js := fmt.Sprintf(`const http=require('http');const fs=require('fs');setTimeout(()=>{http.createServer((q,r)=>{r.setHeader('Content-Type','text/html');r.end('<html>fixture</html>')}).listen(%d,'127.0.0.1',()=>fs.writeFileSync('worker.pid',String(process.pid)))},700);setTimeout(()=>process.exit(0),120000);`, port)
			if err = os.WriteFile(worker, []byte(js), 0600); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(dir, "start.cmd")
			command := fmt.Sprintf("@echo off\r\nstart \"\" /b \"%s\" \"%s\" >worker.log 2>&1\r\necho %s\r\nexit /b 0\r\n", node, worker, other.URL)
			if mode == "powershell" {
				ps := filepath.Join(dir, "start.ps1")
				content := fmt.Sprintf("$ErrorActionPreference='Stop'\r\nStart-Process -FilePath '%s' -ArgumentList '\"%s\"' -WorkingDirectory '%s' -WindowStyle Hidden -RedirectStandardOutput '%s' -RedirectStandardError '%s'\r\n", strings.ReplaceAll(node, "'", "''"), strings.ReplaceAll(worker, "'", "''"), strings.ReplaceAll(dir, "'", "''"), filepath.Join(dir, "out.log"), filepath.Join(dir, "err.log"))
				if err = os.WriteFile(ps, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				command = fmt.Sprintf("@echo off\r\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File \"%s\"\r\necho %s\r\nexit /b 0\r\n", ps, other.URL)
			}
			if err = os.WriteFile(script, []byte(command), 0600); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(filepath.Join(dir, "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetSetting("grace_period_seconds", "1")
			if err = db.CreateApp(&store.App{ID: "fixture", Name: "Background fixture", EntryScript: script, Cwd: dir, AdapterType: "batch", PortHints: []int{port}}); err != nil {
				t.Fatal(err)
			}
			registry := adapter.NewRegistry()
			registry.Register(adapter.BatchAdapter{})
			l := New(db, app.NewManager(db), nil, registry)
			defer l.Stop("fixture")
			if err = l.Start(context.Background(), "fixture"); err != nil {
				t.Fatal(err)
			}
			rt, _ := l.Manager.Registry.Get("fixture")
			waitBackground(t, 20*time.Second, func() bool { return isProcessGone(rt.RootPID) && rt.GetStatus() == app.StatusRunning })
			pidBytes, err := os.ReadFile(filepath.Join(dir, "worker.pid"))
			if err != nil {
				t.Fatal(err)
			}
			workerPID, _ := strconv.Atoi(string(pidBytes))
			members, authoritative, err := ownedRunProcesses(rt)
			if err != nil || !authoritative || members[workerPID] == 0 {
				t.Fatalf("missing retained ownership: %v %v %v", members, authoritative, err)
			}
			svcs, _ := db.ListServicesByRun(rt.RunID)
			if len(svcs) != 1 || svcs[0].Port != port {
				t.Fatalf("wrong services: %+v", svcs)
			}
			beforeIdentity := members[workerPID]
			if mode == "powershell" {
				oldRunID := rt.RunID
				if err = l.Restart(context.Background(), "fixture"); err != nil {
					t.Fatal(err)
				}
				if !processIdentityGone(workerPID, beforeIdentity) {
					t.Fatal("restart retained the old background worker")
				}
				rt, _ = l.Manager.Registry.Get("fixture")
				waitBackground(t, 20*time.Second, func() bool { return isProcessGone(rt.RootPID) && rt.GetStatus() == app.StatusRunning })
				if rt.RunID == oldRunID {
					t.Fatal("restart did not create a new run")
				}
				pidBytes, err = os.ReadFile(filepath.Join(dir, "worker.pid"))
				if err != nil {
					t.Fatal(err)
				}
				workerPID, _ = strconv.Atoi(string(pidBytes))
				members, _, err = ownedRunProcesses(rt)
				if err != nil || members[workerPID] == 0 {
					t.Fatal("restarted worker ownership missing", err)
				}
				beforeIdentity = members[workerPID]
				t.Log("Restart reclaimed the previous group and discovered the new background service")
			}
			if err = l.Stop("fixture"); err != nil {
				t.Fatal(err)
			}
			if !processIdentityGone(workerPID, beforeIdentity) {
				t.Fatal("background worker survived stop")
			}
			run, _ := db.GetLatestRun("fixture")
			if run.Status != app.StatusStopped {
				t.Fatal(run.Status)
			}
			resp, err := http.Get(other.URL)
			if err != nil {
				t.Fatal("unrelated listener was stopped", err)
			}
			resp.Body.Close()
			t.Logf("%s: entry exited, worker %d retained, port %d ready; Stop reclaimed worker, unrelated service untouched", mode, workerPID, port)
		})
	}
}

func waitBackground(t *testing.T, d time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("background lifecycle did not converge")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
