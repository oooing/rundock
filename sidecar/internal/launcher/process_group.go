package launcher

import (
	"strconv"

	"github.com/launcher-sidecar/internal/app"
)

// Current members remain discoverable after the entry script has exited.
func ownedRunProcesses(rt *app.Runtime) (map[int]uint64, bool, error) {
	if h, ok := rt.ProcessHandle.(interface {
		Members() (map[int]uint64, bool, error)
	}); ok {
		members, supported, err := h.Members()
		if supported {
			return members, true, err
		}
	}
	members := collectProcessIdentities(rt.RootPID)
	if rt.RootCreated != "" {
		created, _ := strconv.ParseUint(rt.RootCreated, 10, 64)
		if members[rt.RootPID] != created {
			return nil, false, nil
		}
	}
	return members, false, nil
}
