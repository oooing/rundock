package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// Failure inventory is in the local-web-server-deployment ADR, written before
// implementation. This exercises HTTP, persistent SQLite/vault, process-style
// reconstruction and HTTPS. Only GitHub's external boundary is simulated.
type deploymentRemote struct {
	b          delivery.Batch
	workflow   string
	dispatches atomic.Int32
	stage      atomic.Int32
}

func (r *deploymentRemote) Identity(context.Context) (string, error) { return "fixture", nil }
func (r *deploymentRemote) Upload(context.Context, string, int64, string, string) error {
	return fmt.Errorf("unexpected upload")
}
func (r *deploymentRemote) Digest(context.Context, string) (string, error) {
	return "", fmt.Errorf("unexpected digest")
}
func (r *deploymentRemote) JSON(_ context.Context, method, endpoint string, body, out any) error {
	var value any
	base := "repos/fixture/project"
	if method == "POST" && strings.HasSuffix(endpoint, "/dispatches") {
		payload, _ := json.Marshal(body)
		var request struct {
			Ref    string            `json:"ref"`
			Inputs map[string]string `json:"inputs"`
		}
		_ = json.Unmarshal(payload, &request)
		if request.Ref != r.b.Tag || request.Inputs["release_commit"] != r.b.Commit || request.Inputs["release_run_id"] != r.b.RunID || request.Inputs["target_id"] != r.b.GroupID {
			return fmt.Errorf("wrong frozen dispatch: %s", payload)
		}
		r.dispatches.Add(1)
		return &delivery.HTTPError{Status: 503} // GitHub accepted it; response was lost.
	}
	if method != "GET" {
		return fmt.Errorf("unexpected mutation %s %s", method, endpoint)
	}
	switch {
	case strings.Contains(endpoint, "/actions/workflows/"):
		runs := []map[string]string{}
		if r.stage.Load() > 0 {
			status, conclusion := "in_progress", ""
			if r.stage.Load() == 2 {
				status, conclusion = "completed", "failure"
			}
			if r.stage.Load() == 3 {
				status, conclusion = "completed", "success"
			}
			runs = append(runs, map[string]string{"display_title": "rundock-deploy:" + r.b.RunID + ":" + r.b.GroupID, "head_sha": r.b.Commit, "head_branch": r.b.Tag, "status": status, "conclusion": conclusion, "html_url": "https://github.com/fixture/project/actions/runs/1"})
		}
		value = map[string]any{"workflow_runs": runs}
	case endpoint == base:
		value = map[string]any{"full_name": "fixture/project", "default_branch": "main", "permissions": map[string]bool{"push": true}}
	case strings.Contains(endpoint, "/contents/.github/workflows/deploy.yml"):
		value = map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(r.workflow))}
	case strings.Contains(endpoint, "/contents/.github/workflows"):
		value = []map[string]string{{"name": "deploy.yml", "type": "file"}}
	case strings.Contains(endpoint, "/git/ref/tags/"):
		value = map[string]any{"object": map[string]string{"type": "commit", "sha": r.b.Commit}}
	case strings.Contains(endpoint, "/releases/tags/"):
		value = map[string]any{"id": 1, "tag_name": r.b.Tag, "draft": false, "prerelease": false}
	case strings.Contains(endpoint, "/releases/1/assets"):
		files := []map[string]any{}
		for _, file := range r.b.Files {
			files = append(files, map[string]any{"id": 1, "name": file.Name, "size": file.Size, "state": "uploaded", "digest": "sha256:" + file.SHA256})
		}
		value = files
	default:
		return fmt.Errorf("unexpected lookup: %s", endpoint)
	}
	data, _ := json.Marshal(value)
	return json.Unmarshal(data, out)
}

func TestDeploymentAPIE2E(t *testing.T) {
	if os.Getenv("RUNDOCK_DELIVERY_E2E") != "1" {
		t.Skip("opt-in acceptance")
	}
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := &store.App{ID: "app", Name: "deployment", Cwd: root, EntryScript: "fixture", AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}
	if err = st.CreateApp(app); err != nil {
		t.Fatal(err)
	}
	run := &store.ReleaseRun{ID: "deployment-api", AppID: app.ID, RepoRoot: root, Branch: "main", RemoteName: "origin", TargetVersion: "1.0.0", TagName: "web-server/v1.0.0", Status: "succeeded", Stage: "completed", CommitSHA: strings.Repeat("a", 40)}
	if err = st.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	var live atomic.Value
	live.Store("0.9.0")
	versionServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"version": live.Load()})
	}))
	defer versionServer.Close()
	previousTransport := http.DefaultTransport
	http.DefaultTransport = versionServer.Client().Transport
	defer func() { http.DefaultTransport = previousTransport }()
	workflow := "name: deployment\non: workflow_dispatch\njobs: {}\n"
	workflowHash := sha256.Sum256([]byte(workflow))
	b := delivery.Batch{RunID: run.ID, AppID: app.ID, GroupID: "web-server", Repository: "fixture/project", Account: "fixture", Commit: run.CommitSHA, Tag: run.TagName, Version: run.TargetVersion, SyncURL: versionServer.URL, SyncPointer: "/version", DeploymentWorkflow: "deploy.yml", Workflows: map[string]string{"deploy.yml": hex.EncodeToString(workflowHash[:])}}
	source := filepath.Join(root, "web.zip")
	if err = os.WriteFile(source, []byte("sealed local frontend acceptance"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := delivery.New(st)
	if err = engine.Seal(context.Background(), b, []delivery.Source{{TargetID: "web", Path: source}}); err != nil {
		t.Fatal(err)
	}
	batches, err := engine.Load(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	b = batches[0]
	if err = st.UpdateReleaseDelivery(run.ID, b.GroupID, "published", 1, "https://github.com/fixture/project/releases/tag/v1.0.0", "", ""); err != nil {
		t.Fatal(err)
	}
	remote := &deploymentRemote{b: b, workflow: workflow}
	start := func() *httptest.Server {
		server := New(st, nil, adapter.NewRegistry())
		server.Publisher.SetDeliveryClient(remote)
		return httptest.NewServer(server.Router())
	}
	server := start()
	request := func(status int) publisher.RunView {
		t.Helper()
		res, e := http.Post(server.URL+"/api/releases/"+run.ID+"/sync", "application/json", bytes.NewReader([]byte("{}")))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("HTTP %d: %s", res.StatusCode, raw)
		}
		var view publisher.RunView
		if status == 200 {
			if e = json.Unmarshal(raw, &view); e != nil {
				t.Fatal(e)
			}
			if view.Run.Status != "succeeded" || view.Deliveries[0].State != "published" {
				t.Fatal("deployment altered published release")
			}
		}
		return view
	}
	request(500)
	if remote.dispatches.Load() != 1 {
		t.Fatal("expected one automatic dispatch")
	}
	server.Close()
	server = start()
	defer server.Close()
	if request(200).Deliveries[0].SyncState != "deployment_requested" {
		t.Fatal("lost response not retained")
	}
	if remote.dispatches.Load() != 1 {
		t.Fatal("restart duplicated deployment")
	}
	remote.stage.Store(1)
	if request(200).Deliveries[0].SyncState != "deploying" {
		t.Fatal("running state missing")
	}
	remote.stage.Store(2)
	if request(200).Deliveries[0].SyncState != "deployment_failed" {
		t.Fatal("failure hidden")
	}
	remote.stage.Store(3)
	if request(200).Deliveries[0].SyncState != "pending" {
		t.Fatal("image success must not imply server success")
	}
	live.Store("1.0.0")
	if request(200).Deliveries[0].SyncState != "verified" {
		t.Fatal("live version not verified")
	}
	request(200)
	if remote.dispatches.Load() != 1 {
		t.Fatal("reconciliation duplicated dispatch")
	}
	if err = engine.Verify(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"passed": true, "boundary": "real HTTP/HTTPS, SQLite and sealed artifacts; external GitHub simulated; no real release created", "dispatchCount": remote.dispatches.Load(), "checks": []string{"frozen identity dispatched automatically", "lost response survives reconstruction without duplicate", "remote failure remains visible", "image success waits for actual server version", "published files retained", "repeated check is idempotent"}}
	if directory := os.Getenv("RUNDOCK_DELIVERY_EVIDENCE"); directory != "" {
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err = os.WriteFile(filepath.Join(directory, "deployment-api-e2e.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// This independent API path exercises a frozen server-pull delivery, including
// persistent reconstruction; cloud workflow behavior remains covered above.
type noCloudDeployment struct {
	deploymentRemote
	requests atomic.Int32
}

func (r *noCloudDeployment) JSON(context.Context, string, string, any, any) error {
	r.requests.Add(1)
	return fmt.Errorf("server-pull must not call GitHub")
}

func TestServerPullAPIE2E(t *testing.T) {
	if os.Getenv("RUNDOCK_DELIVERY_E2E") != "1" {
		t.Skip("opt-in acceptance")
	}
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := &store.App{ID: "server-pull-app", Name: "server-pull", Cwd: root, EntryScript: "fixture", AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}
	if err = st.CreateApp(app); err != nil {
		t.Fatal(err)
	}
	run := &store.ReleaseRun{ID: "server-pull-api", AppID: app.ID, RepoRoot: root, Branch: "main", RemoteName: "origin", TargetVersion: "1.0.0", TagName: "web-server/v1.0.0", Status: "succeeded", Stage: "completed", CommitSHA: strings.Repeat("b", 40)}
	if err = st.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	var live atomic.Value
	live.Store("0.9.0")
	versionServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"version": live.Load()})
	}))
	defer versionServer.Close()
	previousTransport := http.DefaultTransport
	http.DefaultTransport = versionServer.Client().Transport
	defer func() { http.DefaultTransport = previousTransport }()
	b := delivery.Batch{RunID: run.ID, AppID: app.ID, GroupID: "web-server", Repository: "fixture/project", Account: "fixture", Commit: run.CommitSHA, Tag: run.TagName, Version: run.TargetVersion, DeploymentStrategy: "server-pull", SyncURL: versionServer.URL, SyncPointer: "/version"}
	source := filepath.Join(root, "web.zip")
	if err = os.WriteFile(source, []byte("frozen local artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	engine := delivery.New(st)
	if err = engine.Seal(context.Background(), b, []delivery.Source{{TargetID: "web", Path: source}}); err != nil {
		t.Fatal(err)
	}
	if err = st.UpdateReleaseDelivery(run.ID, b.GroupID, "published", 1, "https://github.com/fixture/project/releases/tag/v1.0.0", "", ""); err != nil {
		t.Fatal(err)
	}
	remote := &noCloudDeployment{}
	check := func(expected string) {
		t.Helper()
		server := New(st, nil, adapter.NewRegistry())
		server.Publisher.SetDeliveryClient(remote)
		httpServer := httptest.NewServer(server.Router())
		defer httpServer.Close()
		res, e := http.Post(httpServer.URL+"/api/releases/"+run.ID+"/sync", "application/json", bytes.NewReader([]byte("{}")))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		var view publisher.RunView
		if e = json.NewDecoder(res.Body).Decode(&view); e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != 200 || len(view.Deliveries) != 1 || view.Deliveries[0].SyncState != expected || view.Deliveries[0].State != "published" {
			t.Fatalf("unexpected server-pull status: %+v", view)
		}
	}
	check("pending")
	live.Store("1.0.0")
	check("verified")
	check("verified")
	if remote.requests.Load() != 0 {
		t.Fatal("local update consumed a GitHub workflow call")
	}
	if directory := os.Getenv("RUNDOCK_DELIVERY_EVIDENCE"); directory != "" {
		raw, _ := json.MarshalIndent(map[string]any{"passed": true, "githubRequests": remote.requests.Load(), "checks": []string{"server-pull remains pending until live version matches", "persistent API reconstruction retains published artifacts", "server-pull never dispatches Actions", "cloud workflow independently covered"}}, "", "  ")
		if err = os.WriteFile(filepath.Join(directory, "server-pull-api-e2e.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
