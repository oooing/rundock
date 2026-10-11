package portoverride

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func fixture(t *testing.T, command string, definition *Definition) *store.App {
	t.Helper()
	root := t.TempDir()
	data, _ := json.Marshal(map[string]any{"scripts": map[string]string{"dev": command}, "rundock": map[string]any{"temporaryPort": definition}})
	entry := filepath.Join(root, "package.json")
	if err := os.WriteFile(entry, data, 0600); err != nil {
		t.Fatal(err)
	}
	return &store.App{ID: "fixture", Cwd: root, EntryScript: entry, AdapterType: "npm", Cmd: "npm.cmd", Args: []string{"run", "dev"}, Env: map[string]string{"KEEP": "yes"}, PortHints: []int{3000}, HealthURL: "http://127.0.0.1:3000/health?full=1"}
}

func definition() *Definition {
	return &Definition{Version: 1, Adapter: "vite", Script: "dev", Port: 3000, Host: "127.0.0.1", Interfaces: "same-origin"}
}

func TestLifecycleAndWorkspaceEntriesAreNotSingleService(t *testing.T) {
	for _, field := range []string{"predev", "postdev", "workspaces"} {
		t.Run(field, func(t *testing.T) {
			a := fixture(t, "vite", definition())
			raw, _ := os.ReadFile(a.EntryScript)
			var pkg map[string]any
			if err := json.Unmarshal(raw, &pkg); err != nil {
				t.Fatal(err)
			}
			if field == "workspaces" {
				pkg[field] = []string{"apps/*"}
			} else {
				pkg["scripts"].(map[string]any)[field] = "node other-service.js"
			}
			raw, _ = json.Marshal(pkg)
			if err := os.WriteFile(a.EntryScript, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Inspect(a); err == nil {
				t.Fatal("multi-step entry accepted", field)
			}
		})
	}
}

func TestApplyRunOnlyPortPreservesOriginalConfigurationAndFile(t *testing.T) {
	a := fixture(t, "vite --config vite.config.ts", definition())
	before, _ := json.Marshal(a)
	fileBefore, _ := os.ReadFile(a.EntryScript)
	d, err := Inspect(a)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Apply(a, d, 45678)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Args, []string{"run", "dev", "--", "--port", "45678", "--host", "127.0.0.1", "--strictPort"}) {
		t.Fatal(next.Args)
	}
	if next.HealthURL != "http://127.0.0.1:45678/health?full=1" || next.TemporaryPort.OriginalPort != 3000 || next.TemporaryPort.Port != 45678 {
		t.Fatal(next)
	}
	next.Env["KEEP"] = "changed"
	next.PortHints[0] = 1
	next.Args[0] = "changed"
	after, _ := json.Marshal(a)
	fileAfter, _ := os.ReadFile(a.EntryScript)
	if string(before) != string(after) || string(fileBefore) != string(fileAfter) {
		t.Fatal("original configuration was mutated")
	}
	if a.TemporaryPort != nil {
		t.Fatal("override leaked into original app")
	}
}

func TestOnlyExplicitlyCompatibleDirectViteEntriesAreSupported(t *testing.T) {
	for _, command := range []string{"vite", "vite --config apps/web-dev/vite.config.ts", "vite --strictPort"} {
		if _, err := Inspect(fixture(t, command, definition())); err != nil {
			t.Fatal(command, err)
		}
	}
	for _, command := range []string{"wxt", "npm run dev --workspace extension", "vite && node api.js", "vite; echo yes", "vite --port 3000", "vite --host 0.0.0.0", "vite preview", "node server.js"} {
		if _, err := Inspect(fixture(t, command, definition())); err == nil {
			t.Fatal("unsafe command accepted", command)
		}
	}
	if _, err := Inspect(fixture(t, "vite", nil)); err == nil || !strings.Contains(err.Error(), "未声明") {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Definition){func(d *Definition) { d.Host = "0.0.0.0" }, func(d *Definition) { d.Interfaces = "unknown" }, func(d *Definition) { d.Script = "other" }, func(d *Definition) { d.Port = 0 }, func(d *Definition) { d.Version = 2 }} {
		d := definition()
		mutate(d)
		if _, err := Inspect(fixture(t, "vite", d)); err == nil {
			t.Fatal("incompatible declaration accepted", d)
		}
	}
	a := fixture(t, "vite", definition())
	a.HealthURL = "http://127.0.0.1:8000/health"
	if _, err := Inspect(a); err == nil {
		t.Fatal("independent backend health port accepted")
	}
	a.HealthURL = ""
	a.Args = []string{"run", "dev", "--workspace", "web"}
	if _, err := Inspect(a); err == nil {
		t.Fatal("workspace wrapper accepted")
	}
}
