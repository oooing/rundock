package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

func TestInspectRuntimeIdentityAndPorts(t *testing.T) {
	root := t.TempDir()
	a := &store.App{ID: "app", Cwd: root, EntryScript: filepath.Join(root, "start.cmd"), HealthURL: "http://localhost:3100", PortHints: []int{3100, 8100, 7890}}
	p := Process{PID: 100, Created: "1000", Executable: filepath.Join(root, "server.exe")}
	known := []*store.AppService{{Port: 3100, URL: "http://localhost:3100/library", Role: "frontend", RoleSource: "manual"}, {Port: 8100, Role: "backend"}}
	t.Run("two services with preserved addresses and no invented ownership", func(t *testing.T) {
		got := InspectRuntime(a, known, RuntimeSnapshot{Processes: []Process{p}, Listeners: []probe.PortListener{{PID: 100, Port: 3100}, {PID: 100, Port: 3100}, {PID: 100, Port: 8100}}})
		if got.State != "running" || len(got.Services) != 2 || got.PID != 100 {
			t.Fatalf("%+v", got)
		}
		if got.Services[0].URL != known[0].URL || got.Services[0].RoleSource != "manual" || got.Services[0].AppRunID != "" || got.Services[0].Health != "unknown" {
			t.Fatal(got.Services[0])
		}
		if got.Services[1].URL != "" {
			t.Fatal("TCP socket became an invented HTTP URL")
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*Process)
	}{
		{"unrelated executable", func(p *Process) { p.Executable = filepath.Join(root+"-other", "server.exe") }},
		{"unverified creation time", func(p *Process) { p.Created = "" }},
		{"owner path unreadable", func(p *Process) { p.Executable = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := p
			tc.change(&other)
			got := InspectRuntime(a, known, RuntimeSnapshot{Processes: []Process{other}, Listeners: []probe.PortListener{{PID: 100, Port: 3100}}})
			if got.State != "conflict" || got.PID != 0 || len(got.Services) != 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
	t.Run("validated ancestry only", func(t *testing.T) {
		child := Process{PID: 101, ParentPID: 100, Created: "2000", Executable: filepath.Join(t.TempDir(), "node.exe")}
		for _, created := range []string{"1000", "3000", ""} {
			parent := p
			parent.Created = created
			got := InspectRuntime(a, nil, RuntimeSnapshot{Processes: []Process{parent, child}, Listeners: []probe.PortListener{{PID: 101, Port: 3100}}})
			if (got.State == "running") != (created == "1000") {
				t.Fatalf("parent %s: %+v", created, got)
			}
		}
	})
	t.Run("no port-only or historical PID recovery", func(t *testing.T) {
		got := InspectRuntime(a, known, RuntimeSnapshot{Listeners: []probe.PortListener{{PID: 100, Port: 3100}}})
		if got.State != "conflict" {
			t.Fatalf("%+v", got)
		}
		got = InspectRuntime(a, known, RuntimeSnapshot{})
		if got.State != "clear" || len(got.Services) != 0 {
			t.Fatalf("stopped services leaked: %+v", got)
		}
	})
	t.Run("dual stack listener reports one conflict", func(t *testing.T) {
		got := InspectRuntime(a, nil, RuntimeSnapshot{Listeners: []probe.PortListener{{PID: 999, Port: 3100}, {PID: 999, Port: 3100}}})
		if got.State != "conflict" || len(got.Conflicts) != 1 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("proxy hints do not block but explicit listeners do", func(t *testing.T) {
		fixture := *a
		fixture.EntryScript = filepath.Join(t.TempDir(), "start.cmd")
		if err := os.WriteFile(fixture.EntryScript, []byte("set PROXY_CANDIDATE=http://127.0.0.1:7890\nnode server.js --port 8100\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got := InspectRuntime(&fixture, nil, RuntimeSnapshot{Listeners: []probe.PortListener{{PID: 999, Port: 7890}}})
		if got.State != "clear" {
			t.Fatalf("proxy blocked startup: %+v", got)
		}
		got = InspectRuntime(&fixture, nil, RuntimeSnapshot{Listeners: []probe.PortListener{{PID: 999, Port: 8100}}})
		if got.State != "conflict" {
			t.Fatalf("declared binding ignored: %+v", got)
		}
	})
	t.Run("project executable without listener is not automatically active", func(t *testing.T) {
		got := InspectRuntime(a, nil, RuntimeSnapshot{Processes: []Process{p}})
		if got.State != "clear" {
			t.Fatalf("%+v", got)
		}
		p.Executable = a.EntryScript
		got = InspectRuntime(a, nil, RuntimeSnapshot{Processes: []Process{p}})
		if got.State != "running" {
			t.Fatalf("entry process lost: %+v", got)
		}
	})
}
