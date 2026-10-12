package netproxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func listenerProxy(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	return "http://" + l.Addr().String()
}
func closedProxy(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	value := "http://" + l.Addr().String()
	l.Close()
	return value
}

func TestRecoveryReevaluatesConfiguredPorts(t *testing.T) {
	old := closedProxy(t)
	for i := 0; i < 2; i++ {
		current := listenerProxy(t)
		calls := 0
		got, changed, err := Resolve(context.Background(), old, []string{old, current}, func(_ context.Context, p string) error {
			calls++
			if p != current {
				t.Fatal(p)
			}
			return nil
		})
		if err != nil || got != current || !changed || calls != 1 {
			t.Fatalf("%s %v %v calls=%d", got, changed, err, calls)
		}
	}
}
func TestOfflineNeverGuessesPortsOrUsesDirect(t *testing.T) {
	_, _, err := Resolve(context.Background(), closedProxy(t), nil, func(context.Context, string) error { t.Fatal("unexpected probe"); return nil })
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("want clear proxy error, got %v", err)
	}
}
func TestPreservesWorkingExternalAndAuthenticatedProxyPolicies(t *testing.T) {
	for _, value := range []string{listenerProxy(t), "http://proxy.corporate.invalid:8080", "http://name:secret@127.0.0.1:1", ""} {
		got, changed, err := Resolve(context.Background(), value, []string{listenerProxy(t)}, func(context.Context, string) error { t.Fatal("policy replaced"); return nil })
		if err != nil || changed || got != value {
			t.Fatalf("policy changed: %s %v %v", got, changed, err)
		}
	}
}
func TestAuthenticationOrTLSFailureIsNotReplayedOnAnotherRoute(t *testing.T) {
	want := errors.New("certificate or access denied")
	calls := 0
	_, _, err := Resolve(context.Background(), closedProxy(t), []string{listenerProxy(t), listenerProxy(t)}, func(context.Context, string) error { calls++; return want })
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
func TestCancellationAndProbeShareParentBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Resolve(ctx, closedProxy(t), []string{listenerProxy(t)}, func(context.Context, string) error { t.Fatal("canceled probe"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err = Resolve(ctx, closedProxy(t), []string{listenerProxy(t)}, func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestReadOnlyHTTPProbeAndChildEnvironmentRecovery(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodHead || r.Host != "fixture.invalid" {
			t.Errorf("unexpected probe %s %s", r.Method, r.Host)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	env := []string{"HTTPS_PROXY=" + closedProxy(t), "https_proxy=" + closedProxy(t), "ALL_PROXY=" + closedProxy(t), "NO_PROXY=localhost", "KEPT=value"}
	snapshot := append([]string(nil), env...)
	got, err := prepareEnvironment(context.Background(), env, "http://fixture.invalid/", []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	proxy, present, bypass := Environment(got, "https://fixture.invalid/")
	if proxy != server.URL || !present || bypass || requests != 1 {
		t.Fatalf("proxy=%s present=%v bypass=%v requests=%d", proxy, present, bypass, requests)
	}
	if !reflect.DeepEqual(env, snapshot) || !containsEntry(got, "KEPT=value") || !containsEntry(got, "NO_PROXY=localhost") {
		t.Fatal("environment was mutated")
	}
}
func containsEntry(env []string, want string) bool {
	for _, v := range env {
		if v == want {
			return true
		}
	}
	return false
}
func TestBypassAndSystemProtocolMapping(t *testing.T) {
	env := []string{"HTTPS_PROXY=" + closedProxy(t), "NO_PROXY=.github.com"}
	got, err := prepareEnvironment(context.Background(), env, "https://api.github.com/", []string{listenerProxy(t)})
	if err != nil || !reflect.DeepEqual(got, env) {
		t.Fatalf("NO_PROXY lost: %v", err)
	}
	u, _ := url.Parse("https://10.2.3.4/")
	if !Bypass(u, "10.0.0.0/8") {
		t.Fatal("CIDR bypass lost")
	}
	if !Bypass(u, "10.*") {
		t.Fatal("Windows wildcard bypass lost")
	}
	want := []string{"http://127.0.0.1:5678", "socks5://127.0.0.1:9012"}
	gotProxies := parseSystemProxy("http=127.0.0.1:1234;https=127.0.0.1:5678;socks=127.0.0.1:9012", "https")
	if !reflect.DeepEqual(gotProxies, want) {
		t.Fatalf("%v", gotProxies)
	}
	if got := parseSystemProxy("http=127.0.0.1:1234", "https"); len(got) != 0 {
		t.Fatalf("HTTP-only policy used for HTTPS: %v", got)
	}
}
func TestErrorRedactsProxyCredentials(t *testing.T) {
	text := (&UnavailableError{Proxy: "http://private:secret@127.0.0.1:1"}).Error()
	if strings.Contains(text, "private") || strings.Contains(text, "secret") {
		t.Fatal("credentials exposed")
	}
}

func TestHTTPRecoveryChecksAuthenticationAndMissingRoot(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusProxyAuthRequired, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Error("non-read-only probe")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			env := []string{"HTTPS_PROXY=" + closedProxy(t)}
			// HTTP probe against the local proxy fixture avoids external networking.
			env = append(env, "HTTP_PROXY="+closedProxy(t))
			_, err := prepareEnvironment(context.Background(), env, "http://fixture.invalid/", []string{server.URL})
			if (err == nil) != (status == http.StatusNotFound) {
				t.Fatalf("status=%d err=%v", status, err)
			}
		})
	}
}
