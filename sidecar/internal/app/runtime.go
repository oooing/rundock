// Package app 维护运行态：状态机、内存运行时表、与 store 的同步。
// 一个 App 在磁盘上是配置，启动后产生一个 Runtime（含进程句柄、当前 run、日志总线等）。
package app

import (
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

// Status 枚举。
const (
	StatusStarting  = "starting"
	StatusRunning   = "running"
	StatusDegraded  = "degraded"
	StatusStopped   = "stopped"
	StatusFailed    = "failed"
	StatusStopping  = "stopping"
)

// Runtime 描述一个 App 当前的运行态。仅存在于内存，进程退出或重启时更新。
type Runtime struct {
	AppID     string
	RunID     string
	PID       int
	RootPID   int
	Status    string
	StartedAt time.Time

	// ProcessHandle 由 proc 模块在启动时注入，用于停止与回收。
	// 用 any 避免循环依赖（proc 反向依赖 app 的状态）。
	ProcessHandle any

	mu sync.Mutex
}

func (r *Runtime) SetStatus(s string) {
	r.mu.Lock()
	r.Status = s
	r.mu.Unlock()
}

func (r *Runtime) GetStatus() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Status
}

// Registry 维护 appID -> *Runtime 的内存映射。并发安全。
type Registry struct {
	mu       sync.RWMutex
	runtimes map[string]*Runtime
}

func NewRegistry() *Registry {
	return &Registry{runtimes: map[string]*Runtime{}}
}

// Set 记录某 app 的运行态。
func (rg *Registry) Set(appID string, r *Runtime) {
	rg.mu.Lock()
	rg.runtimes[appID] = r
	rg.mu.Unlock()
}

// Get 读取。
func (rg *Registry) Get(appID string) (*Runtime, bool) {
	rg.mu.RLock()
	defer rg.mu.RUnlock()
	r, ok := rg.runtimes[appID]
	return r, ok
}

// Remove 移除（进程完全退出/回收后调用）。
func (rg *Registry) Remove(appID string) {
	rg.mu.Lock()
	delete(rg.runtimes, appID)
	rg.mu.Unlock()
}

// All 返回当前所有运行态的快照。
func (rg *Registry) All() []*Runtime {
	rg.mu.RLock()
	defer rg.mu.RUnlock()
	out := make([]*Runtime, 0, len(rg.runtimes))
	for _, r := range rg.runtimes {
		out = append(out, r)
	}
	return out
}

// IsRunning 是否处于活跃状态（starting/running/degraded/stopping）。
func (rg *Registry) IsRunning(appID string) bool {
	r, ok := rg.Get(appID)
	if !ok {
		return false
	}
	s := r.GetStatus()
	return s == StatusStarting || s == StatusRunning || s == StatusDegraded || s == StatusStopping
}

// Manager 组合 store 与 registry，提供状态变更的统一入口（同时落库 + 推事件）。
type Manager struct {
	Store    *store.Store
	Registry *Registry
	// OnStatus 变更状态时的回调（由 api 层注入，用于 WS 推送）。可为 nil。
	OnStatus func(appID, runID, old, new string)
}

func NewManager(s *store.Store) *Manager {
	return &Manager{Store: s, Registry: NewRegistry()}
}

// Transition 改变运行态：更新 runtime + store(app.last_status + app_runs.status)，并触发回调。
func (m *Manager) Transition(rt *Runtime, newStatus string, exitCode *int) {
	old := rt.GetStatus()
	if old == newStatus {
		return
	}
	rt.SetStatus(newStatus)

	// 落库：app_runs 状态
	_ = m.Store.UpdateRunStatus(rt.RunID, newStatus, exitCode)

	// 缓存到 app.last_status（终态才写）
	switch newStatus {
	case StatusRunning, StatusDegraded, StatusStopped, StatusFailed:
		_ = m.Store.TouchAppRuntime(rt.AppID, "", "", newStatus)
	}

	if m.OnStatus != nil {
		m.OnStatus(rt.AppID, rt.RunID, old, newStatus)
	}
}
