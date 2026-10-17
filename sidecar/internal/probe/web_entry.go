package probe

import (
	"context"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebEntry is separate from process readiness and service role. A healthy
// API, HMR endpoint or extension server need not have a browser homepage.
type WebEntry struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

func LocalWebURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
}

// No proxy, health-route fallback, cookies or off-machine redirects. Only the
// actual candidate page is checked; redirects on the same local port are OK.
var webClient = &http.Client{
	Timeout:   2 * time.Second,
	Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !LocalWebURL(req.URL.String()) || req.URL.Port() != via[0].URL.Port() {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func CheckWebEntry(ctx context.Context, raw string) WebEntry {
	if !LocalWebURL(raw) {
		return WebEntry{"unavailable", "unsupported_address"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return WebEntry{"unavailable", "invalid_address"}
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := webClient.Do(req)
	if err != nil {
		return WebEntry{"unavailable", "connection_failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 405 {
		return WebEntry{"not_page", "missing_page"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WebEntry{"unavailable", "http_status"}
	}
	disposition, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if disposition == "attachment" {
		return WebEntry{"not_page", "download"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return WebEntry{"unavailable", "read_failed"}
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if ct == "" {
		ct, _, _ = mime.ParseMediaType(http.DetectContentType(body))
	}
	if (ct != "text/html" && ct != "application/xhtml+xml") || len(strings.TrimSpace(string(body))) == 0 {
		return WebEntry{"not_page", "not_html"}
	}
	return WebEntry{"available", ""}
}
