package recovery

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/launcher-sidecar/internal/importer"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

// Observation is read-only. Detecting a surviving process never invents a
// managed run, reattaches old log pipes, or grants permission to terminate it.
type Observation struct {
	State     string              `json:"state"` // clear, checking, unknown, running, conflict
	Message   string              `json:"message,omitempty"`
	PID       int                 `json:"pid,omitempty"`
	Services  []*store.AppService `json:"-"`
	Conflicts []Conflict          `json:"conflicts,omitempty"`
}

type RuntimeSnapshot struct {
	Processes []Process
	Listeners []probe.PortListener
}

func ReadRuntimeSnapshot() (RuntimeSnapshot, error) {
	processes, err := Snapshot()
	if err != nil {
		return RuntimeSnapshot{}, err
	}
	listeners, err := probe.SnapshotListenersChecked()
	for i := range processes {
		if !SameProcess(processes[i]) {
			processes[i].Created = ""
		}
	}
	return RuntimeSnapshot{Processes: processes, Listeners: listeners}, err
}

func samePath(a, b string) bool {
	return filepath.IsAbs(a) && filepath.IsAbs(b) && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func SameProject(a, b string) bool { return samePath(a, b) }

func hasEntry(p Process, entry string) bool {
	if samePath(p.Executable, entry) {
		return true
	}
	for _, arg := range commandArgs(p.CommandLine) {
		if samePath(arg, entry) {
			return true
		}
	}
	return false
}

// InspectRuntime requires process evidence as well as port evidence. Unknown
// port owners block a start; an unrelated process on the same port is never
// accepted as this project. Parent links also validate process creation order.
func InspectRuntime(a *store.App, known []*store.AppService, snapshot RuntimeSnapshot) Observation {
	result := Observation{State: "clear", Services: []*store.AppService{}}
	byPID := map[int]Process{}
	for _, p := range snapshot.Processes {
		byPID[p.PID] = p
	}
	belongs := func(pid int) bool {
		visited := map[int]bool{}
		for pid > 4 && !visited[pid] {
			visited[pid] = true
			p, exists := byPID[pid]
			if !exists || p.Created == "" || p.Executable == "" {
				return false
			}
			if Belongs(a.Cwd, p) {
				return true
			}
			parent, exists := byPID[p.ParentPID]
			born, _ := strconv.ParseUint(p.Created, 10, 64)
			parentBorn, _ := strconv.ParseUint(parent.Created, 10, 64)
			if !exists || born == 0 || parentBorn == 0 || parentBorn > born {
				return false
			}
			pid = p.ParentPID
		}
		return false
	}
	ports := map[int]bool{}
	previous := map[int]*store.AppService{}
	// Imported PortHints also contain outbound proxy/dependency URLs. Only
	// declared bindings, previously observed services and configured app URLs
	// can block a start; a generic hint is not proof of an exclusive port.
	for _, port := range importer.DeclaredListenPorts(a.EntryScript) {
		if port > 0 {
			ports[port] = true
		}
	}
	for _, svc := range known {
		ports[svc.Port], previous[svc.Port] = true, svc
	}
	for _, link := range []string{a.LastURL, a.HealthURL} {
		if u, err := url.Parse(link); err == nil {
			if port, err := strconv.Atoi(u.Port()); err == nil {
				ports[port] = true
			}
		}
	}
	seen := map[int]bool{}
	conflictsSeen := map[[2]int]bool{}
	for _, listener := range snapshot.Listeners {
		if belongs(listener.PID) {
			if seen[listener.Port] {
				continue
			}
			seen[listener.Port] = true
			if result.PID == 0 {
				result.PID = listener.PID
			}
			svc := &store.AppService{ID: fmt.Sprintf("observed-%s-%d", a.ID, listener.Port), AppID: a.ID, Port: listener.Port, Role: "unknown", RoleSource: "auto", Health: "unknown"}
			if old := previous[listener.Port]; old != nil {
				svc.URL, svc.Role, svc.RoleSource = old.URL, old.Role, old.RoleSource
			}
			// A listening TCP socket alone does not prove an HTTP URL.
			for _, link := range []string{a.LastURL, a.HealthURL} {
				if u, err := url.Parse(link); err == nil && u.Port() == strconv.Itoa(listener.Port) && svc.URL == "" {
					svc.URL = link
				}
			}
			result.Services = append(result.Services, svc)
		} else if ports[listener.Port] {
			key := [2]int{listener.Port, listener.PID}
			if conflictsSeen[key] {
				continue
			}
			conflictsSeen[key] = true
			p := byPID[listener.PID]
			result.Conflicts = append(result.Conflicts, Conflict{Port: listener.Port, PID: listener.PID, Name: filepath.Base(p.Executable)})
		}
	}
	// Also catch a still-running entry script before it opens any port. Editors
	// and terminals merely mentioning the project folder are not enough.
	for _, p := range snapshot.Processes {
		if p.PID > 4 && p.Created != "" && p.Executable != "" && hasEntry(p, a.EntryScript) {
			if result.PID == 0 {
				result.PID = p.PID
			}
		}
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].Port < result.Services[j].Port })
	sort.Slice(result.Conflicts, func(i, j int) bool { return result.Conflicts[i].Port < result.Conflicts[j].Port })
	switch {
	case result.PID != 0:
		result.State, result.Message = "running", "检测到项目进程仍在运行，已恢复状态；当前仅监测，未接管启停和实时日志。"
	case len(result.Conflicts) > 0:
		result.State, result.Message = "conflict", "项目端口已被其他进程占用，请关闭占用程序或修改项目端口后重试。"
	}
	return result
}
