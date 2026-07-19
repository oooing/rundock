package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeclaredRoles(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "start.bat")
	child := filepath.Join(dir, "run-local.ps1")
	if err := os.WriteFile(entry, []byte("set \"FRONTEND_PORT=1111\"\r\nset \"BACKEND_PORT=8765\"\r\necho Frontend dev server on port 5555\r\necho Backend running at http://localhost:8009\r\npowershell -File \"%~dp0run-local.ps1\"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("$frontendUrl = \"http://127.0.0.1:2222\"\n$backendUrl = \"http://127.0.0.1:8001\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := DeclaredRoles(entry)
	for port, want := range map[int]Role{1111: RoleFrontend, 8765: RoleBackend, 5555: RoleFrontend, 8009: RoleBackend, 2222: RoleFrontend, 8001: RoleBackend} {
		if got[port] != want {
			t.Errorf("port %d = %q, want %q", port, got[port], want)
		}
	}
}
