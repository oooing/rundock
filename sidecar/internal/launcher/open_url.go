package launcher

import (
	"net"
	"net/url"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

// PreferredOpenURL chooses a project page, never a health-check path by guess.
// Explicit script metadata is optional; invalid metadata is rejected by Start.
func PreferredOpenURL(entryScript, lastURL string, services []*store.AppService) string {
	if config, err := readStartupReadiness(entryScript); err == nil && config.openURL != "" {
		return config.openURL
	}
	var frontend string
	for _, svc := range services {
		if svc.StatusScope == "auxiliary" || svc.Role != store.RoleFrontend || svc.URL == "" {
			continue
		}
		if svc.Health == "healthy" {
			return svc.URL
		}
		if frontend == "" {
			frontend = svc.URL
		}
	}
	if frontend != "" {
		return frontend
	}
	if lastURL != "" {
		auxiliary := false
		lastPort := 0
		if u, err := url.Parse(lastURL); err == nil {
			ip := net.ParseIP(u.Hostname())
			if strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback()) {
				lastPort = portFromURLStr(lastURL)
			}
		}
		for _, svc := range services {
			if svc.StatusScope == "auxiliary" && (svc.URL == lastURL || (lastPort > 0 && svc.Port == lastPort)) {
				auxiliary = true
			}
		}
		if !auxiliary {
			return lastURL
		}
	}
	for _, svc := range services {
		if svc.StatusScope != "auxiliary" && svc.Role != store.RoleDatabase && svc.URL != "" {
			return svc.URL
		}
	}
	return ""
}
