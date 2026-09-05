package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestReleaseConfigAPIScanPutAndGet(t *testing.T) {
	router, repo, closeStore := newReleaseConfigAPIFixture(t)
	defer closeStore()
	manifestPath := filepath.Join(repo, filepath.FromSlash(releaseconfig.ManifestPath))

	res := requestAPI(t, router, http.MethodGet, "/api/apps/app1/release-config", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("detected GET: %d %s", res.Code, res.Body.String())
	}
	var detected releaseconfig.Config
	if err := json.Unmarshal(res.Body.Bytes(), &detected); err != nil {
		t.Fatal(err)
	}
	if detected.Source != releaseconfig.SourceDetected || detected.RepoRoot != repo || detected.ConfigPath != releaseconfig.ManifestPath {
		t.Fatalf("detected config metadata = %+v", detected)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("GET discovery wrote a manifest: %v", err)
	}

	want := validReleaseConfig()
	want.Source = "untrusted-client-value"
	want.RepoRoot = `C:\must-not-be-used`
	want.ConfigPath = "outside.yaml"
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	res = requestAPI(t, router, http.MethodPut, "/api/apps/app1/release-config", body)
	if res.Code != http.StatusOK {
		t.Fatalf("PUT config: %d %s", res.Code, res.Body.String())
	}
	var saved releaseconfig.Config
	if err := json.Unmarshal(res.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Source != releaseconfig.SourceFile || saved.RepoRoot != repo || saved.ConfigPath != releaseconfig.ManifestPath {
		t.Fatalf("saved config metadata = %+v", saved)
	}
	rawBeforeScan, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"source"`, `"repoRoot"`, `"configPath"`, `C:\\must-not-be-used`, `outside.yaml`} {
		if bytes.Contains(rawBeforeScan, []byte(forbidden)) {
			t.Fatalf("runtime/client metadata leaked into manifest (%s): %s", forbidden, rawBeforeScan)
		}
	}

	res = requestAPI(t, router, http.MethodGet, "/api/apps/app1/release-config", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("saved GET: %d %s", res.Code, res.Body.String())
	}
	var loaded releaseconfig.Config
	if err := json.Unmarshal(res.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Source != releaseconfig.SourceFile || len(loaded.Targets) != 1 || loaded.Targets[0].ID != "web" || loaded.Targets[0].Steps.Build != "npm run build" {
		t.Fatalf("loaded config = %+v", loaded)
	}

	// A fresh scan is a proposal only: it must ignore and preserve the saved file.
	res = requestAPI(t, router, http.MethodPost, "/api/apps/app1/release-config/scan", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("POST scan: %d %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &detected); err != nil || detected.Source != releaseconfig.SourceDetected {
		t.Fatalf("scan response: err=%v config=%+v", err, detected)
	}
	rawAfterScan, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(rawBeforeScan, rawAfterScan) {
		t.Fatalf("scan modified manifest: err=%v before=%s after=%s", err, rawBeforeScan, rawAfterScan)
	}
}

func TestReleaseConfigAPIRejectsInvalidManifestsWithoutOverwriting(t *testing.T) {
	router, repo, closeStore := newReleaseConfigAPIFixture(t)
	defer closeStore()
	manifestPath := filepath.Join(repo, filepath.FromSlash(releaseconfig.ManifestPath))

	valid := validReleaseConfig()
	body, _ := json.Marshal(valid)
	res := requestAPI(t, router, http.MethodPut, "/api/apps/app1/release-config", body)
	if res.Code != http.StatusOK {
		t.Fatalf("seed config: %d %s", res.Code, res.Body.String())
	}
	wantFile, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*releaseconfig.Config)
	}{
		{name: "unsupported schema", mutate: func(c *releaseconfig.Config) { c.SchemaVersion = 2 }},
		{name: "blank version file", mutate: func(c *releaseconfig.Config) { c.VersionGroups[0].VersionFiles[0].Path = "" }},
		{name: "unsupported version format", mutate: func(c *releaseconfig.Config) { c.VersionGroups[0].VersionFiles[0].Format = "regex" }},
		{name: "cargo lock without manifest", mutate: func(c *releaseconfig.Config) {
			c.VersionGroups[0].VersionFiles = []releaseconfig.VersionFile{{Path: "src-tauri/Cargo.lock", Format: "cargo-lock"}}
		}},
		{name: "unsupported json pointer", mutate: func(c *releaseconfig.Config) { c.VersionGroups[0].VersionFiles[0].JSONPointer = "/nested/version" }},
		{name: "version file traversal", mutate: func(c *releaseconfig.Config) { c.VersionGroups[0].VersionFiles[0].Path = "../package.json" }},
		{name: "working directory traversal", mutate: func(c *releaseconfig.Config) { c.Targets[0].WorkingDir = "../outside" }},
		{name: "artifact traversal", mutate: func(c *releaseconfig.Config) { c.Targets[0].Artifacts = []string{"../../secret/**"} }},
		{name: "duplicate version groups", mutate: func(c *releaseconfig.Config) { c.VersionGroups = append(c.VersionGroups, c.VersionGroups[0]) }},
		{name: "duplicate targets", mutate: func(c *releaseconfig.Config) { c.Targets = append(c.Targets, c.Targets[0]) }},
		{name: "missing version group", mutate: func(c *releaseconfig.Config) { c.Targets[0].VersionGroup = "missing" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneReleaseConfig(t, valid)
			tt.mutate(candidate)
			body, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			res := requestAPI(t, router, http.MethodPut, "/api/apps/app1/release-config", body)
			if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"config_invalid"`) {
				t.Fatalf("invalid PUT: %d %s", res.Code, res.Body.String())
			}
			gotFile, err := os.ReadFile(manifestPath)
			if err != nil || !bytes.Equal(gotFile, wantFile) {
				t.Fatalf("invalid PUT overwrote manifest: err=%v before=%s after=%s", err, wantFile, gotFile)
			}
		})
	}
}

func TestReleaseConfigAPIMissingAppAndCORS(t *testing.T) {
	router, _, closeStore := newReleaseConfigAPIFixture(t)
	defer closeStore()
	res := requestAPI(t, router, http.MethodGet, "/api/apps/missing/release-config", nil)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"app_not_found"`) {
		t.Fatalf("missing app: %d %s", res.Code, res.Body.String())
	}
	res = requestAPI(t, router, http.MethodOptions, "/api/apps/app1/release-config", nil)
	if res.Code != http.StatusNoContent || !strings.Contains(res.Header().Get("Access-Control-Allow-Methods"), http.MethodPut) {
		t.Fatalf("CORS preflight: %d headers=%v", res.Code, res.Header())
	}
}

func TestReleaseProfileAPIRemembersOptionalTagChoice(t *testing.T) {
	router, _, closeStore := newReleaseConfigAPIFixture(t)
	defer closeStore()

	res := requestAPI(t, router, http.MethodGet, "/api/apps/app1/release-profile", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("default profile: %d %s", res.Code, res.Body.String())
	}
	var profile store.ReleaseProfile
	if err := json.Unmarshal(res.Body.Bytes(), &profile); err != nil || !profile.CreateTag || profile.VersionMode != "auto" {
		t.Fatalf("default profile = %+v, err=%v", profile, err)
	}

	res = requestAPI(t, router, http.MethodPatch, "/api/apps/app1/release-profile", []byte(`{
  "remoteName": "origin",
  "versionStrategy": "auto",
  "createTag": false,
  "versionMode": "manual"
}`))
	if res.Code != http.StatusOK {
		t.Fatalf("save profile: %d %s", res.Code, res.Body.String())
	}
	res = requestAPI(t, router, http.MethodGet, "/api/apps/app1/release-profile", nil)
	if err := json.Unmarshal(res.Body.Bytes(), &profile); err != nil || profile.CreateTag || profile.VersionMode != "manual" {
		t.Fatalf("remembered profile = %+v, err=%v", profile, err)
	}

	res = requestAPI(t, router, http.MethodPatch, "/api/apps/app1/release-profile", []byte(`{"versionMode":"calendar"}`))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_version_mode"`) {
		t.Fatalf("invalid version mode: %d %s", res.Code, res.Body.String())
	}
}

func validReleaseConfig() *releaseconfig.Config {
	return &releaseconfig.Config{
		SchemaVersion: releaseconfig.SchemaVersion,
		VersionGroups: []releaseconfig.VersionGroup{{
			ID: "product", Name: "产品版本", CurrentVersion: "1.2.3",
			VersionFiles: []releaseconfig.VersionFile{{Path: "package.json", Format: "json", JSONPointer: "/version"}},
		}},
		Targets: []releaseconfig.Target{{
			ID: "web", Name: "Web", Kind: "web", VersionGroup: "product", WorkingDir: ".",
			Runner:  releaseconfig.Runner{Type: "local", OS: []string{"windows", "linux", "darwin"}},
			Enabled: true, Confidence: 1,
			Steps:     releaseconfig.Steps{Check: "npm test", Build: "npm run build", Publish: "npm run publish"},
			Artifacts: []string{"dist/**"},
		}},
		Warnings: []string{},
	}
}

func cloneReleaseConfig(t *testing.T, cfg *releaseconfig.Config) *releaseconfig.Config {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clone := &releaseconfig.Config{}
	if err := json.Unmarshal(raw, clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func newReleaseConfigAPIFixture(t *testing.T) (http.Handler, string, func()) {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{
  "name": "release-config-test",
  "version": "1.2.3",
  "scripts": {"test": "echo ok", "build": "vite build"},
  "devDependencies": {"vite": "latest"}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	app := &store.App{
		ID: "app1", Name: "demo", EntryScript: filepath.Join(repo, "start.bat"), Cwd: repo,
		AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped",
	}
	if err := st.CreateApp(app); err != nil {
		st.Close()
		t.Fatal(err)
	}
	server := New(st, logbus.NewHub(), adapter.NewRegistry())
	return server.Router(), repo, func() { _ = st.Close() }
}
