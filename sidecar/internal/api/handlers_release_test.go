package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/publisher"
	"github.com/launcher-sidecar/internal/store"
)

func TestReleaseCreateRequestExternalConfirmationWireContract(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/apps/app1/releases", bytes.NewBufferString(`{
  "selectedTargets":[{"targetId":"web","publish":true}],
  "pushRemote":false,
  "externalActionsConfirmed":true,
  "releaseNotes":"## 修复\n- 修复问题",
  "releaseNotesConfirmed":true
}`))
	var body publisher.CreateRequest
	if err := readJSON(req, &body); err != nil {
		t.Fatal(err)
	}
	if !body.ExternalActionsConfirmed || !body.ReleaseNotesConfirmed || body.ReleaseNotes == "" || body.PushRemote == nil || *body.PushRemote || len(body.SelectedTargets) != 1 || !body.SelectedTargets[0].Publish {
		t.Fatalf("decoded request = %+v", body)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/api/releases/run1/retry", bytes.NewBufferString(`{"externalActionsConfirmed":true}`))
	var retryBody publisher.RetryRequest
	if err := readJSON(retryReq, &retryBody); err != nil || !retryBody.ExternalActionsConfirmed {
		t.Fatalf("decoded retry request = %+v, err=%v", retryBody, err)
	}
	res := httptest.NewRecorder()
	writePublisherError(res, &publisher.Error{Code: "external_actions_confirmation_required", Message: "confirm"})
	if res.Code != http.StatusBadRequest || !bytes.Contains(res.Body.Bytes(), []byte(`"code":"external_actions_confirmation_required"`)) {
		t.Fatalf("publisher error response: %d %s", res.Code, res.Body.String())
	}
}

func TestReleaseProfileHistoryAndExportAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app := &store.App{ID: "app1", Name: "demo", EntryScript: `C:\demo\start.bat`, Cwd: `C:\demo`,
		AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}
	if err := st.CreateApp(app); err != nil {
		t.Fatal(err)
	}
	server := New(st, logbus.NewHub(), adapter.NewRegistry())
	router := server.Router()

	profileBody := []byte(`{"remoteName":"upstream","versionStrategy":"node","preReleaseCommand":"npm test"}`)
	res := requestAPI(t, router, http.MethodPatch, "/api/apps/app1/release-profile", profileBody)
	if res.Code != http.StatusOK {
		t.Fatalf("save profile: %d %s", res.Code, res.Body.String())
	}
	res = requestAPI(t, router, http.MethodGet, "/api/apps/app1/release-profile", nil)
	var profile store.ReleaseProfile
	if err := json.Unmarshal(res.Body.Bytes(), &profile); err != nil || profile.RemoteName != "upstream" || profile.VersionStrategy != "node" {
		t.Fatalf("profile response: %v %+v", err, profile)
	}
	res = requestAPI(t, router, http.MethodPatch, "/api/apps/app1/release-profile", []byte(`{"versionStrategy":"unknown"}`))
	if res.Code != http.StatusBadRequest || !bytes.Contains(res.Body.Bytes(), []byte(`"code":"invalid_strategy"`)) {
		t.Fatalf("invalid strategy response: %d %s", res.Code, res.Body.String())
	}

	run := &store.ReleaseRun{ID: "run1", AppID: "app1", RepoRoot: `C:\demo`, Branch: "main", RemoteName: "upstream",
		TargetVersion: "2.0.0", TagName: "v2.0.0", Status: "failed", Stage: "pushing_tag", CommitSHA: "abc"}
	if err := st.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	firstLogID, _ := st.AddReleaseLog("run1", "event", "pushing tag")
	_, _ = st.AddReleaseLog("run1", "error", "push failed")
	res = requestAPI(t, router, http.MethodGet, "/api/apps/app1/releases?limit=10", nil)
	var runs []*store.ReleaseRun
	if err := json.Unmarshal(res.Body.Bytes(), &runs); err != nil || len(runs) != 1 || runs[0].TagName != "v2.0.0" {
		t.Fatalf("history response: %v %+v", err, runs)
	}
	res = requestAPI(t, router, http.MethodGet, "/api/releases/run1?sinceLogId=0", nil)
	var view struct {
		Run  *store.ReleaseRun   `json:"run"`
		Logs []*store.ReleaseLog `json:"logs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil || view.Run == nil || len(view.Logs) != 2 {
		t.Fatalf("run response: %v %+v", err, view)
	}
	res = requestAPI(t, router, http.MethodGet, "/api/releases/run1?sinceLogId="+strconv.FormatInt(firstLogID, 10), nil)
	if err := json.Unmarshal(res.Body.Bytes(), &view); err != nil || len(view.Logs) != 1 || view.Logs[0].Text != "push failed" {
		t.Fatalf("incremental logs response: %v %+v", err, view.Logs)
	}
	res = requestAPI(t, router, http.MethodGet, "/api/releases/missing", nil)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing release response: %d %s", res.Code, res.Body.String())
	}
	retryExternal := &store.ReleaseRun{ID: "retry-external", AppID: "app1", RepoRoot: `C:\demo`, Branch: "main", RemoteName: "upstream",
		CreateTag: false, Status: "failed", Stage: "target_deploy", CommitSHA: "abc", ErrorCode: "target_step_failed",
		SelectedTargets: []store.ReleaseTargetSelection{{TargetID: "web", Deploy: true}},
		ExecutionPlan:   json.RawMessage(`{"schemaVersion":1,"pushRemote":false,"versionGroups":[],"targets":[{"id":"web","selection":{"targetId":"web","deploy":true}}]}`)}
	if err := st.CreateReleaseRun(retryExternal); err != nil {
		t.Fatal(err)
	}
	res = requestAPI(t, router, http.MethodPost, "/api/releases/retry-external/retry", []byte(`{}`))
	if res.Code != http.StatusBadRequest || !bytes.Contains(res.Body.Bytes(), []byte(`"code":"external_actions_confirmation_required"`)) {
		t.Fatalf("unconfirmed external retry response: %d %s", res.Code, res.Body.String())
	}

	res = requestAPI(t, router, http.MethodGet, "/api/export", nil)
	var exported map[string]json.RawMessage
	if err := json.Unmarshal(res.Body.Bytes(), &exported); err != nil || exported["releaseProfiles"] == nil {
		t.Fatalf("export response missing releaseProfiles: %v %s", err, res.Body.String())
	}
	// 旧快照不含 releaseProfiles，仍应可导入。
	res = requestAPI(t, router, http.MethodPost, "/api/import-config", []byte(`{"apps":[],"groups":[],"settings":{}}`))
	if res.Code != http.StatusOK {
		t.Fatalf("legacy import: %d %s", res.Code, res.Body.String())
	}
}

func requestAPI(t *testing.T, handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}
