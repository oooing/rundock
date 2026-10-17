package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

// Share a bounded system snapshot across cards. Rendering never blocks on WMI;
// start requests fresh evidence under the existing startup lock.
type runtimeMonitor struct {
	mu        sync.RWMutex
	gate      chan struct{}
	read      func() (recovery.RuntimeSnapshot, error)
	snapshot  recovery.RuntimeSnapshot
	checked   time.Time
	err       error
	diagnoses map[string]cachedPortDiagnosis
}

type cachedPortDiagnosis struct {
	config      string
	expires     time.Time
	observation recovery.Observation
}

// Every consumer (cards, start checks, conflict resolution and restart plans)
// receives the same project boundaries alongside the process snapshot.
func (s *Server) readProjectRuntimeSnapshot() (recovery.RuntimeSnapshot, error) {
	snapshot, err := recovery.ReadRuntimeSnapshot()
	if err != nil {
		return snapshot, err
	}
	projects, err := s.Store.ListApps()
	if err != nil {
		return recovery.RuntimeSnapshot{}, err
	}
	snapshot.Projects = projects
	snapshot.LauncherPID = os.Getpid()
	for _, rt := range s.Manager.Registry.All() {
		if s.Manager.Registry.IsRunning(rt.AppID) {
			snapshot.ManagedRoots = append(snapshot.ManagedRoots, recovery.ProcessOwner{AppID: rt.AppID, PID: rt.RootPID, Created: rt.RootCreated})
			if h, ok := rt.ProcessHandle.(interface {
				Members() (map[int]uint64, bool, error)
			}); ok {
				members, _, err := h.Members()
				if err != nil {
					return recovery.RuntimeSnapshot{}, err
				}
				for pid, created := range members {
					snapshot.ManagedRoots = append(snapshot.ManagedRoots, recovery.ProcessOwner{AppID: rt.AppID, PID: pid, Created: strconv.FormatUint(created, 10), Inherited: pid != rt.RootPID})
				}
			}
		}
	}
	return snapshot, nil
}

func (m *runtimeMonitor) refresh(ctx context.Context) error {
	select {
	case m.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-m.gate }()
	snapshot, err := m.read()
	if err != nil {
		log.Printf("[runtime-check] snapshot failed: %v", err)
	}
	m.mu.Lock()
	m.snapshot, m.err, m.checked = snapshot, err, time.Now()
	m.mu.Unlock()
	return err
}

func (s *Server) observeRuntime(a *store.App) *recovery.Observation {
	if s.runtimeMonitor == nil || s.Manager.Registry.IsRunning(a.ID) {
		return nil
	}
	m := s.runtimeMonitor
	m.mu.RLock()
	snapshot, checked, err := m.snapshot, m.checked, m.err
	cached := m.diagnoses[a.ID]
	m.mu.RUnlock()
	if checked.IsZero() {
		return &recovery.Observation{State: "checking", Message: "正在检查项目进程和端口…"}
	}
	if err != nil || time.Since(checked) > 30*time.Second {
		return &recovery.Observation{State: "unknown", Message: "暂时无法确认项目状态，已暂停启动，请稍后重新检查。"}
	}
	known, e := s.Store.ListLatestServicesByApp(a.ID)
	if e != nil {
		return &recovery.Observation{State: "unknown", Message: "暂时无法确认项目状态，已暂停启动，请稍后重新检查。"}
	}
	observation := recovery.InspectRuntime(a, known, snapshot)
	if cached.expires.After(time.Now()) && cached.config == launchFingerprint(a) && observation.State != "running" {
		return &cached.observation
	}
	return &observation
}

// Reuse the shared process snapshot rather than launching WMI per project and
// discovery tick. Missing/stale evidence postpones discovery, never widens it.
func (s *Server) filterServiceProcesses(id string, members map[int]uint64) map[int]uint64 {
	m := s.runtimeMonitor
	m.mu.RLock()
	snapshot, checked, err := m.snapshot, m.checked, m.err
	m.mu.RUnlock()
	if err != nil || checked.IsZero() || time.Since(checked) > 30*time.Second {
		return nil
	}
	for _, a := range snapshot.Projects {
		if a.ID == id {
			return recovery.FilterServiceProcesses(a, snapshot, members)
		}
	}
	return nil
}

func (s *Server) checkBeforeStart(ctx context.Context, a *store.App) error {
	if s.runtimeMonitor == nil {
		return nil
	}
	for _, rt := range s.Manager.Registry.All() {
		if !s.Manager.Registry.IsRunning(rt.AppID) {
			continue
		}
		peer, err := s.Store.GetApp(rt.AppID)
		if err != nil {
			return fmt.Errorf("暂时无法确认项目状态，已暂停启动，请稍后重新检查。")
		}
		if peer != nil && recovery.SameProject(a.Cwd, peer.Cwd) {
			return fmt.Errorf("项目已在运行，请勿重复启动。")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.runtimeMonitor.refresh(ctx); err != nil {
		return fmt.Errorf("暂时无法确认项目状态，已暂停启动，请稍后重新检查。")
	}
	s.runtimeMonitor.mu.RLock()
	snapshot := s.runtimeMonitor.snapshot
	s.runtimeMonitor.mu.RUnlock()
	known, err := s.Store.ListLatestServicesByApp(a.ID)
	if err != nil {
		return fmt.Errorf("无法确认项目服务，请稍后重新检查")
	}
	observation := recovery.InspectRuntime(a, known, snapshot)
	if observation.State == "running" {
		return fmt.Errorf("%s", observation.Message)
	}
	denied, dualStack, err := freshBindingEvidence(a, snapshot)
	if err != nil {
		return err
	}
	reserved := append(observation.ReservedPorts, denied...)
	if len(reserved) > 0 {
		return fmt.Errorf("端口 %v 被 Windows 保留或拒绝绑定，关闭程序无法释放，请调整项目端口配置", reserved)
	}
	conflicts := append(observation.Conflicts, dualStack...)
	if len(conflicts) > 0 {
		result := portResolution{State: "conflict", Message: "项目端口已被其他程序占用，可查看占用程序并处理", ReservedPorts: []int{}}
		for _, c := range conflicts {
			name := c.Name
			for _, p := range snapshot.Processes {
				if p.PID == c.PID {
					name = filepath.Base(p.Executable)
					break
				}
			}
			result.Conflicts = append(result.Conflicts, portConflict{Port: c.Port, PID: c.PID, Name: name})
		}
		s.cachePortDiagnosis(a, result)
		return fmt.Errorf("%s", result.Message)
	}
	s.runtimeMonitor.mu.Lock()
	delete(s.runtimeMonitor.diagnoses, a.ID)
	s.runtimeMonitor.mu.Unlock()
	return nil
}

func (s *Server) monitorRuntime(ctx context.Context) {
	delay := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		err := s.runtimeMonitor.refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		delay = 10 * time.Second
		if err != nil {
			delay = 30 * time.Second
		}
		if s.Hub != nil {
			apps, _ := s.Store.ListApps()
			for _, a := range apps {
				if !s.Manager.Registry.IsRunning(a.ID) {
					s.Hub.BroadcastStatus(a.ID, "", "", "")
				}
			}
		}
	}
}

func (s *Server) handleRuntimeCheck(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, 409, "已有启停操作进行中，请稍后重试")
		return
	}
	defer s.startupMu.Unlock()
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		writeError(w, 404, "app not found")
		return
	}
	var fresh *portResolution
	if s.runtimeMonitor != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if result, _, _, err := s.portEvidence(ctx, id); err == nil {
			fresh = &result
		}
	}
	view := appView(a, s)
	if fresh != nil {
		view["runtimeCheck"] = fresh
	}
	writeJSON(w, 200, view)
}

func (s *Server) rejectUnmanagedControl(w http.ResponseWriter, id string) bool {
	if s.runtimeMonitor == nil || s.Manager.Registry.IsRunning(id) {
		return false
	}
	a, _ := s.Store.GetApp(id)
	if a == nil {
		return false
	}
	// An observation never grants a process handle or permission to terminate.
	if check := s.observeRuntime(a); check != nil && check.State != "clear" {
		writeError(w, 409, "当前未接管项目进程，不能直接停止或重启；请先在原启动位置停止服务。")
		return true
	}
	return false
}
