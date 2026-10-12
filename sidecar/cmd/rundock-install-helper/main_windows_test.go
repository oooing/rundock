package main

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestLockedFileRollsBackEarlierMoves(t *testing.T) {
	root := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	locked, _ := windows.UTF16PtrFromString(filepath.Join(root, names[1]))
	h, err := windows.CreateFile(locked, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if err := run("prepare", root); err == nil {
		t.Fatal("locked executable must fail preparation")
	}
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("original file was not restored: %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, journalName)); !os.IsNotExist(err) {
		t.Fatal("successful rollback must clear the journal")
	}
}

func TestInstallerRecoveryAndConcurrentGuard(t *testing.T) {
	root := t.TempDir()
	for _, name := range names {
		os.WriteFile(filepath.Join(root, name), []byte("old"), 0600)
	}
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	if err := run("prepare", root); err == nil {
		t.Fatal("overlapping installation must not discard backups")
	}
	if err := run("rollback", root, "another setup"); err == nil {
		t.Fatal("a competing installer must not roll back our staged files")
	}
	for _, name := range names {
		os.WriteFile(filepath.Join(root, name), []byte("partial new"), 0600)
	}
	if err := run("rollback", root); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(raw) != "old" {
			t.Fatal("old installer input was not restored")
		}
	}
	if err := run("prepare", root); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		os.WriteFile(filepath.Join(root, name), []byte("new"), 0600)
	}
	if err := run("commit", root); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("unloaded backup files were not cleaned")
	}
}

func TestRecoveryRejectsPathsOutsideItsTwoExecutables(t *testing.T) {
	root := t.TempDir()
	if err := run("prepare", filepath.VolumeName(root)+string(os.PathSeparator)); err == nil {
		t.Fatal("drive root allowed")
	}
	if err := run("prepare", "relative"); err == nil {
		t.Fatal("relative install directory allowed")
	}
	other := filepath.Join(root, "project.json")
	os.WriteFile(other, []byte("keep"), 0600)
	os.WriteFile(filepath.Join(root, journalName), []byte(`{"Owner":"test","Files":[{"Name":"project.json","Saved":"../project.json"}]}`), 0600)
	if err := run("rollback", root); err == nil {
		t.Fatal("untrusted recovery path allowed")
	}
	raw, _ := os.ReadFile(other)
	if string(raw) != "keep" {
		t.Fatal("project data changed")
	}
}
