package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func TestReconcileDeclaredRoles(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "start.bat")
	if err := os.WriteFile(script, []byte("set \"FRONTEND_PORT=2222\"\nset \"BACKEND_PORT=8001\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateApp(&store.App{ID: "app", Name: "app", EntryScript: script}); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []*store.AppService{
		{ID: "auto", AppID: "app", AppRunID: "run", Port: 2222, Role: store.RoleUnknown, RoleSource: store.RoleSourceAuto},
		{ID: "manual", AppID: "app", AppRunID: "run", Port: 8001, Role: store.RoleFrontend, RoleSource: store.RoleSourceManual},
	} {
		if err := s.UpsertService(svc); err != nil {
			t.Fatal(err)
		}
	}
	if err := reconcileDeclaredRoles(s); err != nil {
		t.Fatal(err)
	}
	auto, _ := s.GetService("auto")
	manual, _ := s.GetService("manual")
	if auto.Role != store.RoleFrontend || manual.Role != store.RoleFrontend {
		t.Fatalf("roles after reconcile: auto=%q manual=%q", auto.Role, manual.Role)
	}
}
