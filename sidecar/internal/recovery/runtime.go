package recovery

import (
	"fmt"
	"net"
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
	State         string              `json:"state"` // clear, checking, unknown, running, conflict, reserved
	Message       string              `json:"message,omitempty"`
	PID           int                 `json:"pid,omitempty"`
	Services      []*store.AppService `json:"-"`
	Conflicts     []Conflict          `json:"conflicts,omitempty"`
	ReservedPorts []int               `json:"reservedPorts,omitempty"`
}

type RuntimeSnapshot struct {
	Processes      []Process
	Listeners      []probe.PortListener
	ReservedRanges []PortRange
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
	return RuntimeSnapshot{Processes: processes, Listeners: listeners, ReservedRanges: ReadExcludedRanges()}, err
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
	bindings := RequiredBindings(a)
	previous := map[int]*store.AppService{}
	// PortHints can include outbound dependencies, while saved services and
	// LastURL describe earlier runs. Only current script declarations and the
	// explicit HealthURL are evidence of ports the next run must bind.
	for _, port := range RequiredPorts(a) {
		if port > 0 {
			ports[port] = true
		}
	}
	for _, svc := range known {
		previous[svc.Port] = svc
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
		} else if ports[listener.Port] && BindingsConflict(bindings, listener) {
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
	sort.Slice(result.Conflicts, func(i, j int) bool {
		if result.Conflicts[i].Port == result.Conflicts[j].Port {
			return result.Conflicts[i].PID < result.Conflicts[j].PID
		}
		return result.Conflicts[i].Port < result.Conflicts[j].Port
	})
	reservedSeen := map[int]bool{}
	for _, binding := range bindings {
		listening := false
		for _, l := range snapshot.Listeners {
			if ListenerConflicts(binding, l) {
				listening = true
				break
			}
		}
		if !listening && !reservedSeen[binding.Port] && BindingExcluded(binding, snapshot.ReservedRanges) {
			result.ReservedPorts = append(result.ReservedPorts, binding.Port)
			reservedSeen[binding.Port] = true
		}
	}
	sort.Ints(result.ReservedPorts)
	switch {
	case result.PID != 0:
		result.State, result.Message = "running", "检测到项目进程仍在运行，已恢复状态；当前仅监测，未接管启停和实时日志。"
	case len(result.Conflicts) > 0:
		result.State, result.Message = "conflict", "项目端口已被其他程序占用，可查看占用程序并处理。"
	case len(result.ReservedPorts) > 0:
		result.State, result.Message = "reserved", "项目端口被 Windows 保留，关闭应用程序无法释放，请调整项目端口配置。"
	}
	if result.State == "clear" {
		for _, binding := range bindings {
			for _, listener := range snapshot.Listeners {
				if PotentialDualStack(binding, listener) {
					result.State, result.Message = "unknown", "端口可能被 IPv6 双栈程序占用，请重新检查端口。"
				}
			}
		}
	}
	return result
}

// RequiredPorts contains only current authoritative listen declarations. Saved
// URLs/services and generic hints may be dependencies or historical discoveries.
func RequiredPorts(a *store.App) []int {
	ports := map[int]bool{}
	for _, port := range importer.DeclaredListenPorts(a.EntryScript) {
		if port > 0 && port <= 65535 {
			ports[port] = true
		}
	}
	if u, err := url.Parse(a.HealthURL); err == nil {
		host := u.Hostname()
		ip := net.ParseIP(host)
		local := (u.Scheme == "http" || u.Scheme == "https") && (host == "localhost" || (ip != nil && ip.IsLoopback()))
		if port, err := strconv.Atoi(u.Port()); local && err == nil && port > 0 && port <= 65535 {
			ports[port] = true
		}
	}
	result := []int{}
	for port := range ports {
		result = append(result, port)
	}
	sort.Ints(result)
	return result
}
