package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebEntryDoesNotConfuseReadinessWithHomepage(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		ct, body, want string
	}{
		{"html", 200, "text/html", "<!doctype html><title>App</title>", "available"},
		{"xhtml", 200, "application/xhtml+xml", "<html/>", "available"},
		{"empty", 200, "text/html", "", "not_page"},
		{"api", 200, "application/json", `{"ok":true}`, "not_page"},
		{"vite client", 200, "text/javascript", "createHotContext vite-hmr [vite]", "not_page"},
		{"missing", 404, "text/html", "<h1>Not found</h1>", "not_page"},
		{"starting", 503, "text/html", "<h1>Starting</h1>", "unavailable"},
		{"auth", 401, "text/html", "<h1>Unauthorized</h1>", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.ct)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			if got := CheckWebEntry(context.Background(), srv.URL); got.State != tc.want {
				t.Fatal(got)
			}
		})
	}
}

func TestWebEntryChecksExactPathAndRestrictsRedirects(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cross-port redirect was followed") }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.NotFound(w, r)
		case "/library":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>library</html>"))
		case "/redirect":
			http.Redirect(w, r, "/library", 302)
		case "/other":
			http.Redirect(w, r, other.URL, 302)
		case "/remote":
			http.Redirect(w, r, "https://example.com", 302)
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		case "/@vite/client", "/health":
			t.Error("must not fall back to health or HMR")
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()
	for path, want := range map[string]string{"/": "not_page", "/library": "available", "/redirect": "available", "/other": "unavailable", "/remote": "unavailable", "/loop": "unavailable"} {
		if got := CheckWebEntry(context.Background(), srv.URL+path); got.State != want {
			t.Fatalf("%s: %+v", path, got)
		}
	}
	for _, raw := range []string{"file:///test", "http://example.com", "http://user:password@localhost:1234"} {
		if got := CheckWebEntry(context.Background(), raw); got.State != "unavailable" {
			t.Fatal(got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := CheckWebEntry(ctx, srv.URL); got.State != "unavailable" {
		t.Fatal(got)
	}
}
