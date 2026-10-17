//go:build windows

package proc

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func backgroundFixture(t *testing.T, code int) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Skip("Node fixture unavailable")
	}
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker.js")
	if err = os.WriteFile(worker, []byte(`require('fs').writeFileSync('worker.pid',String(process.pid));setInterval(()=>{},1000);setTimeout(()=>process.exit(),60000);`), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "start.cmd")
	if err = os.WriteFile(script, []byte(fmt.Sprintf("@echo off\r\nstart \"\" /b \"%s\" \"%s\" >worker.log 2>&1\r\nexit /b %d\r\n", node, worker, code)), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, script
}

func waitFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("fixture file missing: " + path)
	return nil
}

func TestBackgroundGroupCancellationAfterRootExit(t *testing.T) {
	dir, script := backgroundFixture(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := StartWithConPTY(ctx, &PreparedCommand{Cmd: os.Getenv("ComSpec"), Args: []string{"/d", "/c", "call", script}, Cwd: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	done := make(chan error, 1)
	go func() { _, err := h.Wait(); done <- err }()
	pid, _ := strconv.Atoi(string(waitFixtureFile(t, filepath.Join(dir, "worker.pid"))))
	members, _, err := h.Members()
	if err != nil || members[pid] == 0 {
		t.Fatal("missing member", members, err)
	}
	select {
	case <-done:
		t.Fatal("root exit ended the run")
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop background group")
	}
	active, err := h.group.Active()
	if err != nil || active {
		t.Fatal(active, err)
	}
}

func TestFailedBackgroundRootKeepsFailureAndCleansChildren(t *testing.T) {
	dir, script := backgroundFixture(t, 7)
	h, err := StartWithConPTY(context.Background(), &PreparedCommand{Cmd: os.Getenv("ComSpec"), Args: []string{"/d", "/c", "call", script}, Cwd: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	code, err := h.Wait()
	if err != nil || code != 7 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if err = h.Terminate(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		active, err := h.group.Active()
		if err != nil {
			t.Fatal(err)
		}
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed root leaked workers")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Subprocess deliberately exits without Close to model a backend crash.
func TestBackgroundOwnerCrashHelper(t *testing.T) {
	dir := os.Getenv("RUNDOCK_GROUP_CRASH_FIXTURE")
	if dir == "" {
		t.Skip("subprocess only")
	}
	h, err := StartWithConPTY(context.Background(), &PreparedCommand{Cmd: os.Getenv("ComSpec"), Args: []string{"/d", "/c", "call", filepath.Join(dir, "start.cmd")}, Cwd: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	go h.Wait()
	waitFixtureFile(t, filepath.Join(dir, "crash-now"))
	os.Exit(17)
}

func TestBackgroundGroupReclaimedOnOwnerCrash(t *testing.T) {
	dir, _ := backgroundFixture(t, 0)
	// Independent listener must survive the owner's crash.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBackgroundOwnerCrashHelper$")
	cmd.Env = append(os.Environ(), "RUNDOCK_GROUP_CRASH_FIXTURE="+dir)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	pid, _ := strconv.Atoi(string(waitFixtureFile(t, filepath.Join(dir, "worker.pid"))))
	identity := (&Handle{rootPID: pid}).IdentityCreated()
	if identity == "" {
		t.Fatal("fixture not alive")
	}
	if err = os.WriteFile(filepath.Join(dir, "crash-now"), []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("owner did not simulate crash")
	}
	deadline := time.Now().Add(5 * time.Second)
	for (&Handle{rootPID: pid}).IdentityCreated() == identity {
		if time.Now().After(deadline) {
			t.Fatal("crash leaked worker")
		}
		time.Sleep(50 * time.Millisecond)
	}
	c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal("unrelated listener affected", err)
	}
	c.Close()
}
