//go:build windows

package proc

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed node_background.cjs
var nodeBackgroundHook []byte

// The preload belongs only to this run, never the user's global Node settings
// or project files. Keep it until the entire Job exits, not just its entry.
func managedNodeEnvironment(env []string) ([]string, func(), error) {
	dir, err := os.MkdirTemp("", "rundock-node-background-")
	if err != nil {
		return nil, nil, fmt.Errorf("prepare background launch: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	file := filepath.Join(dir, "background.cjs")
	if err := os.WriteFile(file, nodeBackgroundHook, 0600); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("prepare background launch: %w", err)
	}
	option := `--require="` + strings.ReplaceAll(file, `\`, `/`) + `"`
	options := ""
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "NODE_OPTIONS") {
			options = value
		} else {
			result = append(result, entry)
		}
	}
	result = append(result, "NODE_OPTIONS="+strings.TrimSpace(options+" "+option))
	return result, cleanup, nil
}
