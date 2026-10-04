// Package launcher 是编排层：把 adapter / proc / logbus / probe / store / 状态机串成一个完整的
// 启动—观测—停止闭环。它是 api 层之下、各基础模块之上的"指挥者"。
package launcher

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/proc"
	"github.com/launcher-sidecar/internal/store"
)

// Launcher 持有所有依赖，提供 Start/Stop/Restart。
type Launcher struct {
	Store        *store.Store
	Manager      *app.Manager
	Hub          *logbus.Hub
	Registry     *adapter.Registry
	Diagnostics  *diagnostics.Service
	BeforeStart  func(context.Context, *store.App) error
	startProcess func(context.Context, *proc.PreparedCommand, func(string)) (*proc.Handle, error)

	// 每个 appID 的活跃编排上下文，停止时取用
	mu   sync.Mutex
	runs map[string]*runState
}

// runState 一个 app 当前启动的运行态（进程句柄 + collector + cancel）。
type runState struct {
	exitDone      chan struct{}
	stopPIDs      []int
	handle        *proc.Handle
	collector     *logbus.Collector
	cancel        context.CancelFunc
	rootPID       int
	candidateURLs []string // 从日志解析到的候选 URL（供服务发现优先使用）
}

// New 创建 launcher。运行参数在每次操作时读取，保存后无需重启后台。
func New(s *store.Store, m *app.Manager, hub *logbus.Hub, reg *adapter.Registry) *Launcher {
	_ = reconcileDeclaredRoles(s)
	l := &Launcher{
		Store: s, Manager: m, Hub: hub, Registry: reg,
		runs:         map[string]*runState{},
		startProcess: proc.StartWithConPTY,
	}
	return l
}

// reconcileDeclaredRoles 用新规则回填历史服务；manual 标注由存储层守卫，不会被覆盖。
func reconcileDeclaredRoles(s *store.Store) error {
	apps, err := s.ListApps()
	if err != nil {
		return err
	}
	for _, a := range apps {
		roles := probe.DeclaredRoles(a.EntryScript)
		if len(roles) == 0 {
			continue
		}
		services, err := s.ListServicesByApp(a.ID)
		if err != nil {
			return err
		}
		for _, svc := range services {
			if role := roles[svc.Port]; role != "" {
				if _, err := s.UpdateServiceRoleIfAuto(svc.ID, string(role)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Start 启动一个已存在的 App。
// 流程：prepare -> 记录端口快照 -> 隐藏窗口 spawn(加入 Job Object) ->
// collector 接管日志 -> 状态 starting -> goroutine 等 ready/URL/健康 ->
// running/degraded；进程退出 -> stopped/failed。
func (l *Launcher) Start(ctx context.Context, appID string) error {
	a, err := l.Store.GetApp(appID)
	if err != nil {
		return fmt.Errorf("get app: %w", err)
	}
	if a == nil {
		return fmt.Errorf("app not found: %s", appID)
	}
	if l.Manager.Registry.IsRunning(appID) {
		return fmt.Errorf("app already running: %s", appID)
	}

	urlTimeout := durationFromSetting(l.Store, "url_discover_timeout_seconds", 30)
	gracePeriod := durationFromSetting(l.Store, "grace_period_seconds", 8)
	readiness, err := readStartupReadiness(a.EntryScript, urlTimeout)
	if err != nil {
		return fmt.Errorf("启动就绪配置: %w", err)
	}

	// prepare：优先用 App 自存的 cmd/args（已确认的配置），否则走适配器重新 prepare
	cmd, args := a.Cmd, a.Args
	cwd, env := a.Cwd, a.Env
	preparedByAdapter := false
	if cmd == "" {
		ad := l.Registry.Get(a.AdapterType)
		if ad == nil {
			ad = l.Registry.Select(a.Cwd, a.EntryScript)
		}
		po, err := ad.Prepare(&adapter.PrepareInput{EntryScript: a.EntryScript, Cwd: a.Cwd, Env: a.Env, PortHints: a.PortHints})
		if err != nil {
			return fmt.Errorf("prepare: %w", err)
		}
		cmd, args, cwd, env = po.Cmd, po.Args, po.Cwd, po.Env
		preparedByAdapter = true
		// 回写，避免下次再 prepare
		a.Cmd, a.Args, a.Cwd, a.Env = cmd, args, cwd, env
		_ = l.Store.UpdateApp(a)
	}

	if l.BeforeStart != nil {
		if err := l.BeforeStart(ctx, a); err != nil {
			return err
		}
	}
	// Retain the last known services even when this attempt fails before discovery.
	oldSvcs, _ := l.Store.ListLatestServicesByApp(appID)
	// 快照用户手动标注的角色（按端口），以便重启后还原到新 run 的服务上。
	manualRoles := map[int]string{}
	for _, s := range oldSvcs {
		if s.RoleSource == store.RoleSourceManual && s.Role != "" {
			manualRoles[s.Port] = s.Role
		}
	}
	if err := l.Store.PruneServiceHistory(appID); err != nil {
		return fmt.Errorf("retain service history: %w", err)
	}

	// 启动前端口快照（用于事后只看本进程树新增端口）
	beforePorts := probe.SnapshotListeners()

	runCtx, cancel := context.WithCancel(context.Background())
	runID := app.NewRunID()
	collector := logbus.NewCollector(l.Store, l.Hub, appID, runID)

	// 重要：先 CreateRun 再写日志，保证日志挂在有效 run 下，打开「查看日志」能查到。
	nowStr := time.Now().UTC().Format(time.RFC3339)
	nowTime := time.Now()
	if err := l.Store.CreateRun(&store.AppRun{
		ID: runID, AppID: appID, PID: 0, RootPID: 0,
		Status: app.StatusStarting, StartedAt: nowStr,
	}); err != nil {
		cancel()
		return fmt.Errorf("create run: %w", err)
	}
	if l.Diagnostics != nil {
		collector.OnLog = func(_ *logbus.Collector, stream, level, text string) {
			l.Diagnostics.RecordProcessLine(appID, runID, stream, level, text)
		}
		l.Diagnostics.Record(diagnostics.Event{
			AppID: appID, RunID: runID, Kind: "lifecycle", Severity: "info", Source: "launcher",
			Operation: "app.start", Stage: "spawn", Status: "started", Message: "开始启动项目",
			Context: map[string]any{"adapterType": a.AdapterType, "preparedByAdapter": preparedByAdapter},
		})
	}

	// 启动诊断：配置摘要（用户排查 + AI 排错用）
	envKeys := make([]string, 0, len(env))
	for k := range env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	collector.Info(fmt.Sprintf("[启动] 项目=%s adapter=%s runId=%s", a.Name, a.AdapterType, runID))
	collector.Info(fmt.Sprintf("[启动] entry=%s", a.EntryScript))
	collector.Info(fmt.Sprintf("[启动] cwd=%s", cwd))
	collector.Info(fmt.Sprintf("[启动] cmd=%s args=%v preparedByAdapter=%v", cmd, args, preparedByAdapter))
	collector.Debug(fmt.Sprintf("[启动] portHints=%v healthUrl=%q envKeys=%v", a.PortHints, a.HealthURL, envKeys))
	collector.Debug(fmt.Sprintf("[启动] 启动前系统监听端口数=%d urlDiscoverTimeout=%s grace=%ds",
		len(beforePorts), urlTimeout, int(gracePeriod/time.Second)))

	// 先占位 runState：日志回调可能在 spawn 返回前就打出 URL，不能丢。
	rs := &runState{collector: collector, cancel: cancel, exitDone: make(chan struct{})}
	l.mu.Lock()
	l.runs[appID] = rs
	l.mu.Unlock()

	// 多服务：每个本地 URL / 裸端口都记入 candidate，供 discover 做 log-url 证据。
	// batch 里 Start-Process 拉起的前后端子进程不在 root 进程树里，只能靠日志证据发现。
	addCandidate := func(u string) {
		if u == "" {
			return
		}
		l.mu.Lock()
		dup := false
		for _, old := range rs.candidateURLs {
			if old == u {
				dup = true
				break
			}
		}
		if !dup {
			rs.candidateURLs = append(rs.candidateURLs, u)
		}
		l.mu.Unlock()
		if !dup {
			collector.Info(fmt.Sprintf("[日志解析] 发现 URL: %s", u))
		}
	}
	collector.OnURL = func(_ *logbus.Collector, u string) { addCandidate(u) }
	collector.OnEvent = func(_ *logbus.Collector, ev logbus.Event) {
		// "on port 9100" 这类只有端口、没有完整 URL 的行
		if ev.Kind == logbus.EventPortListen && ev.Port > 0 {
			addCandidate(fmt.Sprintf("http://localhost:%d", ev.Port))
		}
	}

	// 用 ConPTY 启动（提供完整伪控制台，让 timeout/pause/Ctrl+C 等正常工作）。
	// ConPTY 不区分 stdout/stderr，统一走 OnStdout（logbus 会按内容推断级别）。
	collector.Info("[启动] 正在创建进程（当前用户 ConPTY，无需管理员权限）...")
	handle, err := l.startProcess(runCtx, &proc.PreparedCommand{Cmd: cmd, Args: args, Cwd: cwd, Env: env},
		collector.OnStdout)
	if err != nil {
		collector.Error(fmt.Sprintf("[启动] 创建进程失败: %v", err))
		if l.Diagnostics != nil {
			l.Diagnostics.Record(diagnostics.Event{
				AppID: appID, RunID: runID, Kind: "error", Severity: "error", Source: "launcher",
				Operation: "app.start", Stage: "spawn", Status: "failed", ErrorCode: "spawn_failed",
				DurationMS: time.Since(nowTime).Milliseconds(), Message: err.Error(),
			})
		}
		code := -1
		_ = l.Store.UpdateRunStatus(runID, app.StatusFailed, &code)
		_ = l.Store.TouchAppRuntime(appID, nowStr, "", app.StatusFailed)
		l.mu.Lock()
		delete(l.runs, appID)
		l.mu.Unlock()
		cancel()
		return fmt.Errorf("spawn: %w", err)
	}

	pid := handle.PID()
	_ = l.Store.UpdateRunPID(runID, pid, pid)

	rt := &app.Runtime{
		AppID: appID, RunID: runID, PID: pid, RootPID: pid,
		RootCreated: handle.IdentityCreated(),
		Status:      app.StatusStarting, StartedAt: nowTime,
	}
	l.Manager.Registry.Set(appID, rt)
	_ = l.Store.TouchAppRuntime(appID, nowStr, "", app.StatusStarting)

	l.mu.Lock()
	rs.handle = handle
	rs.rootPID = pid
	l.mu.Unlock()

	collector.Info(fmt.Sprintf("[启动] 进程已创建 pid=%d rootPid=%d status=starting", pid, pid))
	if l.Diagnostics != nil {
		l.Diagnostics.Record(diagnostics.Event{
			AppID: appID, RunID: runID, Kind: "performance", Severity: "info", Source: "launcher",
			Operation: "app.spawn", Stage: "spawn", Status: "succeeded",
			DurationMS: time.Since(nowTime).Milliseconds(), Message: "项目进程已创建",
			Context: map[string]any{"pid": pid},
		})
	}

	// 后台监听进程退出
	go func() { defer close(rs.exitDone); l.watchExit(appID, rt, handle, cancel, collector) }()

	// 后台：多服务发现 + 健康检查 + 综合状态（替代旧的单服务 watchHealth/observePorts）
	hintedPorts := map[int]bool{}
	for _, port := range a.PortHints {
		hintedPorts[port] = true
	}
	go l.watchServices(appID, rt, beforePorts, manualRoles, probe.DeclaredRoles(a.EntryScript), hintedPorts, collector, readiness)

	return nil
}

// durationFromSetting 读 settings 里的秒数配置。
func durationFromSetting(s *store.Store, key string, def int) time.Duration {
	v := s.GetSetting(key, strconv.Itoa(def))
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		n = def
	}
	return time.Duration(n) * time.Second
}
