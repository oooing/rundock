package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirectoryIsolation(t *testing.T) {
	base := t.TempDir()
	t.Setenv("APPDATA", base)
	t.Setenv("LAUNCHER_DATA_DIR", "")
	production := Default()
	if production.DataDir != filepath.Join(base, "launcher-sidecar") {
		t.Fatal(production)
	}
	dev := filepath.Join(base, "launcher-sidecar-dev")
	t.Setenv("LAUNCHER_DATA_DIR", dev)
	t.Setenv("LAUNCHER_PORT", "17655")
	cfg := Default()
	if cfg.DataDir != dev || cfg.DBPath != filepath.Join(dev, "launcher.db") || cfg.LogsDir != filepath.Join(dev, "logs") || cfg.HTTPAddr != "127.0.0.1:17655" {
		t.Fatal(cfg)
	}
	if cfg.DBPath == production.DBPath {
		t.Fatal("shared database")
	}
	if _, err := os.Stat(dev); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitDataDirectoryFailureDoesNotFallBack(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAUNCHER_DATA_DIR", filepath.Join(file, "data"))
	defer func() {
		if recover() == nil {
			t.Fatal("must fail, not fall back to shared data")
		}
	}()
	Default()
}
