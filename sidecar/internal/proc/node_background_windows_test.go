//go:build windows

package proc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestManagedConsoleProbeHelper(t *testing.T) {
	file := os.Getenv("RUNDOCK_CONSOLE_PROBE")
	if file == "" {
		t.Skip("subprocess only")
	}
	hwnd, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	visible, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("IsWindowVisible").Call(hwnd)
	b, _ := json.Marshal(map[string]interface{}{"pid": os.Getpid(), "hwnd": hwnd, "visible": visible, "preload": os.Getenv("USER_PRELOAD")})
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
}

func TestManagedHiddenNodeGrandchildKeepsConsoleAfterEntryExit(t *testing.T) {
	node, err := exec.LookPath("node.exe")
	if err != nil {
		t.Skip("Node unavailable")
	}
	t.Log("node", node)
	dir := t.TempDir()
	file := filepath.Join(dir, "probe.json")
	exeJSON, _ := json.Marshal(os.Args[0])
	// Mirror start-preview -> detached Wrangler -> normal workerd. The grandchild
	// intentionally does not request windowsHide, as in the real dependency.
	worker := `console.log('worker',process.pid,process.cwd(),process.env.RUNDOCK_CONSOLE_PROBE);require('node:child_process').spawn(` + string(exeJSON) + `,['-test.run=^TestManagedConsoleProbeHelper$','-test.v'],{stdio:'inherit'}).on('error',console.error);`
	if err := os.WriteFile(filepath.Join(dir, "worker.cjs"), []byte(worker), 0600); err != nil {
		t.Fatal(err)
	}
	entry := `const log=require('node:fs').openSync('worker.log','a');const child=require('node:child_process').spawn(process.execPath,['worker.cjs'],{detached:true,windowsHide:true,stdio:['ignore',log,log]});child.unref();`
	if err := os.WriteFile(filepath.Join(dir, "entry.cjs"), []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user.cjs"), []byte(`process.env.USER_PRELOAD='preserved';`), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := StartWithConPTY(context.Background(), &PreparedCommand{Cmd: node, Args: []string{"entry.cjs"}, Cwd: dir, Env: map[string]string{"RUNDOCK_CONSOLE_PROBE": file, "NODE_OPTIONS": "--require ./user.cjs"}}, func(line string) { t.Log(line) })
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	defer func() {
		b, err := os.ReadFile(filepath.Join(dir, "worker.log"))
		t.Log(string(b), err)
		members, _, _ := h.Members()
		t.Log("members", members)
	}()
	done := make(chan error, 1)
	go func() { _, err := h.Wait(); done <- err }()
	var probe struct {
		PID     int     `json:"pid"`
		HWND    uintptr `json:"hwnd"`
		Visible int     `json:"visible"`
		Preload string  `json:"preload"`
	}
	if err := json.Unmarshal(waitFixtureFile(t, file), &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Visible != 0 {
		t.Fatalf("grandchild must not expose a console: %+v", probe)
	}
	if probe.Preload != "preserved" {
		t.Fatal("user NODE_OPTIONS lost")
	}
	deadline := time.Now().Add(5 * time.Second)
	for (&Handle{rootPID: h.PID()}).IdentityCreated() != "" {
		if time.Now().After(deadline) {
			t.Fatal("entry did not exit")
		}
		time.Sleep(50 * time.Millisecond)
	}
	members, _, err := h.Members()
	if err != nil || members[probe.PID] == 0 {
		t.Fatal("background worker ownership lost", err)
	}
	select {
	case <-done:
		t.Fatal("entry exit stopped the run")
	default:
	}
	if err := h.Terminate(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not reclaim background worker")
	}
	t.Log("entry exited; hidden grandchild alive in Job; user preload preserved; Stop reclaimed it")
}

func TestManagedNodeEnvironmentCleanupAndScope(t *testing.T) {
	env, cleanup, err := managedNodeEnvironment([]string{"Path=C:/example", "node_options=--trace-warnings"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(env) != 2 || env[0] != "Path=C:/example" || !strings.HasPrefix(env[1], "NODE_OPTIONS=--trace-warnings --require=") {
		t.Fatal(env)
	}
	file, err := strconv.Unquote(strings.SplitN(env[1], "--require=", 2)[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("preload file not cleaned", err)
	}
}
