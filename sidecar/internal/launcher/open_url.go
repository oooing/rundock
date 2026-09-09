package launcher

import "github.com/launcher-sidecar/internal/store"

// PreferredOpenURL chooses a project page, never a health-check path by guess.
// Explicit script metadata is optional; invalid metadata is rejected by Start.
func PreferredOpenURL(entryScript, lastURL string, services []*store.AppService) string {
	if config, err := readStartupReadiness(entryScript, 0); err == nil && config.openURL != "" {
		return config.openURL
	}
	var frontend string
	for _, svc := range services {
		if svc.Role != store.RoleFrontend || svc.URL == "" {
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
		return lastURL
	}
	for _, svc := range services {
		if svc.Role != store.RoleDatabase && svc.URL != "" {
			return svc.URL
		}
	}
	return ""
}
