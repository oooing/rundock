package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func TestReleaseConfigFileReadSaveAndConflict(t *testing.T) {
	router, repo, closeStore := newReleaseConfigAPIFixture(t)
	defer closeStore()
	endpoint := "/api/apps/app1/release-config/file"
	path := filepath.Join(repo, filepath.FromSlash(releaseconfig.ManifestPath))
	get := func() releaseconfig.ConfigFile {
		t.Helper()
		res := requestAPI(t, router, http.MethodGet, endpoint, nil)
		if res.Code != 200 {
			t.Fatalf("read: %d %s", res.Code, res.Body.String())
		}
		var doc struct {
			releaseconfig.ConfigFile
			Example string `json:"example"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Path != path || doc.Example != releaseconfig.ExampleFile {
			t.Fatalf("wrong file/example: %+v", doc)
		}
		return doc.ConfigFile
	}
	put := func(content, revision string, want int) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"content": content, "revision": revision})
		res := requestAPI(t, router, http.MethodPut, endpoint, body)
		if res.Code != want {
			t.Fatalf("save: %d want %d: %s", res.Code, want, res.Body.String())
		}
	}
	draft := get()
	if draft.Exists || draft.Revision != "missing" {
		t.Fatalf("not a missing draft: %+v", draft)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("viewing created the manifest")
	}
	// The downloadable annotated example must be accepted as an actual manifest.
	put(releaseconfig.ExampleFile, draft.Revision, 200)
	saved := get()
	if !saved.Exists || saved.Content != releaseconfig.ExampleFile {
		t.Fatal("comments or content lost")
	}
	res := requestAPI(t, router, http.MethodGet, "/api/apps/app1/release-config", nil)
	if res.Code != 200 {
		t.Fatalf("normal release loader cannot read sample: %s", res.Body.String())
	}
	var cfg releaseconfig.Config
	if err := json.Unmarshal(res.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 2 || cfg.Targets[0].Runner.Type != "git-push" || cfg.Targets[1].Runner.Type != "local" {
		t.Fatal("sample targets incorrect")
	}
	// Invalid syntax/fields must never damage the existing file.
	put("{ invalid", saved.Revision, 400)
	put(strings.Replace(saved.Content, `"versionGroup": "web"`, `"versionGroup": "missing"`, 1), saved.Revision, 400)
	if got := get(); got.Content != saved.Content {
		t.Fatal("invalid save overwrote file")
	}
	// External edits must survive a stale editor's save.
	external := saved.Content + "\n# external edit\n"
	if err := os.WriteFile(path, []byte(external), 0644); err != nil {
		t.Fatal(err)
	}
	put(saved.Content, saved.Revision, 409)
	if get().Content != external {
		t.Fatal("stale save overwrote external edit")
	}
	// Invalid files can still be opened and repaired.
	if err := os.WriteFile(path, []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}
	broken := get()
	if broken.Content != "{broken" {
		t.Fatal("raw read failed")
	}
	put(saved.Content, broken.Revision, 200)
	if get().Content != saved.Content {
		t.Fatal("repair did not persist")
	}
	res = requestAPI(t, router, http.MethodGet, "/api/apps/missing/release-config/file", nil)
	if res.Code != 404 {
		t.Fatalf("unknown project: %d", res.Code)
	}
}
