package probe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckRootDoesNotUseHealthEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<script type="module" src="/@vite/client"></script>`))
	}))
	defer server.Close()

	got := CheckRoot(context.Background(), server.URL)
	if got == nil || got.ContentType != "text/html" || !containsAny(got.Body, frontendBodyKW) {
		t.Fatalf("CheckRoot() = %#v, want root HTML", got)
	}
}

func TestParseNetstat(t *testing.T) {
	// 真实 netstat 片段（含 LISTENING 与 ESTABLISHED 噪声）
	text := `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1056
  TCP    127.0.0.1:3000         127.0.0.1:51234        ESTABLISHED     1234
  TCP    0.0.0.0:5173           0.0.0.0:0              LISTENING       5678
  TCP    [::]:445               [::]:0                 LISTENING       4
  TCP    192.168.1.5:50123      20.1.2.3:443           ESTABLISHED     9012
`
	got := parseNetstat(text)
	ports := map[int]bool{}
	for _, p := range got {
		ports[p.Port] = true
	}
	// 只 LISTENING 的 135/5173/445 应被收录；ESTABLISHED 的 3000/50123 不应
	if !ports[135] {
		t.Errorf("expected 135 listening, got %v", ports)
	}
	if !ports[5173] {
		t.Errorf("expected 5173 listening, got %v", ports)
	}
	if !ports[445] {
		t.Errorf("expected 445 listening, got %v", ports)
	}
	if ports[3000] {
		t.Errorf("3000 is ESTABLISHED, should not be included: %v", ports)
	}
	if ports[50123] {
		t.Errorf("50123 is ESTABLISHED, should not be included: %v", ports)
	}
}

func TestDiffListeners(t *testing.T) {
	before := []PortListener{{Port: 3000}, {Port: 80}}
	after := []PortListener{{Port: 3000}, {Port: 80}, {Port: 5173}, {Port: 8080}}
	diff := DiffListeners(before, after)
	got := map[int]bool{}
	for _, p := range diff {
		got[p.Port] = true
	}
	if !got[5173] || !got[8080] {
		t.Errorf("expected new ports 5173,8080, got %v", got)
	}
	if got[3000] || got[80] {
		t.Errorf("existing ports should be excluded, got %v", got)
	}
}

func TestPortFromAddr(t *testing.T) {
	cases := map[string]int{
		"0.0.0.0:5173":   5173,
		"127.0.0.1:3000": 3000,
		"[::]:445":       445,
		"bad":            0,
	}
	for addr, want := range cases {
		if got := portFromAddr(addr); got != want {
			t.Errorf("portFromAddr(%q) = %d want %d", addr, got, want)
		}
	}
}
