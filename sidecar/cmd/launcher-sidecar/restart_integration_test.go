//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/security"
	"github.com/launcher-sidecar/internal/store"
)

// Exercises the real HTTP -> graceful exit -> independent supervisor -> new
// worker chain. All files, database and processes belong to this test only.
func TestSelfRestartRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("isolated binary integration")
	}
	root := t.TempDir()
	exe := filepath.Join(root, "sidecar.exe")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	entry := filepath.Join(root, "start.cmd")
	if err := os.WriteFile(entry, []byte("@echo off\r\nexit /b 0\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := security.HashFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	if err = os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(data, "launcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	err = st.CreateApp(&store.App{ID: "self", Name: "Isolated restart fixture", Cwd: root, EntryScript: entry, AdapterType: "batch", ScriptHash: hash, Confirmed: true, ConfirmedHash: hash, LastStatus: "stopped"})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: 20 * time.Second}
	request := func(method, path string, body any) (int, map[string]any, error) {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		r, err := http.NewRequest(method, base+path, bytes.NewReader(data))
		if err != nil {
			return 0, nil, err
		}
		r.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		var result map[string]any
		err = json.NewDecoder(resp.Body).Decode(&result)
		return resp.StatusCode, result, err
	}
	log, err := os.Create(filepath.Join(root, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(exe, "--supervise", "-port", strconv.Itoa(port))
	configureSupervisorChild(cmd)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "LAUNCHER_DATA_DIR="+data)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		request("POST", "/api/desktop/shutdown", nil)
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			cmd.Process.Kill()
			t.Error("isolated supervisor did not exit")
		}
		if t.Failed() {
			log.Seek(0, 0)
			out, _ := io.ReadAll(log)
			t.Log(string(out))
		}
	}()
	waitHealth := func(previous string) string {
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			status, h, e := request("GET", "/api/health", nil)
			if e == nil && status == 200 && h["instanceId"] != previous && h["instanceId"] != "" {
				if id, ok := h["instanceId"].(string); ok {
					return id
				}
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatal("no replacement backend became healthy")
		return ""
	}
	old := waitHealth("")
	status, plan, err := request("POST", "/api/apps/self/restart-plan", nil)
	if err != nil || status != 200 || plan["kind"] != "self" || plan["canRestart"] != true {
		t.Fatalf("plan: %d %v %v", status, plan, err)
	}
	status, result, err := request("POST", "/api/apps/self/restart-confirm", map[string]any{"confirmationToken": plan["confirmationToken"]})
	if err != nil || status != 202 || result["instanceId"] != old {
		t.Fatalf("confirm: %d %v %v", status, result, err)
	}
	newID := waitHealth(old)
	if newID == old {
		t.Fatal("old backend was treated as replacement")
	}
	status, app, err := request("GET", "/api/apps/self", nil)
	if err != nil || status != 200 || app["name"] != "Isolated restart fixture" {
		t.Fatal("configuration was not preserved", status, app, err)
	}
	t.Log(fmt.Sprintf("HTTP confirmation -> supervisor relaunch verified: %s -> %s", old, newID))

	// A dedicated external PowerShell fixture: no real project is touched.
	workerRoot := filepath.Join(root, "worker")
	if err = os.Mkdir(workerRoot, 0700); err != nil {
		t.Fatal(err)
	}
	workerScript := filepath.Join(workerRoot, "start.ps1")
	listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	workerPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	script := fmt.Sprintf("$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, %d)\n$listener.Start()\nwhile ($true) { $client = $listener.AcceptTcpClient(); $stream = $client.GetStream(); $bytes = [Text.Encoding]::ASCII.GetBytes(\"HTTP/1.1 200 OK`r`nContent-Length: 2`r`nConnection: close`r`n`r`nok\"); $stream.Write($bytes,0,$bytes.Length); $client.Close() }\n", workerPort)
	if err = os.WriteFile(workerScript, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	worker := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-NonInteractive", "-File", workerScript)
	worker.Dir = workerRoot
	configureSupervisorChild(worker)
	if err = worker.Start(); err != nil {
		t.Fatal(err)
	}
	workerID := ""
	defer func() {
		if workerID != "" {
			request("POST", "/api/apps/"+workerID+"/stop", nil)
		}
		worker.Process.Kill()
		worker.Wait()
	}()
	workerURL := fmt.Sprintf("http://127.0.0.1:%d", workerPort)
	ready := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		resp, e := client.Get(workerURL)
		if e == nil {
			resp.Body.Close()
			ready = resp.StatusCode == 200
			if ready {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("external fixture not ready")
	}
	workerHash, _ := security.HashFile(workerScript)
	status, created, err := request("POST", "/api/apps", map[string]any{"name": "External fixture", "entryScript": workerScript, "cwd": workerRoot, "adapterType": "ps1", "healthUrl": workerURL, "scriptHash": workerHash})
	if err != nil || status != 201 {
		t.Fatal(status, created, err)
	}
	workerID, _ = created["id"].(string)
	status, plan, err = request("POST", "/api/apps/"+workerID+"/restart-plan", nil)
	if err != nil || status != 200 || plan["kind"] != "external" || plan["canRestart"] != true {
		t.Fatal("external plan", status, plan, err)
	}
	status, result, err = request("POST", "/api/apps/"+workerID+"/restart-confirm", map[string]any{"confirmationToken": plan["confirmationToken"]})
	if err != nil || status != 200 {
		t.Fatal("external confirm", status, result, err)
	}
	status, app, err = request("GET", "/api/apps/"+workerID, nil)
	if err != nil || status != 200 || app["runId"] == "" || app["pid"] == float64(worker.Process.Pid) {
		t.Fatal("external fixture was not restarted and managed", status, app, err)
	}
	status, plan, err = request("POST", "/api/apps/self/restart-plan", nil)
	if err != nil || status != 200 || plan["canRestart"] != false {
		t.Fatal("self restart allowed while fixture is managed", status, plan, err)
	}
	t.Log("External fixture identity confirmed, restarted under management, and protected from self-restart interruption")
}
