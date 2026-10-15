package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseIgnoreBatchRequest(t *testing.T) {
	router, repo, closeDB := newReleaseConfigAPIFixture(t)
	defer closeDB()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git("init")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.invalid")
	git("add", "package.json")
	git("commit", "-m", "fixture")
	head := git("rev-parse", "HEAD")
	files := []map[string]string{}
	for _, name := range []string{"local-one.txt", "local-two.txt"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, map[string]string{"path": name, "contentFingerprint": fmt.Sprintf("%x", sha256.Sum256([]byte(name)))})
	}
	body, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		r := httptest.NewRequest(http.MethodPost, "/api/apps/app1/release/ignore-file", strings.NewReader(string(body)))
		r.Header.Set("Origin", "http://127.0.0.1:17656")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: %d %s", attempt, w.Code, w.Body.String())
		}
		var result struct {
			IgnoreFiles []string        `json:"ignoreFiles"`
			Preflight   json.RawMessage `json:"preflight"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.IgnoreFiles) != 1 || result.IgnoreFiles[0] != ".gitignore" || len(result.Preflight) == 0 {
			t.Fatalf("unexpected response: %s", w.Body.String())
		}
	}
	for _, file := range files {
		git("check-ignore", "--", file["path"])
		if raw, err := os.ReadFile(filepath.Join(repo, file["path"])); err != nil || string(raw) != file["path"] {
			t.Fatalf("local file changed: %v", err)
		}
	}
	if git("rev-parse", "HEAD") != head || strings.TrimSpace(git("diff", "--cached", "--name-only")) != "" {
		t.Fatal("ignore changed HEAD or index")
	}
}

func TestReleaseIgnoreRequestBoundaries(t *testing.T) {
	router, _, closeDB := newReleaseConfigAPIFixture(t)
	defer closeDB()
	for _, tc := range []struct {
		method, origin, contentType, body string
		status                            int
	}{
		{"GET", "", "", "", http.StatusMethodNotAllowed},
		{"POST", "https://example.invalid", "application/json", `{}`, http.StatusForbidden},
		{"POST", "http://127.0.0.1:17656", "text/plain", `{}`, http.StatusForbidden},
		{"POST", "http://localhost:17656", "application/json", `{`, http.StatusBadRequest},
		{"POST", "http://127.0.0.1:17656", "application/json", `{"path":"../outside.txt"}`, http.StatusBadRequest},
		{"POST", "http://127.0.0.1:17656", "application/json", `{"files":[]}`, http.StatusBadRequest},
		{"POST", "http://127.0.0.1:17656", "application/json", `{"path":"local.txt","files":[]}`, http.StatusBadRequest},
		{"POST", "http://127.0.0.1:17656", "application/json", `{"files":[{"path":"../outside.txt"}]}`, http.StatusBadRequest},
	} {
		r := httptest.NewRequest(tc.method, "/api/apps/app1/release/ignore-file", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.contentType)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: got %d %s", tc, w.Code, w.Body.String())
		}
	}
}
