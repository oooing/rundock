package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

type portConflict struct {
	Port           int    `json:"port"`
	PID            int    `json:"pid"`
	Name           string `json:"name"`
	ManagedAppID   string `json:"managedAppId,omitempty"`
	ManagedAppName string `json:"managedAppName,omitempty"`
	CanClose       bool   `json:"canClose"`
	Reason         string `json:"reason,omitempty"`
}

type portResolution struct {
	State             string         `json:"state"`
	Message           string         `json:"message"`
	Conflicts         []portConflict `json:"conflicts"`
	ReservedPorts     []int          `json:"reservedPorts"`
	CanResolve        bool           `json:"canResolve"`
	ConfirmationToken string         `json:"confirmationToken,omitempty"`
	ExpiresAt         string         `json:"expiresAt,omitempty"`
}

type portTarget struct {
	Process                            recovery.Process
	RootProcess                        recovery.Process
	ManagedID, ManagedRun, RootCreated string
	RootPID                            int
}
type portPlan struct {
	AppID, Origin, Fingerprint string
	Expires                    time.Time
}

func ancestryContains(pid, ancestor int, byPID map[int]recovery.Process) bool {
	seen := map[int]bool{}
	for pid > 4 && !seen[pid] {
		if pid == ancestor {
			return true
		}
		seen[pid] = true
		child, ok := byPID[pid]
		if !ok || child.Created == "" {
			return false
		}
		parent, ok := byPID[child.ParentPID]
		if !ok || parent.Created == "" {
			return false
		}
		born, _ := strconv.ParseUint(child.Created, 10, 64)
		prior, _ := strconv.ParseUint(parent.Created, 10, 64)
		if prior > born {
			return false
		}
		pid = parent.PID
	}
	return false
}

func (s *Server) portEvidence(ctx context.Context, id string) (portResolution, []portTarget, string, error) {
	result := portResolution{State: "unknown", Message: "无法确认端口状态，请重新检查", Conflicts: []portConflict{}, ReservedPorts: []int{}}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		return result, nil, "", fmt.Errorf("app not found")
	}
	defer func() { s.cachePortDiagnosis(a, result) }()
	if s.Manager.Registry.IsRunning(id) {
		result.State, result.Message = "running", "项目已在运行，无需重复启动"
		return result, nil, "", nil
	}
	for _, rt := range s.Manager.Registry.All() {
		if !s.Manager.Registry.IsRunning(rt.AppID) {
			continue
		}
		peer, err := s.Store.GetApp(rt.AppID)
		if err != nil {
			return result, nil, "", err
		}
		if peer != nil && recovery.SameProject(a.Cwd, peer.Cwd) {
			result.State, result.Message = "running", "项目已在运行，无需重复启动"
			return result, nil, "", nil
		}
	}
	if s.runtimeMonitor == nil {
		return result, nil, "", nil
	}
	if err := s.runtimeMonitor.refresh(ctx); err != nil {
		return result, nil, "", nil
	}
	s.runtimeMonitor.mu.RLock()
	snapshot := s.runtimeMonitor.snapshot
	s.runtimeMonitor.mu.RUnlock()
	known, err := s.Store.ListLatestServicesByApp(id)
	if err != nil {
		return result, nil, "", err
	}
	observation := recovery.InspectRuntime(a, known, snapshot)
	result.State, result.Message = observation.State, observation.Message
	result.ReservedPorts = append(result.ReservedPorts, observation.ReservedPorts...)
	if observation.State == "running" {
		return result, nil, "", nil
	}
	denied, dualStack, err := freshBindingEvidence(a, snapshot)
	result.ReservedPorts = append(result.ReservedPorts, denied...)
	if err != nil {
		result.State, result.Message = "unknown", err.Error()
		return result, nil, "", nil
	}
	observation.Conflicts = append(observation.Conflicts, dualStack...)
	sort.Slice(observation.Conflicts, func(i, j int) bool {
		if observation.Conflicts[i].Port == observation.Conflicts[j].Port {
			return observation.Conflicts[i].PID < observation.Conflicts[j].PID
		}
		return observation.Conflicts[i].Port < observation.Conflicts[j].Port
	})
	reservedSeen := map[int]bool{}
	uniqueReserved := []int{}
	for _, port := range result.ReservedPorts {
		if !reservedSeen[port] {
			uniqueReserved = append(uniqueReserved, port)
			reservedSeen[port] = true
		}
	}
	sort.Ints(uniqueReserved)
	result.ReservedPorts = uniqueReserved
	byPID := map[int]recovery.Process{}
	for _, p := range snapshot.Processes {
		byPID[p.PID] = p
	}
	protected := map[int]bool{0: true, 4: true}
	for pid := os.Getpid(); pid > 4 && !protected[pid]; {
		protected[pid] = true
		pid = byPID[pid].ParentPID
	}
	targets := []portTarget{}
	seen := map[int]bool{}
	allSafe := len(observation.Conflicts) > 0
	for _, c := range observation.Conflicts {
		p := byPID[c.PID]
		view := portConflict{Port: c.Port, PID: c.PID, Name: filepath.Base(p.Executable)}
		if p.Executable == "" {
			view.Name = "未知程序"
		}
		target := portTarget{Process: p}
		// Managed ownership requires the exact native root creation identity
		// captured at spawn, not a reused PID or a command-line folder guess.
		for _, rt := range s.Manager.Registry.All() {
			root := byPID[rt.RootPID]
			if !s.Manager.Registry.IsRunning(rt.AppID) || rt.RootCreated == "" || root.Created != rt.RootCreated || !ancestryContains(p.PID, rt.RootPID, byPID) {
				continue
			}
			peer, _ := s.Store.GetApp(rt.AppID)
			if peer == nil {
				continue
			}
			target.ManagedID, target.ManagedRun, target.RootPID, target.RootCreated = rt.AppID, rt.RunID, rt.RootPID, rt.RootCreated
			target.RootProcess = root
			view.ManagedAppID, view.ManagedAppName = peer.ID, peer.Name
			break
		}
		switch {
		case protected[p.PID]:
			view.Reason = "RunDock 后台或其启动程序受保护，请手动处理"
		case p.Created == "" || p.Executable == "":
			view.Reason = "无法验证占用程序身份，请手动关闭"
		case target.ManagedID != "":
			view.CanClose = true
		case ancestryContains(p.PID, os.Getpid(), byPID):
			view.Reason = "RunDock 自身子进程受保护，请手动处理"
		default:
			if err := recovery.CanCloseExternal(p); err != nil {
				view.Reason = err.Error()
			} else {
				view.CanClose = true
			}
		}
		if !view.CanClose {
			allSafe = false
		}
		result.Conflicts = append(result.Conflicts, view)
		if !seen[p.PID] {
			targets = append(targets, target)
			seen[p.PID] = true
		}
	}
	if len(result.ReservedPorts) > 0 {
		result.State, result.Message = "reserved", "项目端口被 Windows 保留或拒绝绑定，关闭程序无法释放，请调整项目端口配置"
	} else if len(result.Conflicts) > 0 {
		result.State, result.Message = "conflict", "端口被其他程序占用，确认关闭后将自动启动项目"
		result.CanResolve = allSafe
	} else {
		result.State, result.Message = "clear", "端口可用，可以启动项目"
	}
	scriptHash, err := hashOf(a.EntryScript)
	if err != nil {
		return result, nil, "", fmt.Errorf("无法读取项目启动脚本")
	}
	// Persisted runtime labels are deliberately excluded: only current launch
	// configuration, actual script bytes, and exact confirmed owners bind a plan.
	fingerprint := recovery.Fingerprint(struct {
		Config, ActualScript string
		Targets              []portTarget
		Conflicts            []portConflict
		Reserved             []int
	}{launchFingerprint(a), scriptHash, targets, result.Conflicts, result.ReservedPorts})
	return result, targets, fingerprint, nil
}

func launchFingerprint(a *store.App) string {
	copy := *a
	copy.LastStatus, copy.LastURL, copy.LastStartedAt = "", "", nil
	return recovery.Fingerprint(copy)
}

func (s *Server) validateManagedTarget(t portTarget) error {
	rt, ok := s.Manager.Registry.Get(t.ManagedID)
	if !ok || !s.Manager.Registry.IsRunning(t.ManagedID) || rt.RunID != t.ManagedRun || rt.RootPID != t.RootPID || rt.RootCreated != t.RootCreated || !recovery.SameProcess(t.RootProcess) {
		return fmt.Errorf("占用项目已变化，请重新检查")
	}
	return nil
}
