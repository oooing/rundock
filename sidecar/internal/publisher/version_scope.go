package publisher

import "github.com/launcher-sidecar/internal/releaseconfig"

// Groups exclusively owned by disabled targets do not block active targets.
// Unattached groups can be intentional source-only version configuration.
func disabledOnlyVersionGroups(cfg *releaseconfig.Config) map[string]bool {
	seen, enabled := map[string]bool{}, map[string]bool{}
	for _, target := range cfg.Targets {
		seen[target.VersionGroup] = true
		if target.Enabled {
			enabled[target.VersionGroup] = true
		}
	}
	for group := range seen {
		seen[group] = !enabled[group]
	}
	return seen
}
