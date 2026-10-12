// Package netproxy recovers stale local proxy addresses without guessing ports,
// editing user settings, disabling TLS, or replaying a mutating request.
package netproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type UnavailableError struct{ Proxy string }

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("本机网络代理 %s 不可用，未找到已验证的替代连接；请开启代理软件或检查连接设置后重试。", Endpoint(e.Proxy))
}

func Endpoint(value string) string {
	if u, err := proxyURL(value); err == nil {
		return u.Host
	}
	return "（地址无效）"
}

func proxyURL(value string) (*url.URL, error) {
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid proxy")
	}
	return u, nil
}

func localAddress(value string) (string, bool) {
	u, err := proxyURL(value)
	if err != nil || u.User != nil {
		return "", false
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h", "socks4", "socks4a":
	default:
		return "", false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return "", false
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else if u.Scheme == "http" {
			port = "80"
		} else {
			port = "1080"
		}
	}
	return net.JoinHostPort(host, port), true
}

func available(ctx context.Context, address string) bool {
	conn, err := (&net.Dialer{Timeout: 600 * time.Millisecond}).DialContext(ctx, "tcp", address)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Resolve changes only a failed, credential-free loopback proxy. Alternatives
// must come from actual user settings and pass a read-only destination probe.
// An open proxy's authentication/TLS/server errors never trigger fallback.
func Resolve(ctx context.Context, current string, candidates []string, verify func(context.Context, string) error) (string, bool, error) {
	address, local := localAddress(current)
	if !local {
		return current, false, nil
	}
	if available(ctx, address) {
		return current, false, nil
	}
	seen := map[string]bool{current: true}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		a, ok := localAddress(candidate)
		if !ok || !available(ctx, a) {
			continue
		}
		if verify != nil {
			if err := verify(ctx, candidate); err != nil {
				return "", false, err
			}
			return candidate, true, nil
		}
	}
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	return "", false, &UnavailableError{Proxy: current}
}

func envValue(env []string, names ...string) (string, bool) {
	for _, name := range names {
		for i := len(env) - 1; i >= 0; i-- {
			key, value, ok := strings.Cut(env[i], "=")
			if ok && key == name && value != "" {
				return value, true
			}
		}
	}
	return "", false
}

// Environment respects NO_PROXY before proposing any local recovery.
func Environment(env []string, target string) (string, bool, bool) {
	u, err := url.Parse(target)
	if err != nil {
		return "", false, false
	}
	noProxy, _ := envValue(env, "NO_PROXY", "no_proxy")
	if Bypass(u, noProxy) {
		return "", false, true
	}
	if u.Scheme == "https" {
		if p, ok := envValue(env, "https_proxy", "HTTPS_PROXY"); ok {
			return p, true, false
		}
	}
	if u.Scheme == "http" {
		if p, ok := envValue(env, "http_proxy", "HTTP_PROXY"); ok {
			return p, true, false
		}
	}
	p, ok := envValue(env, "all_proxy", "ALL_PROXY")
	return p, ok, false
}

func Bypass(u *url.URL, list string) bool {
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return true
	}
	for _, rule := range strings.Split(list, ",") {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if rule == "<local>" && !strings.Contains(host, ".") {
			return true
		}
		if strings.Contains(rule, "*") {
			if matches, _ := path.Match(rule, host); matches {
				return true
			}
		}
		if _, block, err := net.ParseCIDR(rule); err == nil && ip != nil && block.Contains(ip) {
			return true
		}
		if h, p, err := net.SplitHostPort(rule); err == nil {
			if p != u.Port() && !(u.Port() == "" && ((u.Scheme == "https" && p == "443") || (u.Scheme == "http" && p == "80"))) {
				continue
			}
			rule = h
		}
		rule = strings.TrimPrefix(strings.TrimPrefix(rule, "*"), ".")
		if host == rule || strings.HasSuffix(host, "."+rule) {
			return true
		}
	}
	return false
}

func WithEnvironment(env []string, proxy string) []string {
	out := make([]string, 0, len(env)+6)
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
			continue
		}
		out = append(out, item)
	}
	return append(out, "HTTP_PROXY="+proxy, "HTTPS_PROXY="+proxy, "ALL_PROXY="+proxy, "http_proxy="+proxy, "https_proxy="+proxy, "all_proxy="+proxy)
}

// PrepareEnvironment is shared by gh metadata, upload and verification. It
// re-reads OS settings for each command rather than pinning a port at startup.
func PrepareEnvironment(ctx context.Context, env []string, target string) ([]string, error) {
	return prepareEnvironment(ctx, env, target, SystemCandidates(target))
}

func prepareEnvironment(ctx context.Context, env []string, target string, candidates []string) ([]string, error) {
	current, present, bypass := Environment(env, target)
	if bypass {
		return env, nil
	}
	if !present && len(candidates) > 0 {
		current = candidates[0]
	}
	if current == "" {
		return env, nil
	}
	selected, _, err := Resolve(ctx, current, candidates, func(ctx context.Context, p string) error {
		u, e := proxyURL(p)
		if e != nil {
			return e
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(u)
		defer transport.CloseIdleConnections()
		request, e := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
		if e != nil {
			return e
		}
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, e := client.Do(request)
		if e != nil {
			return e
		}
		response.Body.Close()
		// The upload host has no public root resource. A 404 still verifies its
		// TLS connection; authentication and proxy authorization failures do not.
		if (response.StatusCode >= 200 && response.StatusCode < 400) || response.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("替代代理无法访问发布服务（HTTP %d）；请检查代理设置或访问权限后重试", response.StatusCode)
	})
	if err != nil {
		return nil, err
	}
	if !present || selected != current {
		return WithEnvironment(env, selected), nil
	}
	return env, nil
}

func CurrentEnvironment(target string) (string, bool, bool) { return Environment(os.Environ(), target) }

// HTTPClient applies the same fresh routing policy to public read-only requests.
func HTTPClient(ctx context.Context, target string, timeout time.Duration) (*http.Client, error) {
	env, err := PrepareEnvironment(ctx, os.Environ(), target)
	if err != nil {
		return nil, err
	}
	proxy, present, bypass := Environment(env, target)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if present && !bypass {
		u, err := proxyURL(proxy)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}
