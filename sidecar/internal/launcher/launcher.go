// Package launcher 是编排层：把 adapter / proc / logbus / probe / store / 状态机串成一个完整的
// 启动—观测—停止闭环。它是 api 层之下、各基础模块之上的"指挥者"。
package launcher

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/proc"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

// Launcher 持有所有依赖，提供 Start/Stop/Restart。
type Launcher struct {
	Store    *store.Store
	Manager  *app.Manager
	Hub      *logbus.Hub
	Registry *adapter.Registry

	// 停止用配置（从 settings 读）
	graceSeconds int
	urlTimeout   time.Duration

	// 每个 appID 的活跃编排上下文，停止时取用
	mu      sync.Mutex
	runs    map[string]*runState
}

// runState 一个 app 当前启动的运行态（进程句柄 + collector + cancel）。
type runState struct {
	handle        *proc.Handle
	collector     *logbus.Collector
	cancel        context.CancelFunc
	rootPID       int
	candidateURLs []string // 从日志解析到的候选 URL（供服务发现优先使用）
}

// New 创建 launcher，并从 settings 读入 grace period / url timeout。
func New(s *store.Store, m *app.Manager, hub *logbus.Hub, reg *adapter.Registry) *Launcher {
	l := &Launcher{
		Store: s, Manager: m, Hub: hub, Registry: reg,
		runs: map[string]*runState{},
	}
	l.graceSeconds, _ = strconv.Atoi(s.GetSetting("grace_period_seconds", "8"))
	l.urlTimeout = durationFromSetting(s, "url_discover_timeout_seconds", 30)
	return l
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

	// prepare：优先用 App 自存的 cmd/args（已确认的配置），否则走适配器重新 prepare
	cmd, args := a.Cmd, a.Args
	cwd, env := a.Cwd, a.Env
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
		// 回写，避免下次再 prepare
		a.Cmd, a.Args, a.Cwd, a.Env = cmd, args, cwd, env
		_ = l.Store.UpdateApp(a)
	}

	// 启动前：清理该 app 上次运行遗留的 service 记录（避免累积重复）
	// 同时收集历史端口（该 app 之前用过的端口），用于启动前清端口
	oldSvcs, _ := l.Store.ListServicesByApp(appID)
	historicPorts := make([]int, 0, len(oldSvcs))
	for _, s := range oldSvcs {
		historicPorts = append(historicPorts, s.Port)
	}
	_ = l.Store.DeleteServicesByApp(appID)

	// 启动前端口快照（用于事后只看本进程树新增端口）
	beforePorts := probe.SnapshotListeners()

	runCtx, cancel := context.WithCancel(context.Background())
	runID := app.NewRunID()
	collector := logbus.NewCollector(l.Store, l.Hub, appID, runID)

	// 启动前写一条提示日志
	collector.EmitLog("event", "info", fmt.Sprintf("启动 %s（%s %v）", a.Name, cmd, args))

	// 启动前自动清理被占用的端口（该 app 历史用过的 + 脚本声明的 portHints）
	portsToClear := append(historicPorts, a.PortHints...)
	if cleared := clearPortsIfOccupied(portsToClear); len(cleared) > 0 {
		for _, msg := range cleared {
			collector.EmitLog("event", "warn", msg)
		}
		// 清完端口后重新拍快照（端口已释放）
		beforePorts = probe.SnapshotListeners()
	}

	// 关键：在 spawn 之前设置好日志回调（避免 watchServices/watchExit 提前执行时回调为 nil）
	collector.OnURL = func(c *logbus.Collector, u string) {
		l.mu.Lock()
		if rs, ok := l.runs[appID]; ok {
			rs.candidateURLs = append(rs.candidateURLs, u)
		}
		l.mu.Unlock()
	}

	// 用 ConPTY 启动（提供完整伪控制台，让 timeout/pause/Ctrl+C 等正常工作）。
	// ConPTY 不区分 stdout/stderr，统一走 OnStdout（logbus 会按内容推断级别）。
	handle, err := proc.StartWithConPTY(runCtx, &proc.PreparedCommand{Cmd: cmd, Args: args, Cwd: cwd, Env: env},
		collector.OnStdout)
	if err != nil {
		cancel()
		return fmt.Errorf("spawn: %w", err)
	}

	// 记录 run
	nowStr := time.Now().UTC().Format(time.RFC3339)
	nowTime := time.Now()
	rt := &app.Runtime{
		AppID: appID, RunID: runID, PID: handle.PID(), RootPID: handle.PID(),
		Status: app.StatusStarting, StartedAt: nowTime,
	}
	_ = l.Store.CreateRun(&store.AppRun{
		ID: runID, AppID: appID, PID: handle.PID(), RootPID: handle.PID(),
		Status: app.StatusStarting, StartedAt: nowStr,
	})
	l.Manager.Registry.Set(appID, rt)
	_ = l.Store.TouchAppRuntime(appID, nowStr, "", app.StatusStarting)

	l.mu.Lock()
	l.runs[appID] = &runState{handle: handle, collector: collector, cancel: cancel, rootPID: handle.PID()}
	l.mu.Unlock()

	// 后台监听进程退出
	go l.watchExit(appID, rt, handle, cancel)

	// 后台：多服务发现 + 健康检查 + 综合状态（替代旧的单服务 watchHealth/observePorts）
	go l.watchServices(appID, rt, beforePorts)

	return nil
}

// watchServices 多服务监测核心循环。
// 周期性扫描本进程树新增的所有监听端口，每个端口记为一个 service 并做健康检查。
// 项目状态按木桶原则综合：所有 service healthy => running；任一 unhealthy => degraded。
//
// 与旧逻辑区别：不再只盯第一个端口，而是发现全部端口，每个独立判定，综合出项目状态。
func (l *Launcher) watchServices(appID string, rt *app.Runtime, before []probe.PortListener) {
	deadline := time.NewTimer(l.urlTimeout) // 发现窗口：超过这个时间仍无任何端口则标 degraded
	defer deadline.Stop()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cur := rt.GetStatus()
			if cur == app.StatusStopped || cur == app.StatusFailed {
				return
			}
			// 扫描新增端口，登记为 service
			l.discoverServices(appID, rt, before)
			// 对所有 service 做健康检查，并综合出项目状态
			l.recheckAndAggregate(appID, rt)
		case <-deadline.C:
			// 发现窗口结束：若仍无任何 service 或全部不健康，按结果定 degraded
			if rt.GetStatus() == app.StatusStarting {
				l.recheckAndAggregate(appID, rt)
			}
			// 窗口结束后仍继续周期性复查（服务可能晚起），直到进程退出
		}
	}
}

// discoverServices 发现属于本 App 的所有监听端口，每个登记为一个 service（去重）。
//
// 三重确认策略（A + 进程树，覆盖进程树断裂的 batch 场景，且多项目不串）：
//  证据1（强）进程树归属：端口 PID 属于本进程树 → 直接纳入（spider 走这条）
//  证据2（中）日志 URL：日志里明确出现了该端口的 URL → 纳入（batch 走这条）
//           但日志证据必须叠加"时间窗"约束：该端口是启动后才新出现的（不在 before 快照里），
//           避免把别的项目早就占着的端口误收。
// 只有两个证据都不满足才跳过。这样：
//   - 进程树完整的项目（spider）→ 证据1 命中，准确
//   - 进程树断裂的项目（batch）→ 证据2 命中，仍能发现
//   - 无关端口 → 两证据都不满足，排除
//   - 多项目并存 → 各自日志只提自己的 URL，互不干扰
func (l *Launcher) discoverServices(appID string, rt *app.Runtime, before []probe.PortListener) {
	all := probe.SnapshotListeners()

	// === 证据1：进程树归属 ===
	treePIDs := collectProcessTree(rt.RootPID)
	treeSet := map[int]bool{}
	for _, p := range treePIDs {
		treeSet[p] = true
	}

	// === 证据2：日志里出现的端口 URL（时间窗内）===
	// 从 runState 收集的 candidateURLs 提取端口
	logPorts := map[int]bool{}
	l.mu.Lock()
	if rs, ok := l.runs[appID]; ok {
		for _, u := range rs.candidateURLs {
			if port := portFromURLStr(u); port > 0 {
				logPorts[port] = true
			}
		}
	}
	l.mu.Unlock()

	// before 快照里的端口集合（时间窗下界：这些端口是"早就存在"的）
	beforePorts := map[int]bool{}
	for _, b := range before {
		beforePorts[b.Port] = true
	}

	for _, p := range all {
		if l.Store.HasService(rt.RunID, p.Port) {
			continue
		}
		// 证据1：端口属于本进程树
		ownedByTree := treeSet[p.PID]
		// 证据2：日志提到该端口，且是启动后新出现的（时间窗约束）
		inLog := logPorts[p.Port]
		isNew := !beforePorts[p.Port] // 不在启动前快照里 = 启动后新出现
		logEvidence := inLog && isNew

		if !ownedByTree && !logEvidence {
			continue
		}
		url := probe.JoinHostPort("localhost", p.Port)
		svc := &store.AppService{
			ID:         app.NewID(),
			AppID:      appID,
			AppRunID:   rt.RunID,
			Port:       p.Port,
			URL:        url,
			Health:     "unknown",
			DetectedAt: time.Now().UTC().Format(time.RFC3339),
		}
		_ = l.Store.UpsertService(svc)
		_ = l.Store.InsertPort(rt.RunID, p.Port, "tcp")
		if l.Hub != nil {
			l.Hub.BroadcastURL(appID, url, []int{p.Port})
		}
		a, _ := l.Store.GetApp(appID)
		if a != nil && a.LastURL == "" {
			_ = l.Store.TouchAppRuntime(appID, "", url, "")
		}
	}
}

// recheckAndAggregate 对所有 service 做健康检查，按木桶原则综合出项目状态。
func (l *Launcher) recheckAndAggregate(appID string, rt *app.Runtime) {
	svcs, err := l.Store.ListServicesByRun(rt.RunID)
	if err != nil || len(svcs) == 0 {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	healthy, unhealthy := 0, 0
	for _, svc := range svcs {
		// 已 healthy 的不重复探（省负载）；unhealthy/unknown 的复查
		if svc.Health == "healthy" {
			healthy++
			continue
		}
		ok := l.probeService(svc.URL)
		if ok {
			_ = l.Store.UpdateServiceHealth(svc.ID, "healthy", now)
			healthy++
		} else {
			_ = l.Store.UpdateServiceHealth(svc.ID, "unhealthy", now)
			unhealthy++
		}
	}

	// 广播 service 状态变更
	if l.Hub != nil {
		updated, _ := l.Store.ListServicesByRun(rt.RunID)
		l.Hub.BroadcastServices(appID, rt.RunID, updated)
	}

	// 木桶原则综合项目状态
	cur := rt.GetStatus()
	if cur == app.StatusStopped || cur == app.StatusFailed || cur == app.StatusStopping {
		return
	}
	switch {
	case unhealthy > 0:
		// 任一不健康 => degraded
		if cur != app.StatusDegraded {
			l.Manager.Transition(rt, app.StatusDegraded, nil)
		}
	case healthy > 0 && unhealthy == 0:
		// 全部健康 => running
		if cur != app.StatusRunning {
			l.Manager.Transition(rt, app.StatusRunning, nil)
		}
	default:
		// 全 unknown（刚发现还没探）：保持 starting
	}
}

// probeService 单次健康检查某 URL。
func (l *Launcher) probeService(url string) bool {
	if url == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	confirmed, _ := probe.ConfirmReachable(cctx, []string{url}, 4*time.Second)
	return confirmed != ""
}

// watchExit 等进程退出，更新状态。
func (l *Launcher) watchExit(appID string, rt *app.Runtime, handle *proc.Handle, cancel context.CancelFunc) {
	exitCode, _ := handle.Wait()
	cancel()
	_ = handle.Close()

	// 清理运行态
	l.mu.Lock()
	delete(l.runs, appID)
	l.mu.Unlock()

	cur := rt.GetStatus()
	// 若是用户主动停止（status=stopping）则终态 stopped；否则按退出码判定
	if cur == app.StatusStopping {
		l.Manager.Transition(rt, app.StatusStopped, &exitCode)
	} else if exitCode == 0 {
		l.Manager.Transition(rt, app.StatusStopped, &exitCode)
	} else {
		l.Manager.Transition(rt, app.StatusFailed, &exitCode)
	}
	l.Manager.Registry.Remove(appID)
}

// Stop 停止一个 app。分级：Ctrl-Break -> grace -> Terminate(taskkill /t /f) -> 端口确认。
func (l *Launcher) Stop(appID string) error {
	l.mu.Lock()
	rs, ok := l.runs[appID]
	l.mu.Unlock()
	if !ok {
		return fmt.Errorf("app not running: %s", appID)
	}
	rt, _ := l.Manager.Registry.Get(appID)
	if rt != nil {
		l.Manager.Transition(rt, app.StatusStopping, nil)
	}

	// 1) 优雅：Ctrl-Break
	_ = rs.handle.GracefulStop()

	// 2) 等待 grace period
	grace := time.Duration(l.graceSeconds) * time.Second
	waitCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if exited := isProcessGone(rs.rootPID); exited {
			return nil
		}
		select {
		case <-waitCtx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}

	// 3) 强制：taskkill /t /f（terminateTree 内部）
	if err := rs.handle.Terminate(); err != nil {
		// 仍尝试取消上下文
	}
	// 再给一点时间收敛
	time.Sleep(500 * time.Millisecond)
	return nil
}

// Restart = Stop + Start。
func (l *Launcher) Restart(ctx context.Context, appID string) error {
	if l.Manager.Registry.IsRunning(appID) {
		if err := l.Stop(appID); err != nil {
			return err
		}
		// 等端口释放
		waitPortRelease(appID, 5*time.Second)
	}
	return l.Start(ctx, appID)
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
