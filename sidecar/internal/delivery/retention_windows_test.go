//go:build windows

package delivery

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRetentionRejectsWindowsJunction(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "junction")
	keep := filepath.Join(outside, "keep.zip")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, outside)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create isolated junction: %v %s", err, out)
	}
	defer os.Remove(link) // Remove only the directory link, never its target.
	if err := plainRetentionDir(root, link); err == nil {
		t.Fatal("junction accepted")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
		t.Fatal("outside file changed", err)
	}
}
