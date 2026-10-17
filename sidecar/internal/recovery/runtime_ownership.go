package recovery

import (
	"strconv"

	"github.com/launcher-sidecar/internal/store"
)

func validParentCreation(child, parent string) bool {
	c, ce := strconv.ParseUint(child, 10, 64)
	p, pe := strconv.ParseUint(parent, 10, 64)
	return ce == nil && pe == nil && c > 0 && p > 0 && p <= c
}

// A managed launch is identified by PID AND native creation time. PIDs alone
// must never assign a later, unrelated process to an old run.
type ProcessOwner struct {
	AppID   string
	PID     int
	Created string
	// An inherited Job member can be a nested project's worker. A directly
	// managed root remains authoritative over shared executable paths.
	Inherited bool
}

// OwnedProcesses exposes the same boundary for callers that must reject a
// restart of a mixed project tree, including children without listening ports.
func OwnedProcesses(a *store.App, snapshot RuntimeSnapshot) map[int]bool {
	byPID := map[int]Process{}
	for _, p := range snapshot.Processes {
		byPID[p.PID] = p
	}
	belongs := runtimeOwnership(a, snapshot, byPID)
	owned := map[int]bool{}
	for _, p := range snapshot.Processes {
		if belongs(p.PID) {
			owned[p.PID] = true
		}
	}
	return owned
}

func runtimeOwnership(a *store.App, snapshot RuntimeSnapshot, byPID map[int]Process) func(int) bool {
	managed := map[int]ProcessOwner{}
	for _, root := range snapshot.ManagedRoots {
		p, ok := byPID[root.PID]
		if ok && root.Created != "" && p.Created == root.Created {
			managed[root.PID] = root
		}
	}
	projects := append([]*store.App{a}, snapshot.Projects...)
	// Cache direct evidence once per snapshot inspection, not per listener.
	type evidence struct{ found, ours bool }
	direct := map[int]evidence{}
	commandDirect := map[int]evidence{}
	match := func(p Process) evidence {
		if e, ok := direct[p.PID]; ok {
			return e
		}
		var candidates []*store.App
		for _, project := range projects {
			if project != nil && Belongs(project.Cwd, p) {
				candidates = append(candidates, project)
			}
		}
		e := evidence{found: len(candidates) > 0, ours: len(candidates) > 0}
		for _, project := range candidates {
			// A nested project owns its own workers; its parent directory does not.
			lessSpecific := false
			for _, other := range candidates {
				if !SameProject(project.Cwd, other.Cwd) && Inside(project.Cwd, other.Cwd) {
					lessSpecific = true
					break
				}
			}
			if !lessSpecific && !SameProject(a.Cwd, project.Cwd) {
				e.ours = false
			}
		}
		direct[p.PID] = e
		return e
	}
	return func(pid int) bool {
		var chain []Process
		var managedOwner *ProcessOwner
		seen := map[int]bool{}
		for current := pid; current > 4 && !seen[current]; {
			seen[current] = true
			p, ok := byPID[current]
			if !ok || p.Created == "" || p.Executable == "" {
				break
			}
			// Children of the launcher need independent ownership evidence. Do
			// not inherit the launcher's project, or anything above it.
			if current == snapshot.LauncherPID && current != pid {
				break
			}
			chain = append(chain, p)
			if owner, ok := managed[current]; ok && !owner.Inherited {
				return owner.AppID == a.ID
			}
			// Explicit project arguments are stronger than membership in an
			// outer launcher's Job (nested projects inherit that Job too).
			e, cached := commandDirect[p.PID]
			if !cached {
				e.found, e.ours = commandProjectOwnership(a, projects, p)
				commandDirect[p.PID] = e
			}
			if e.found {
				return e.ours
			}
			if owner, ok := managed[current]; ok && managedOwner == nil {
				copy := owner
				managedOwner = &copy
			}
			parent, ok := byPID[p.ParentPID]
			if !ok || !validParentCreation(p.Created, parent.Created) {
				break
			}
			current = p.ParentPID
		}
		if managedOwner != nil {
			return managedOwner.AppID == a.ID
		}
		for _, p := range chain {
			if e := match(p); e.found {
				return e.ours
			}
		}
		return false
	}
}

func commandProjectOwnership(a *store.App, projects []*store.App, p Process) (bool, bool) {
	var matches []*store.App
	args := commandArgs(p.CommandLine)
	for _, project := range projects {
		if project == nil {
			continue
		}
		for _, arg := range args {
			if Inside(project.Cwd, arg) {
				matches = append(matches, project)
				break
			}
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
			return true, false
		}
	}
	return len(matches) > 0, len(matches) > 0
}
