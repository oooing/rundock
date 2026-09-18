package api

import (
	"encoding/json"
	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/publisher"
	"github.com/launcher-sidecar/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPreviewAPIReadOnlyContract(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	name := "中文 #%.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("hello preview"), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &store.App{ID: "preview-app", Name: "fixture", Cwd: root, EntryScript: filepath.Join(root, "start.bat"), AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}
	if err := st.CreateApp(a); err != nil {
		t.Fatal(err)
	}
	s := New(st, logbus.NewHub(), adapter.NewRegistry())
	r := httptest.NewRequest(http.MethodGet, "/api/apps/preview-app/release/file-preview?path="+url.QueryEscape(name), nil)
	r.Header.Set("Origin", "http://127.0.0.1:1421")
	w := httptest.NewRecorder()
	s.Router().ServeHTTP(w, r)
	var result publisher.FilePreview
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Text != "hello preview" || result.Path != name || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bad response %d %s", w.Code, w.Body.String())
	}
}

func TestPreviewOriginBoundary(t *testing.T) {
	for _, origin := range []string{"", "http://127.0.0.1:1421", "http://localhost:1421", "http://tauri.localhost", "tauri://localhost", "http://[::1]:1421"} {
		if !previewOriginAllowed(origin) {
			t.Errorf("rejected local %s", origin)
		}
	}
	for _, origin := range []string{"null", "https://evil.example", "http://localhost.evil.example", "file:///tmp", "http://evil@localhost", "http://localhost/?evil"} {
		if previewOriginAllowed(origin) {
			t.Errorf("allowed %s", origin)
		}
		r := httptest.NewRequest(http.MethodGet, "/preview", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		(&Server{}).handleReleaseFilePreview(w, r, "app")
		if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("invalid preview boundary")
		}
	}
}

func TestPreviewReadOnlyMethod(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).handleReleaseFilePreview(w, httptest.NewRequest(http.MethodPost, "/preview", nil), "app")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}

func TestFindingContextHTTPBoundary(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/api/apps/a/release/finding-context", nil)
		if method == http.MethodGet {
			r.Header.Set("Origin", "https://untrusted.example")
		}
		w := httptest.NewRecorder()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		(&Server{}).handleFindingContext(w, r, "a")
		expected := http.StatusMethodNotAllowed
		if method == http.MethodGet {
			expected = http.StatusForbidden
		}
		if w.Code != expected || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("raw context boundary failed")
		}
		if method == http.MethodGet && w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("external origin allowed")
		}
	}
}
