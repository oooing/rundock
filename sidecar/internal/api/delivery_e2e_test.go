package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/publisher"
	"github.com/launcher-sidecar/internal/store"
)

// Real HTTP router, SQLite, retained artifacts and HTTPS version endpoint.
// No live GitHub service or installed RunDock instance is involved.
func TestDeliveryAPIE2E(t *testing.T) {
	if os.Getenv("RUNDOCK_DELIVERY_E2E") != "1" {
		t.Skip("opt-in acceptance")
	}
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := &store.App{ID: "app", Name: "acceptance", Cwd: root, EntryScript: "fixture", AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}
	if err = st.CreateApp(app); err != nil {
		t.Fatal(err)
	}
	server := New(st, nil, adapter.NewRegistry())
	httpServer := httptest.NewServer(server.Router())
	defer httpServer.Close()
	var version atomic.Value
	version.Store("0.9.0")
	versionServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"version": version.Load().(string)}})
	}))
	defer versionServer.Close()
	previousTransport := http.DefaultTransport
	http.DefaultTransport = versionServer.Client().Transport
	defer func() { http.DefaultTransport = previousTransport }()
	run := &store.ReleaseRun{ID: "delivery-api", AppID: app.ID, RepoRoot: root, Branch: "main", RemoteName: "origin", TargetVersion: "1.0.0", TagName: "v1.0.0", Status: "succeeded", Stage: "completed", CommitSHA: strings.Repeat("a", 40)}
	if err = st.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "package.zip")
	payload := []byte("retained artifact acceptance")
	if err = os.WriteFile(source, payload, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	engine := delivery.New(st)
	batch := delivery.Batch{RunID: run.ID, AppID: app.ID, GroupID: "product", Repository: "fixture/project", Account: "fixture", Commit: run.CommitSHA, Tag: run.TagName, Version: run.TargetVersion, SyncURL: versionServer.URL, SyncPointer: "/data/version"}
	if err = engine.Seal(context.Background(), batch, []delivery.Source{{TargetID: "desktop", Path: source, SHA256: hex.EncodeToString(sum[:])}}); err != nil {
		t.Fatal(err)
	}
	if err = st.UpdateReleaseDelivery(run.ID, "product", "published", 1, "https://github.com/fixture/project/releases/tag/v1.0.0", "", ""); err != nil {
		t.Fatal(err)
	}
	checks := []string{}
	request := func(method, suffix string, status int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, httpServer.URL+"/api/releases/"+run.ID+suffix, bytes.NewReader([]byte("{}")))
		req.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, suffix, res.StatusCode, raw)
		}
		return raw
	}
	inspect := func(want string) {
		t.Helper()
		var view publisher.RunView
		raw := request("POST", "/sync", 200)
		if e := json.Unmarshal(raw, &view); e != nil {
			t.Fatal(e)
		}
		if view.Run.Status != "succeeded" || len(view.Deliveries) != 1 || view.Deliveries[0].SyncState != want {
			t.Fatalf("sync outcome: %s", raw)
		}
		if bytes.Contains(raw, []byte("syncUrl")) || bytes.Contains(raw, []byte("manifest_json")) {
			t.Fatal("internal manifest leaked")
		}
		checks = append(checks, want)
	}
	inspect("pending")
	version.Store("1.0.0")
	inspect("verified")
	request("GET", "/sync", 405)
	request("GET", "/cancel", 405)
	request("POST", "/cancel", 200)
	checks = append(checks, "cancel completed run is idempotent; mutation methods enforced")
	if err = st.UpdateReleaseRun(run.ID, "running", "delivery_publish", run.CommitSHA, "", "", false); err != nil {
		t.Fatal(err)
	}
	request("POST", "/cancel", 200)
	logs, e := st.ReleaseLogs(run.ID, 0, 100)
	if e != nil || len(logs) == 0 {
		t.Fatalf("cancellation did not record feedback: %v", e)
	}
	batches, e := engine.Load(run.ID)
	if e != nil || len(batches) != 1 {
		t.Fatal("cancellation lost artifacts")
	}
	if e = engine.Verify(context.Background(), batches[0]); e != nil {
		t.Fatal(e)
	}
	checks = append(checks, "cancel requested through HTTP retains verified artifact batch")
	raw, _ := json.MarshalIndent(map[string]any{"passed": true, "boundary": "real HTTP, HTTPS, SQLite and artifact vault; publication seeded", "checks": checks}, "", "  ")
	if dir := os.Getenv("RUNDOCK_DELIVERY_EVIDENCE"); dir != "" {
		if e = os.WriteFile(filepath.Join(dir, "delivery-api-e2e.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
