package api

import (
	"errors"
	"fmt"
	"sort"

	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

// Confirm IPv6 wildcard overlap by a real IPv4 bind failure plus a second
// listener snapshot. IPv6-only owners are never offered for IPv4 termination.
func freshBindingEvidence(a *store.App, snapshot recovery.RuntimeSnapshot) ([]int, []recovery.Conflict, error) {
	reserved := map[int]bool{}
	conflicts := []recovery.Conflict{}
	for _, binding := range recovery.RequiredBindings(a) {
		if recovery.BindingExcluded(binding, snapshot.ReservedRanges) {
			continue
		}
		listening := false
		for _, l := range snapshot.Listeners {
			if recovery.ListenerConflicts(binding, l) {
				listening = true
				break
			}
		}
		if listening {
			continue
		}
		denied, err := recovery.ProbeBindings([]recovery.PortBinding{binding})
		for _, p := range denied {
			reserved[p] = true
		}
		if err == nil {
			continue
		}
		var failure *recovery.BindFailure
		if !errors.As(err, &failure) || (!failure.Occupied && !failure.AccessDenied) {
			return nil, nil, err
		}
		possible := map[int]bool{}
		for _, l := range snapshot.Listeners {
			if recovery.PotentialDualStack(binding, l) {
				possible[l.PID] = true
			}
		}
		if len(possible) != 1 {
			if failure.AccessDenied {
				continue
			}
			return nil, nil, err
		}
		fresh, checkErr := probe.SnapshotListenersChecked()
		if checkErr != nil {
			return nil, nil, fmt.Errorf("无法确认端口占用，请重新检查")
		}
		confirmed := 0
		for _, l := range fresh {
			if recovery.ListenerConflicts(binding, l) {
				return nil, nil, fmt.Errorf("端口占用程序已变化，请重新检查")
			}
			if recovery.PotentialDualStack(binding, l) {
				if !possible[l.PID] {
					return nil, nil, fmt.Errorf("端口占用程序已变化，请重新检查")
				}
				confirmed = l.PID
			}
		}
		if confirmed == 0 {
			return nil, nil, fmt.Errorf("端口占用程序已变化，请重新检查")
		}
		// Exclusive Windows dual-stack listeners can report WSAEACCES instead
		// of WSAEADDRINUSE. A corroborated unique wildcard owner is an app
		// conflict; access denial without that evidence remains system-limited.
		delete(reserved, binding.Port)
		conflicts = append(conflicts, recovery.Conflict{Port: binding.Port, PID: confirmed})
	}
	ports := []int{}
	for p := range reserved {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports, conflicts, nil
}
