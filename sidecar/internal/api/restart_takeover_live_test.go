//go:build windows

package api

import (
	"encoding/json"
	"fmt"
	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/importer"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt-in real-process acceptance; only disposable project files/processes are
// changed. The helper exits so the service genuinely has no living launcher.
func TestExternalTakeoverFixture(t *testing.T) {
	if os.Getenv("RUNDOCK_TAKEOVER_HELPER") == "1" {
		cmd := exec.Command(os.Getenv("RUNDOCK_TAKEOVER_NODE"), os.Getenv("RUNDOCK_TAKEOVER_SCRIPT"))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		cmd.Process.Release()
		os.Exit(0)
	}
	if os.Getenv("RUNDOCK_TAKEOVER_LIVE") != "1" {
		t.Skip("opt-in disposable Windows process acceptance")
	}
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "server.js")
	info := filepath.Join(dir, "server.json")
	source := `const fs=require('fs'),http=require('http');const server=http.createServer((q,r)=>r.end('fixture'));server.listen(0,'127.0.0.1',()=>fs.writeFileSync(__filename.replace('.js','.json'),JSON.stringify({pid:process.pid,port:server.address().port})));`
	if err = os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestExternalTakeoverFixture$")
	helper.Env = append(os.Environ(), "RUNDOCK_TAKEOVER_HELPER=1", "RUNDOCK_TAKEOVER_NODE="+node, "RUNDOCK_TAKEOVER_SCRIPT="+script)
	helper.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if out, e := helper.CombinedOutput(); e != nil {
		t.Fatalf("helper: %v %s", e, out)
	}
	type service struct {
		PID  int `json:"pid"`
		Port int `json:"port"`
	}
	wait := func(file string, old int) service {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			b, _ := os.ReadFile(file)
			var x service
			if json.Unmarshal(b, &x) == nil && x.PID > 0 && x.PID != old {
				client := http.Client{Timeout: time.Second}
				r, e := client.Get(fmt.Sprintf("http://127.0.0.1:%d", x.Port))
				if e == nil {
					r.Body.Close()
					if r.StatusCode == 200 {
						return x
					}
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("service not ready: %s", file)
		return service{}
	}
	first := wait(info, 0)
	// Clean up only identities captured for our own disposable worker.
	var oldProcess recovery.Process
	snapshot, err := recovery.ReadRuntimeSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range snapshot.Processes {
		if p.PID == first.PID {
			oldProcess = p
		}
	}
	if oldProcess.Created == "" {
		t.Fatal("missing initial fixture identity")
	}
	defer func() {
		if recovery.SameProcess(oldProcess) {
			_ = recovery.Terminate(oldProcess)
		}
	}()
	sentinelDir := t.TempDir()
	sentinelScript := filepath.Join(sentinelDir, "sentinel.js")
	os.WriteFile(sentinelScript, []byte(source), 0600)
	sentinel := exec.Command(node, sentinelScript)
	sentinel.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err = sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { sentinel.Process.Kill(); sentinel.Wait() }()
	other := wait(filepath.Join(sentinelDir, "sentinel.json"), 0)
	s := restartTestServer(t)
	s.Registry.Register(adapter.BatchAdapter{})
	entry := filepath.Join(dir, "start.cmd")
	os.WriteFile(entry, []byte(fmt.Sprintf("@echo off\r\n\"%s\" \"%s\"\r\n", node, script)), 0600)
	cand, err := importer.Import(entry, s.Registry)
	if err != nil {
		t.Fatal(err)
	}
	a := &store.App{ID: "takeover-fixture", Name: "takeover-fixture", Cwd: dir, EntryScript: entry, AdapterType: cand.AdapterType, Cmd: cand.Cmd, Args: cand.Args, Env: cand.Env, ScriptHash: cand.ScriptHash, ConfirmedHash: cand.ScriptHash, Confirmed: true}
	if err = s.Store.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	s.runtimeMonitor = &runtimeMonitor{gate: make(chan struct{}, 1), read: s.readProjectRuntimeSnapshot}
	s.Launcher.BeforeStart = s.checkBeforeStart
	defer s.Launcher.Stop(a.ID)
	request := func(endpoint, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/apps/"+a.ID+"/"+endpoint, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Origin", "http://127.0.0.1:17656")
		w := httptest.NewRecorder()
		s.Router().ServeHTTP(w, r)
		return w
	}
	w := request("restart-plan", "{}")
	var plan restartView
	if err = json.Unmarshal(w.Body.Bytes(), &plan); err != nil || !plan.CanRestart || plan.Mode != "detached_worker" {
		t.Fatalf("plan: %d %s %v", w.Code, w.Body.String(), err)
	}
	if !recovery.SameProcess(oldProcess) {
		t.Fatal("planning stopped fixture")
	}
	body, _ := json.Marshal(map[string]string{"confirmationToken": plan.ConfirmationToken})
	w = request("restart-confirm", string(body))
	if w.Code != 200 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	next := wait(info, first.PID)
	if recovery.SameProcess(oldProcess) {
		t.Fatal("old fixture still running")
	}
	if !s.Manager.Registry.IsRunning(a.ID) {
		t.Fatal("replacement was not managed")
	}
	r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", other.Port))
	if err != nil {
		t.Fatal("sentinel interrupted", err)
	}
	r.Body.Close()
	// Consumed tokens cannot perform a second restart.
	if w = request("restart-confirm", string(body)); w.Code != 409 {
		t.Fatal("token reused", w.Code)
	}
	if again := wait(info, 0); again.PID != next.PID {
		t.Fatal("replacement restarted twice")
	}
	t.Logf("PASS: detached PID %d -> managed PID %d; independent PID %d stayed reachable; token replay refused", first.PID, next.PID, other.PID)
}
