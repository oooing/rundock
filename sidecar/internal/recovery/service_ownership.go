package recovery

import (
	"strconv"

	"github.com/launcher-sidecar/internal/store"
)

// A native Job contains nested launchers' children too. Keep that lifetime
// protection, but do not advertise a known foreign project's ports as ours.
// Unknown command lines remain supported when native group identity is proven.
func FilterServiceProcesses(a *store.App, snapshot RuntimeSnapshot, members map[int]uint64) map[int]uint64 {
	byPID := map[int]Process{}
	for _, p := range snapshot.Processes {
		byPID[p.PID] = p
	}
	foreign := map[int]bool{}
	for pid, created := range members {
		p, ok := byPID[pid]
		if !ok || created == 0 || p.Created != strconv.FormatUint(created, 10) {
			continue
		}
		var matches []*store.App
		for _, project := range snapshot.Projects {
			if project != nil && Belongs(project.Cwd, p) {
				matches = append(matches, project)
			}
		}
		for _, project := range matches {
			moreSpecific := false
			for _, other := range matches {
				if !SameProject(project.Cwd, other.Cwd) && Inside(project.Cwd, other.Cwd) {
					moreSpecific = true
				}
			}
			if !moreSpecific && !SameProject(a.Cwd, project.Cwd) {
				foreign[p.PID] = true
			}
		}
	}
	out := map[int]uint64{}
	for pid, created := range members {
		p, exists := byPID[pid]
		if !exists || created == 0 || p.Created != strconv.FormatUint(created, 10) {
			continue
		}
		seen := map[int]bool{}
		excluded := false
		for !seen[p.PID] {
			seen[p.PID] = true
			if foreign[p.PID] {
				excluded = true
				break
			}
			parent, ok := byPID[p.ParentPID]
			// Only traverse the actual native group with verified identities.
			if !ok || members[parent.PID] == 0 || parent.Created != strconv.FormatUint(members[parent.PID], 10) || !validParentCreation(p.Created, parent.Created) {
				break
			}
			p = parent
		}
		if !excluded {
			out[pid] = created
		}
	}
	return out
}
