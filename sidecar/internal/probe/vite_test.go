package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const viteTestClient = `console.debug('[vite] connecting...'); new WebSocket(url, 'vite-hmr'); export function createHotContext(ownerPath) {}`

func TestWXTDevServerWithoutHomepage(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@vite/client" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write([]byte(viteTestClient))
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()
	for name, check := range map[string]func(context.Context, string) *HealthResult{"health": CheckHealth, "role": CheckRole} {
		t.Run(name, func(t *testing.T) {
			got := check(context.Background(), s.URL)
			if !got.Reachable || got.URL != s.URL+"/@vite/client" || got.StatusCode != 200 {
				t.Fatalf("WXT dev server not recognized: %+v", got)
			}
			role, confidence := Classify(ClassifyInput{BodyCT: got.ContentType, Body: got.Body})
			if role != RoleFrontend || confidence != ConfMedium {
				t.Fatalf("role=%s confidence=%d", role, confidence)
			}
		})
	}
	// An explicit readiness URL must still pass that exact check.
	if got := CheckURL(context.Background(), s.URL+"/required-ready"); got.Reachable || got.StatusCode != 404 {
		t.Fatalf("explicit readiness bypassed: %+v", got)
	}
	if got := CheckHealth(context.Background(), s.URL+"/required-ready"); got.Reachable {
		t.Fatal("non-root service URL must not fall back to the Vite client")
	}
}

func TestViteFallbackDoesNotMaskFailures(t *testing.T) {
	for _, code := range []int{400, 401, 403, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			clientRequests := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/@vite/client" {
					clientRequests++
					w.Header().Set("Content-Type", "text/javascript")
					_, _ = w.Write([]byte(viteTestClient))
					return
				}
				if r.URL.Path == "/health" {
					w.WriteHeader(code)
					return
				}
				http.NotFound(w, r)
			}))
			defer s.Close()
			got := CheckHealth(context.Background(), s.URL)
			if got.Reachable || got.StatusCode != code || clientRequests != 0 {
				t.Fatalf("real failure masked: %+v, client requests=%d", got, clientRequests)
			}
		})
	}
}

func TestViteFallbackRequiresActualJavaScriptClient(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		code                    int
	}{
		{"HTML catchall", "text/html", "<html>" + viteTestClient + "</html>", 200},
		{"unrelated JS", "text/javascript", "console.log('ready')", 200},
		{"incomplete signature", "application/javascript", "createHotContext vite-hmr", 200},
		{"client failure", "text/javascript", viteTestClient, 503},
		{"client missing", "text/javascript", viteTestClient, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/@vite/client" {
					w.Header().Set("Content-Type", tc.contentType)
					w.WriteHeader(tc.code)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				http.NotFound(w, r)
			}))
			defer s.Close()
			if got := CheckHealth(context.Background(), s.URL); got.Reachable {
				t.Fatalf("false ready: %+v", got)
			}
			if got := CheckRole(context.Background(), s.URL); got.Reachable {
				t.Fatalf("false role confirmation: %+v", got)
			}
		})
	}
}

// Opt-in, read-only acceptance against an already running local WXT service.
// Never dump the dev client body: it can contain a WebSocket token.
func TestLiveWXTDevEndpoint(t *testing.T) {
	base := os.Getenv("RUNDOCK_WXT_TEST_URL")
	if base == "" {
		t.Skip("set RUNDOCK_WXT_TEST_URL for live read-only acceptance")
	}
	if !strings.HasPrefix(base, "http://localhost:") && !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatal("live acceptance is restricted to local HTTP services")
	}
	got := CheckHealth(context.Background(), base)
	if !got.Reachable || got.URL != strings.TrimRight(base, "/")+"/@vite/client" || !isViteClient(got.ContentType, got.Body) {
		t.Fatalf("live WXT readiness: URL=%s status=%d reachable=%v reason=%s", got.URL, got.StatusCode, got.Reachable, got.Error)
	}
	t.Logf("verified: URL=%s status=%d contentType=%s", got.URL, got.StatusCode, got.ContentType)
}
