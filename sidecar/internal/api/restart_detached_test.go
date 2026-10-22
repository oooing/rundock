//go:build windows

package api

import (
	"errors"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
	"testing"
)

func TestRestartDetachedServiceBoundaries(t *testing.T) {
	a := &store.App{ID: "ours", Cwd: `C:\fixture`, EntryScript: `C:\fixture\package.json`, HealthURL: "http://127.0.0.1:18331"}
	base := []recovery.Process{
		{PID: 100, ParentPID: 90, Created: "1000", Executable: `C:\tools\node.exe`, CommandLine: `node C:\fixture\node_modules\vite\bin\vite.js --config tests/preview.ts --port 18331`},
		{PID: 101, ParentPID: 100, Created: "1100", Executable: `C:\fixture\node_modules\esbuild\esbuild.exe`, CommandLine: `C:\fixture\node_modules\esbuild\esbuild.exe --service`},
	}
	cases := []struct {
		name   string
		mutate func(*recovery.RuntimeSnapshot)
		code   string
	}{
		{"orphan service", nil, ""},
		{"reused parent is not original launcher", func(s *recovery.RuntimeSnapshot) {
			s.Processes = append(s.Processes, recovery.Process{PID: 90, Created: "9999", Executable: `C:\editor.exe`})
		}, ""},
		{"live launcher", func(s *recovery.RuntimeSnapshot) {
			s.Processes = append(s.Processes, recovery.Process{PID: 90, Created: "900", Executable: `C:\Windows\System32\cmd.exe`})
		}, "active_launcher"},
		{"unreadable parent", func(s *recovery.RuntimeSnapshot) {
			s.Processes = append(s.Processes, recovery.Process{PID: 90, Executable: `C:\editor.exe`})
		}, "active_launcher"},
		{"editor is not a service worker", func(s *recovery.RuntimeSnapshot) { s.Processes[0].Executable = `C:\Code.exe` }, "unverified_entry"},
		{"eval may only mention a project", func(s *recovery.RuntimeSnapshot) {
			s.Processes[0].CommandLine = `node -e console.log(1) C:\fixture\server.js`
		}, "unverified_entry"},
		{"outside script with project data", func(s *recovery.RuntimeSnapshot) {
			s.Processes[0].CommandLine = `node C:\tools\shared.js C:\fixture\data.json`
		}, "unverified_entry"},
		{"unknown worker identity", func(s *recovery.RuntimeSnapshot) { s.Processes[1].Created = "" }, "incomplete_identity"},
		{"foreign child", func(s *recovery.RuntimeSnapshot) {
			s.Projects = []*store.App{{ID: "other", Cwd: `C:\other`}}
			s.Processes[1].Executable = `C:\tools\node.exe`
			s.Processes[1].CommandLine = `node C:\other\worker.js`
		}, "mixed_tree"},
		{"unregistered foreign script", func(s *recovery.RuntimeSnapshot) {
			s.Processes[1].Executable = `C:\tools\node.exe`
			s.Processes[1].CommandLine = `node C:\unregistered-project\worker.js`
		}, "shared_child"},
		{"nested project child", func(s *recovery.RuntimeSnapshot) {
			s.Projects = []*store.App{{ID: "other", Cwd: `C:\fixture\nested`}}
			s.Processes[1].Executable = `C:\tools\node.exe`
			s.Processes[1].CommandLine = `node C:\fixture\nested\worker.js`
		}, "mixed_tree"},
		{"another manager owns child", func(s *recovery.RuntimeSnapshot) {
			s.ManagedRoots = []recovery.ProcessOwner{{AppID: "other", PID: 101, Created: "1100"}}
		}, "mixed_tree"},
		{"browser shares the tree", func(s *recovery.RuntimeSnapshot) {
			s.Processes[1].Executable = `C:\Chrome\chrome.exe`
			s.Processes[1].CommandLine = `chrome http://localhost:18331`
		}, "shared_child"},
		{"independent service", func(s *recovery.RuntimeSnapshot) {
			s.Processes = append(s.Processes, recovery.Process{PID: 200, ParentPID: 90, Created: "1200", Executable: `C:\tools\node.exe`, CommandLine: `node C:\fixture\other.js`})
			s.Listeners = append(s.Listeners, probe.PortListener{PID: 200, Port: 18332})
		}, "independent_services"},
		{"port only is insufficient", func(s *recovery.RuntimeSnapshot) { s.Processes[0].CommandLine = `node C:\elsewhere\server.js` }, "unverified_entry"},
		{"launcher itself is protected", func(s *recovery.RuntimeSnapshot) {
			s.Processes = append(s.Processes, recovery.Process{PID: 999, ParentPID: 100, Created: "1200", Executable: `C:\fixture\launcher.exe`})
		}, "incomplete_identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := recovery.RuntimeSnapshot{Processes: append([]recovery.Process{}, base...), Listeners: []probe.PortListener{{PID: 100, Port: 18331}}}
			if tc.mutate != nil {
				tc.mutate(&snap)
			}
			targets, err := restartTree(a, snap, 999)
			if tc.code == "" {
				if err != nil || len(targets) != 2 || targets[0].PID != 100 {
					t.Fatalf("%v %v", targets, err)
				}
				return
			}
			var block *restartBlock
			if !errors.As(err, &block) || block.Code != tc.code || len(block.Steps) == 0 {
				t.Fatalf("want %s, got %#v", tc.code, err)
			}
		})
	}
}
