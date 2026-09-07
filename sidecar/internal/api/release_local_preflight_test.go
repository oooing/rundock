package api

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/publisher"
	"github.com/launcher-sidecar/internal/store"
)

func TestReleasePreflightAPIDefaultIsLocalEvenWhenRemoteIsUnavailable(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", output, err)
		}
	}
	git("init", "-b", "main")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"name":"fixture","version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "initial")
	git("remote", "add", "origin", filepath.Join(t.TempDir(), "unavailable.git"))
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateApp(&store.App{ID: "fixture", Name: "fixture", Cwd: repo, EntryScript: filepath.Join(repo, "start.bat"), AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	router := New(st, logbus.NewHub(), adapter.NewRegistry()).Router()
	for _, query := range []string{"", "?remote=false", "?remote=true"} {
		res := requestAPI(t, router, http.MethodPost, "/api/apps/fixture/release/preflight"+query, nil)
		var pf publisher.Preflight
		if err := json.Unmarshal(res.Body.Bytes(), &pf); err != nil || res.Code != http.StatusOK {
			t.Fatalf("preflight: %s %v", res.Body.String(), err)
		}
		if query == "?remote=true" {
			if !pf.RemoteChecked || len(pf.BlockingIssues) == 0 {
				t.Fatalf("explicit remote query was ignored: %+v", pf)
			}
		} else if pf.RemoteChecked || !pf.CanRelease {
			t.Fatalf("default still blocks on unavailable remote: %+v", pf)
		}
	}
}
