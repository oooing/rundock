package netproxy

import "strings"

func parseSystemProxy(server, scheme string) []string {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil
	}
	if !strings.Contains(server, "=") {
		return []string{normalizeProxy(server, "http")}
	}
	values := map[string]string{}
	for _, entry := range strings.Split(server, ";") {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	var out []string
	// Per-protocol settings are deliberate: an HTTP-only proxy is not an
	// implicit instruction to route HTTPS through the same server.
	for _, key := range []string{scheme, "socks"} {
		if value := values[key]; value != "" {
			protocol := "http"
			if key == "socks" {
				protocol = "socks5"
			}
			out = append(out, normalizeProxy(value, protocol))
		}
	}
	return out
}

func normalizeProxy(value, scheme string) string {
	if !strings.Contains(value, "://") {
		return scheme + "://" + value
	}
	return value
}
