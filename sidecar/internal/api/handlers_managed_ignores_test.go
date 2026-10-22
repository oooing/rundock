package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/publisher"
)

func TestManagedIgnoreAPI(t *testing.T) {
	router, repo, closeDB := newReleaseConfigAPIFixture(t)
	defer closeDB()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}, {"add", "package.json"}, {"commit", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	os.MkdirAll(filepath.Join(repo, "demos"), 0755)
	os.WriteFile(filepath.Join(repo, "demos", "file.txt"), []byte("preserved"), 0644)
	request := func(method, origin, contentType, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/apps/app1/release/ignore-policy", strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		method, origin, content string
		status                  int
	}{
		{"GET", "", "", 405}, {"POST", "https://evil.invalid", "application/json", 403}, {"POST", "http://localhost:17656", "text/plain", 403},
	} {
		if w := request(tc.method, tc.origin, tc.content, ""); w.Code != tc.status {
			t.Fatalf("boundary: %d %s", w.Code, w.Body.String())
		}
	}
	w := request("POST", "http://127.0.0.1:17656", "application/json", "")
	if w.Code != 200 {
		t.Fatalf("ensure: %d %s", w.Code, w.Body.String())
	}
	var policy publisher.ManagedIgnorePolicy
	if err := json.Unmarshal(w.Body.Bytes(), &policy); err != nil || !policy.Managed {
		t.Fatal("invalid policy", err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	w = request("POST", "http://127.0.0.1:17656", "application/json", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	after, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if !bytes.Equal(raw, after) {
		t.Fatal("non-idempotent ensure")
	}
	for _, invalid := range []string{`{}`, `{"id":"demos","enabled":null}`, `{"enabled":"false"}`} {
		if w = request("PATCH", "http://127.0.0.1:17656", "application/json", invalid); w.Code != 400 {
			t.Fatalf("invalid update accepted: %d", w.Code)
		}
	}
	body, _ := json.Marshal(publisher.ManagedIgnoreUpdate{ID: "demos", Revision: policy.Revision, Enabled: false})
	w = request("PATCH", "http://127.0.0.1:17656", "application/json", string(body))
	if w.Code != 200 {
		t.Fatalf("restore: %d %s", w.Code, w.Body.String())
	}
	var result publisher.ManagedIgnoreResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Preflight == nil {
		t.Fatal("missing preflight", err)
	}
	found := false
	for _, change := range result.Preflight.Changes {
		if change.Path == "demos/file.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("restored demo absent")
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "demos/file.txt")); string(data) != "preserved" {
		t.Fatal("file removed or changed")
	}
	w = request("PATCH", "http://127.0.0.1:17656", "application/json", string(body))
	if w.Code == 200 {
		t.Fatal("stale update accepted")
	}
}
