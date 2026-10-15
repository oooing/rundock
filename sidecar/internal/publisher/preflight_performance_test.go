package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

type preflightTimingRunner struct{ t *testing.T }

func (r preflightTimingRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	start := time.Now()
	out, err := (execRunner{}).Run(ctx, dir, name, args...)
	r.t.Logf("command %s: %s", strings.Join(args, " "), time.Since(start))
	return out, err
}

// Opt-in read-only measurement against a real checkout. The application and
// profile live in a temporary database; no candidate or release is created.
func TestPreflightPerformanceProbe(t *testing.T) {
	root := os.Getenv("RUNDOCK_PREFLIGHT_PROBE_ROOT")
	if root == "" {
		t.Skip("set RUNDOCK_PREFLIGHT_PROBE_ROOT for a local measurement")
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateApp(&store.App{ID: "probe", Name: "probe", Cwd: root, EntryScript: filepath.Join(root, "start.bat"), AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}}); err != nil {
		t.Fatal(err)
	}
	svc := New(db)
	svc.runner = preflightTimingRunner{t}
	start := time.Now()
	pf, err := svc.PreflightLocal(context.Background(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("preflight: %s; files: %d", time.Since(start), len(pf.Changes))
	start = time.Now()
	fileHashes(pf.RepoRoot, pf.Changes, nil)
	t.Logf("fileHashes: %s", time.Since(start))
	start = time.Now()
	classifyChanges(pf.RepoRoot, pf.Changes, nil, nil)
	t.Logf("classifyChanges: %s", time.Since(start))
}
