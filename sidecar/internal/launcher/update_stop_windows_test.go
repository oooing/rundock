//go:build windows

package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/proc"
	"github.com/launcher-sidecar/internal/store"
)

// Reproduce a real service refusing Ctrl+C while the update aborts. The same
// process must regain live health monitoring without restarting or killing it.
func TestCancelledUpdateStopResumesServiceMonitoring(t *testing.T) {
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Skip("Node fixture unavailable")
	}
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "untouched") }))
	defer unrelated.Close()
	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	worker := filepath.Join(dir, "worker.js")
	source := fmt.Sprintf(`require('http').createServer((q,r)=>r.end('ok')).listen(%d,'127.0.0.1',()=>console.log('http://127.0.0.1:%d/'));process.on('SIGINT',()=>console.log('refusing graceful stop'));setTimeout(()=>process.exit(0),90000);`, port, port)
	if err = os.WriteFile(worker, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "start.cmd")
	if err = os.WriteFile(entry, []byte("@echo off\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Longer than the 3-second monitor tick, ensuring it observes 'stopping'.
	db.SetSetting("grace_period_seconds", "4")
	if err = db.CreateApp(&store.App{ID: "fixture", Name: "Refuses stop", EntryScript: entry, Cwd: dir, AdapterType: "batch", Cmd: node, Args: []string{worker}, PortHints: []int{port}, HealthURL: fmt.Sprintf("http://127.0.0.1:%d/", port)}); err != nil {
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
	defer func() {
		if t.Failed() {
			logs, _ := db.RecentLogs(rt.RunID, 0, 40)
			for _, line := range logs {
				t.Log(line.Text)
			}
		}
	}()
	waitBackground(t, 20*time.Second, func() bool { return rt.GetStatus() == app.StatusRunning })
	pid, runID := rt.RootPID, rt.RunID
	if err = l.StopForUpdate("fixture"); err == nil {
		t.Fatal("expected cancellation, not a forced kill")
	}
	waitBackground(t, 12*time.Second, func() bool { return rt.GetStatus() == app.StatusRunning })
	if rt.RootPID != pid || rt.RunID != runID || isProcessGone(pid) {
		t.Fatal("existing run was replaced or terminated")
	}
	response, err := http.Get(unrelated.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("unrelated service affected")
	}
}

// Unlike a directly launched Node process, Start-Process creates a separate
// console. Ctrl+C sent to the entry's ConPTY cannot stop these HTTP children.
// Exercise the real batch adapter, retained Job ownership and confirmed stop;
// the external sentinels deliberately use the very same node.exe and worker.
func TestConfirmedUpdateStopsDetachedOwnedJob(t *testing.T) {
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Skip("Node fixture unavailable")
	}
	for _, mode := range []string{"waiting-entry", "exited-entry"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "project with spaces")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			worker := filepath.Join(dir, "http worker.cjs")
			source := `const fs=require('node:fs'), http=require('node:http');
const [port,tag]=process.argv.slice(2);
const identity={pid:process.pid,port:Number(port),tag};
const server=http.createServer((q,r)=>{r.setHeader('Content-Type','application/json');r.end(JSON.stringify(identity))});
server.listen(Number(port),'127.0.0.1',()=>{fs.writeFileSync(tag+'.json',JSON.stringify(identity));console.log('http://127.0.0.1:'+port+'/')});
for(const signal of ['SIGINT','SIGBREAK'])process.on(signal,()=>{fs.appendFileSync(tag+'.signals',signal+'\n');server.close(()=>process.exit(0));server.closeIdleConnections()});
setTimeout(()=>process.exit(0),180000).unref();
`
			if err := os.WriteFile(worker, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
			t.Cleanup(client.CloseIdleConnections)
			ports := map[int]bool{17654: true, 17655: true, 17656: true}
			newPort := func() int {
				for {
					listener, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					port := listener.Addr().(*net.TCPAddr).Port
					listener.Close()
					if !ports[port] {
						ports[port] = true
						return port
					}
				}
			}
			// A real unmanaged HTTP process, outside the target's dedicated Job.
			externalPort := newPort()
			external := exec.Command(node, worker, strconv.Itoa(externalPort), "external-http")
			external.Dir = dir
			external.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
			if err := external.Start(); err != nil {
				t.Fatal(err)
			}
			externalDone := make(chan struct{})
			go func() { _ = external.Wait(); close(externalDone) }()
			t.Cleanup(func() {
				_ = external.Process.Kill() // This exact test-created process handle only.
				select {
				case <-externalDone:
				case <-time.After(5 * time.Second):
					t.Error("external fixture cleanup timed out")
				}
			})
			// A second live HTTP process in a DIFFERENT dedicated Windows Job.
			jobPort := newPort()
			externalJob, err := proc.StartWithConPTY(context.Background(), &proc.PreparedCommand{
				Cmd: node, Args: []string{worker, strconv.Itoa(jobPort), "external-job"}, Cwd: dir,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			jobDone := make(chan struct{})
			go func() { _, _ = externalJob.Wait(); close(jobDone) }()
			t.Cleanup(func() {
				_ = externalJob.Terminate()
				select {
				case <-jobDone:
				case <-time.After(5 * time.Second):
					t.Error("external Job fixture cleanup timed out")
				}
				_ = externalJob.Close()
			})
			sentinels := []updateStopHTTP{
				waitUpdateStopHTTP(t, client, dir, "external-http"),
				waitUpdateStopHTTP(t, client, dir, "external-job"),
			}
			jobMembers, supported, err := externalJob.Members()
			if err != nil || !supported || jobMembers[sentinels[1].PID] == 0 {
				t.Fatalf("external Job ownership unavailable: %v %v %v", jobMembers, supported, err)
			}
			assertSentinels := func() {
				for _, sentinel := range sentinels {
					if err := sentinel.check(client); err != nil || processIdentityGone(sentinel.PID, sentinel.created) {
						t.Fatalf("external service changed during confirmed stop: %+v: %v", sentinel, err)
					}
				}
				members, supported, err := externalJob.Members()
				if err != nil || !supported || members[sentinels[1].PID] != jobMembers[sentinels[1].PID] {
					t.Fatalf("external Job/PID identity changed: %v %v", members, err)
				}
			}

			childPorts := []int{newPort(), newPort()}
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
			ps := filepath.Join(dir, "start hidden.ps1")
			launch := "$ErrorActionPreference='Stop'\r\n"
			for i, port := range childPorts {
				tag := fmt.Sprintf("owned-%d", i)
				arguments := fmt.Sprintf("\"%s\" %d %s", worker, port, tag)
				launch += fmt.Sprintf("Start-Process -FilePath %s -ArgumentList %s -WorkingDirectory %s -WindowStyle Hidden -RedirectStandardOutput %s -RedirectStandardError %s | Out-Null\r\n",
					quote(node), quote(arguments), quote(dir), quote(filepath.Join(dir, tag+".out")), quote(filepath.Join(dir, tag+".err")))
			}
			if err := os.WriteFile(ps, []byte(launch), 0600); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(dir, "start entry.cmd")
			batch := fmt.Sprintf("@echo off\r\npowershell.exe -NoProfile -ExecutionPolicy Bypass -File \"%s\"\r\nif errorlevel 1 exit /b 1\r\necho ready>entry.ready\r\n", ps)
			if mode == "waiting-entry" {
				batch += ":wait\r\nchoice /C RQ /N /M \"Fixture waiting: R or Q\"\r\ngoto wait\r\n"
			} else {
				batch += "exit /b 0\r\n"
			}
			if err := os.WriteFile(entry, []byte(batch), 0600); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(filepath.Join(dir, "fixture.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.CreateApp(&store.App{ID: "confirmed-fixture", Name: "Detached HTTP", EntryScript: entry,
				Cwd: dir, AdapterType: "batch", PortHints: childPorts, HealthURL: fmt.Sprintf("http://127.0.0.1:%d/", childPorts[0])}); err != nil {
				t.Fatal(err)
			}
			registry := adapter.NewRegistry()
			registry.Register(adapter.BatchAdapter{})
			l := New(db, app.NewManager(db), nil, registry)
			if err := l.Start(context.Background(), "confirmed-fixture"); err != nil {
				t.Fatal(err)
			}
			rt, _ := l.Manager.Registry.Get("confirmed-fixture")
			l.mu.Lock()
			run := l.runs["confirmed-fixture"]
			l.mu.Unlock()
			t.Cleanup(func() {
				if t.Failed() {
					logs, _ := db.RecentLogs(rt.RunID, 0, 40)
					for _, line := range logs {
						t.Log(line.Text)
					}
				}
				_ = run.handle.Terminate() // Cleanup is separate from the tested operation.
				select {
				case <-run.exitDone:
				case <-time.After(5 * time.Second):
					t.Error("target fixture cleanup timed out")
				}
			})
			workers := []updateStopHTTP{waitUpdateStopHTTP(t, client, dir, "owned-0"), waitUpdateStopHTTP(t, client, dir, "owned-1")}
			waitBackground(t, 20*time.Second, func() bool {
				_, ready := os.Stat(filepath.Join(dir, "entry.ready"))
				return ready == nil && rt.GetStatus() == app.StatusRunning && (mode != "exited-entry" || isProcessGone(rt.RootPID))
			})
			if mode == "waiting-entry" && isProcessGone(rt.RootPID) {
				t.Fatal("fixture entry exited instead of waiting at choice")
			}
			owned, authoritative, err := ownedRunProcesses(rt)
			if err != nil || !authoritative || len(owned) == 0 {
				t.Fatalf("target dedicated Job not verified: %v %v %v", owned, authoritative, err)
			}
			for _, worker := range workers {
				if owned[worker.PID] != worker.created {
					t.Fatalf("worker not owned by target Job: %+v, %v", worker, owned)
				}
			}
			for _, sentinel := range sentinels {
				if _, found := owned[sentinel.PID]; found {
					t.Fatal("external sentinel accidentally belongs to target Job")
				}
			}
			assertSentinels()
			grace := time.Second
			setting := "1"
			if mode == "waiting-entry" {
				setting, grace = "120", 15*time.Second
			} // Confirm the production 15s cap.
			if err := db.SetSetting("grace_period_seconds", setting); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			stopped := make(chan error, 1)
			go func() { stopped <- l.StopForConfirmedUpdate("confirmed-fixture") }()
			deadline := time.NewTimer(grace + 5*time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(200 * time.Millisecond)
			defer tick.Stop()
			samples := 0
			for stopping := true; stopping; {
				select {
				case err := <-stopped:
					if err != nil {
						t.Fatal(err)
					}
					stopping = false
				case <-tick.C:
					assertSentinels()
					samples++
				case <-deadline.C:
					t.Fatalf("confirmed stop exceeded bound %s (configured grace=%s)", grace+5*time.Second, setting)
				}
			}
			elapsed := time.Since(started)
			if elapsed < grace-200*time.Millisecond || elapsed > grace+5*time.Second {
				t.Fatalf("stop must exercise bounded grace then owned-Job completion: %s, expected grace %s", elapsed, grace)
			}
			for pid, created := range owned {
				if !processIdentityGone(pid, created) {
					t.Errorf("owned process survived confirmed stop: %d", pid)
				}
			}
			for _, worker := range workers {
				response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", worker.Port))
				if err == nil {
					response.Body.Close()
					t.Errorf("owned HTTP port remains open: %d", worker.Port)
				}
			}
			latest, err := db.GetLatestRun("confirmed-fixture")
			if err != nil || latest == nil || latest.Status != app.StatusStopped {
				t.Fatalf("run not stopped: %+v, %v", latest, err)
			}
			assertSentinels()
			t.Logf("%s: confirmed stop=%s, owned members=%d, children=%v; external HTTP PID=%d and separate Job PID=%d unchanged across %d samples", mode, elapsed, len(owned), childPorts, sentinels[0].PID, sentinels[1].PID, samples)
		})
	}
}

type updateStopHTTP struct {
	PID     int    `json:"pid"`
	Port    int    `json:"port"`
	Tag     string `json:"tag"`
	created uint64
}

func (s updateStopHTTP) check(client *http.Client) error {
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", s.Port))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var actual updateStopHTTP
	if err := json.NewDecoder(response.Body).Decode(&actual); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || actual.PID != s.PID || actual.Tag != s.Tag || actual.Port != s.Port {
		return fmt.Errorf("HTTP identity mismatch: status=%d actual=%+v expected=%+v", response.StatusCode, actual, s)
	}
	return nil
}

func waitUpdateStopHTTP(t *testing.T, client *http.Client, directory, tag string) updateStopHTTP {
	t.Helper()
	var identity updateStopHTTP
	waitBackground(t, 15*time.Second, func() bool {
		bytes, err := os.ReadFile(filepath.Join(directory, tag+".json"))
		if err != nil || json.Unmarshal(bytes, &identity) != nil || identity.PID <= 0 || identity.Tag != tag {
			return false
		}
		identity.created = processCreation(identity.PID)
		return identity.created != 0 && identity.check(client) == nil
	})
	return identity
}
