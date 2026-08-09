// Package launcher 是编排层：把 adapter / proc / logbus / probe / store / 状态机串成一个完整的
// 启动—观测—停止闭环。它是 api 层之下、各基础模块之上的"指挥者"。
package launcher

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
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
	startProcess func(context.Context, *proc.PreparedCommand, func(string)) (*proc.Handle, error)

	// 停止用配置（从 settings 读）
	graceSeconds int
	urlTimeout   time.Duration

	// 每个 appID 的活跃编排上下文，停止时取用
	mu   sync.Mutex
	runs map[string]*runState
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
	_ = reconcileDeclaredRoles(s)
	l := &Launcher{
		Store: s, Manager: m, Hub: hub, Registry: reg,
		runs:         map[string]*runState{},
		startProcess: proc.StartWithConPTY,
	}
	l.graceSeconds, _ = strconv.Atoi(s.GetSetting("grace_period_seconds", "8"))
	l.urlTimeout = durationFromSetting(s, "url_discover_timeout_seconds", 30)
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

	// 启动前：清理该 app 上次运行遗留的 service 记录（避免累积重复）
	oldSvcs, _ := l.Store.ListServicesByApp(appID)
	// 快照用户手动标注的角色（按端口），以便重启后还原到新 run 的服务上。
	manualRoles := map[int]string{}
	for _, s := range oldSvcs {
		if s.RoleSource == store.RoleSourceManual && s.Role != "" {
			manualRoles[s.Port] = s.Role
		}
	}
	_ = l.Store.DeleteServicesByApp(appID)

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
		len(beforePorts), l.urlTimeout, l.graceSeconds))

	// 先占位 runState：日志回调可能在 spawn 返回前就打出 URL，不能丢。
	rs := &runState{collector: collector, cancel: cancel}
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
		Status: app.StatusStarting, StartedAt: nowTime,
	}
	l.Manager.Registry.Set(appID, rt)
	_ = l.Store.TouchAppRuntime(appID, nowStr, "", app.StatusStarting)

	l.mu.Lock()
	rs.handle = handle
	rs.rootPID = pid
	l.mu.Unlock()

	collector.Info(fmt.Sprintf("[启动] 进程已创建 pid=%d rootPid=%d status=starting", pid, pid))

	// 后台监听进程退出
	go l.watchExit(appID, rt, handle, cancel, collector)

	// 后台：多服务发现 + 健康检查 + 综合状态（替代旧的单服务 watchHealth/observePorts）
	hintedPorts := map[int]bool{}
	for _, port := range a.PortHints {
		hintedPorts[port] = true
	}
	go l.watchServices(appID, rt, beforePorts, manualRoles, probe.DeclaredRoles(a.EntryScript), hintedPorts, collector)

	return nil
}

// watchServices 多服务监测核心循环。
// 周期性扫描本进程树新增的所有监听端口，每个端口记为一个 service 并做健康检查。
// 项目状态按木桶原则综合：所有 service healthy => running；任一 unhealthy => degraded。
//
// 与旧逻辑区别：不再只盯第一个端口，而是发现全部端口，每个独立判定，综合出项目状态。
func (l *Launcher) watchServices(appID string, rt *app.Runtime, before []probe.PortListener, manualRoles map[int]string, declaredRoles map[int]probe.Role, hintedPorts map[int]bool, col *logbus.Collector) {
	deadline := time.NewTimer(l.urlTimeout) // 发现窗口：超过这个时间仍无任何端口则标 degraded
	defer deadline.Stop()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	rejectedPorts := map[int]int{} // port -> PID；同一进程的非服务端口不重复探测
	scanRound := 0

	if col != nil {
		col.Info(fmt.Sprintf("[监测] 开始服务发现 tick=3s discoverTimeout=%s", l.urlTimeout))
		if len(declaredRoles) > 0 {
			parts := make([]string, 0, len(declaredRoles))
			for p, r := range declaredRoles {
				parts = append(parts, fmt.Sprintf("%d=%s", p, r))
			}
			sort.Strings(parts)
			col.Debug(fmt.Sprintf("[监测] 脚本声明角色: %s", strings.Join(parts, ", ")))
		}
		if len(hintedPorts) > 0 {
			col.Debug(fmt.Sprintf("[监测] portHints: %v", keysOfInt(hintedPorts)))
		}
	}

	for {
		select {
		case <-ticker.C:
			cur := rt.GetStatus()
			if cur == app.StatusStopped || cur == app.StatusFailed {
				return
			}
			scanRound++
			// 扫描新增端口，登记为 service
			l.discoverServices(appID, rt, before, manualRoles, declaredRoles, hintedPorts, rejectedPorts, col, scanRound)
			// 对所有 service 做健康检查，并综合出项目状态
			l.recheckAndAggregate(appID, rt, col)
		case <-deadline.C:
			// 发现窗口结束：若仍无任何 service 或全部不健康，按结果定 degraded
			if rt.GetStatus() == app.StatusStarting {
				svcs, _ := l.Store.ListServicesByRun(rt.RunID)
				if col != nil {
					if len(svcs) == 0 {
						col.Warn(fmt.Sprintf("[监测] 发现窗口已结束（%s）仍无任何服务端口，status 仍为 starting；将继续后台扫描", l.urlTimeout))
						col.Debug("[监测] 排查提示: 检查进程是否卡在依赖安装/编译；日志是否打印了 Local URL；端口是否被防火墙/绑定异常；cwd/cmd 是否正确")
					} else {
						col.Warn(fmt.Sprintf("[监测] 发现窗口已结束（%s）已登记 %d 个服务但尚未全部健康，保持/重算状态", l.urlTimeout, len(svcs)))
					}
				}
				l.recheckAndAggregate(appID, rt, col)
			}
			// 窗口结束后仍继续周期性复查（服务可能晚起），直到进程退出
		}
	}
}

// discoverServices 发现属于本 App 的所有监听端口，每个登记为一个 service（去重）。
//
// 三重确认策略（A + 进程树，覆盖进程树断裂的 batch 场景，且多项目不串）：
//
//	证据1（强）进程树归属：端口 PID 属于本进程树 → 直接纳入（spider 走这条）
//	证据2（中）日志 URL：日志里明确出现了该端口的 URL → 纳入（batch 走这条）
//	         但日志证据必须叠加"时间窗"约束：该端口是启动后才新出现的（不在 before 快照里），
//	         避免把别的项目早就占着的端口误收。
//
// 只有两个证据都不满足才跳过。这样：
//   - 进程树完整的项目（spider）→ 证据1 命中，准确
//   - 进程树断裂的项目（batch）→ 证据2 命中，仍能发现
//   - 无关端口 → 两证据都不满足，排除
//   - 多项目并存 → 各自日志只提自己的 URL，互不干扰
func (l *Launcher) discoverServices(appID string, rt *app.Runtime, before []probe.PortListener, manualRoles map[int]string, declaredRoles map[int]probe.Role, hintedPorts map[int]bool, rejectedPorts map[int]int, col *logbus.Collector, scanRound int) {
	clear(rejectedPorts) // 仅在本轮去重；慢启动服务必须在下一轮重新探测。
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
	candidateN := 0
	l.mu.Lock()
	if rs, ok := l.runs[appID]; ok {
		candidateN = len(rs.candidateURLs)
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

	// starting 阶段每轮打摘要；已 running 后降低频率（每 10 轮）
	curStatus := rt.GetStatus()
	shouldSummary := curStatus == app.StatusStarting || scanRound%10 == 1
	if col != nil && shouldSummary {
		alive := !isProcessGone(rt.RootPID)
		col.Debug(fmt.Sprintf("[发现#%d] status=%s rootAlive=%v treePIDs=%d systemListeners=%d logURLs=%d logPorts=%v",
			scanRound, curStatus, alive, len(treePIDs), len(all), candidateN, keysOfInt(logPorts)))
		if curStatus == app.StatusStarting && len(treePIDs) <= 1 && candidateN == 0 && scanRound >= 2 {
			col.Debug(fmt.Sprintf("[发现#%d] 进程树仅 root、日志未出现 URL——可能仍在装依赖/编译，或输出未走到 ConPTY", scanRound))
		}
	}

	registered := 0
	for _, p := range all {
		if l.Store.HasService(rt.RunID, p.Port) {
			continue
		}
		if rejectedPID, ok := rejectedPorts[p.Port]; ok && rejectedPID == p.PID {
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
		if !isServicePort(p.Port, logEvidence, declaredRoles[p.Port], hintedPorts[p.Port], nil) {
			root := l.probeRole(url)
			if !isServicePort(p.Port, logEvidence, declaredRoles[p.Port], hintedPorts[p.Port], root) {
				rejectedPorts[p.Port] = p.PID
				if col != nil {
					col.Debug(fmt.Sprintf("[发现] 跳过端口 %d pid=%d（树归属=%v 日志证据=%v 但非服务端口）", p.Port, p.PID, ownedByTree, logEvidence))
				}
				continue
			}
		}
		// 初判角色：端口(DB 端口高置信) + 日志特征(低置信)。
		logs, _ := l.Store.SearchLogs(rt.RunID, strconv.Itoa(p.Port), 20)
		logHints := make([]string, 0, len(logs))
		for _, entry := range logs {
			logHints = append(logHints, entry.Text)
		}
		role, conf := probe.Classify(probe.ClassifyInput{Port: p.Port, DeclaredRole: declaredRoles[p.Port], LogHints: logHints})
		roleStr := string(role)
		roleSource := store.RoleSourceAuto
		// 还原用户上次手动标注的角色（按端口匹配，重启后保留）。
		if manualRole, ok := manualRoles[p.Port]; ok {
			roleStr = manualRole
			roleSource = store.RoleSourceManual
			conf = probe.ConfHigh // manual 视为高置信，跳过异步升级
		}
		evidence := "process-tree"
		if logEvidence && ownedByTree {
			evidence = "process-tree+log"
		} else if logEvidence {
			evidence = "log-url"
		}
		svc := &store.AppService{
			ID:         app.NewID(),
			AppID:      appID,
			AppRunID:   rt.RunID,
			Port:       p.Port,
			URL:        url,
			Health:     "unknown",
			DetectedAt: time.Now().UTC().Format(time.RFC3339),
			Role:       roleStr,
			RoleSource: roleSource,
		}
		_ = l.Store.UpsertService(svc)
		_ = l.Store.InsertPort(rt.RunID, p.Port, "tcp")
		registered++
		if col != nil {
			col.Info(fmt.Sprintf("[发现] 登记服务 port=%d url=%s role=%s source=%s evidence=%s pid=%d",
				p.Port, url, roleStr, roleSource, evidence, p.PID))
		}
		if l.Hub != nil {
			l.Hub.BroadcastURL(appID, url, []int{p.Port})
		}
		a, _ := l.Store.GetApp(appID)
		if a != nil && a.LastURL == "" {
			_ = l.Store.TouchAppRuntime(appID, "", url, "")
		}
		// 异步用 HTTP 响应头升级 role（仅当当前置信度不足 High，即非 DB 端口/非 manual）。
		if conf < probe.ConfHigh {
			go l.refineRoleWithProbe(appID, svc.ID, rt.RunID, url)
		}
	}
	if col != nil && registered > 0 {
		col.Debug(fmt.Sprintf("[发现#%d] 本轮新登记 %d 个服务", scanRound, registered))
	}
}

func isServicePort(port int, logEvidence bool, declaredRole probe.Role, hinted bool, root *probe.HealthResult) bool {
	if logEvidence || declaredRole != "" || hinted || (root != nil && root.StatusCode > 0) {
		return true
	}
	role, _ := probe.Classify(probe.ClassifyInput{Port: port})
	return role == probe.RoleDatabase
}

// recheckAndAggregate 对所有 service 做健康检查，按木桶原则综合出项目状态。
func (l *Launcher) recheckAndAggregate(appID string, rt *app.Runtime, col *logbus.Collector) {
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
		prev := svc.Health
		hr := l.probeService(svc.URL)
		ok := hr != nil && hr.Reachable
		if ok {
			_ = l.Store.UpdateServiceHealth(svc.ID, "healthy", now)
			healthy++
			if col != nil && prev != "healthy" {
				detail := ""
				if hr != nil {
					detail = fmt.Sprintf(" status=%d server=%q title=%q", hr.StatusCode, hr.Server, hr.Title)
				}
				col.Info(fmt.Sprintf("[健康] %s port=%d %s → healthy%s", svc.URL, svc.Port, prev, detail))
			}
			// 顺带：用响应头升级 auto 服务角色（仅当前还是 unknown 时，避免覆盖已有判定）。
			if svc.RoleSource == store.RoleSourceAuto && svc.Role == store.RoleUnknown {
				l.tryUpgradeRole(svc)
			}
		} else {
			_ = l.Store.UpdateServiceHealth(svc.ID, "unhealthy", now)
			unhealthy++
			if col != nil && prev != "unhealthy" {
				col.Warn(fmt.Sprintf("[健康] %s port=%d %s → unhealthy（不可达）", svc.URL, svc.Port, prev))
			}
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
			if col != nil {
				col.Warn(fmt.Sprintf("[状态] %s → degraded（healthy=%d unhealthy=%d）", cur, healthy, unhealthy))
			}
			l.Manager.Transition(rt, app.StatusDegraded, nil)
		}
	case healthy > 0 && unhealthy == 0:
		// 全部健康 => running
		if cur != app.StatusRunning {
			if col != nil {
				col.Info(fmt.Sprintf("[状态] %s → running（全部 %d 个服务健康）", cur, healthy))
			}
			l.Manager.Transition(rt, app.StatusRunning, nil)
		}
	default:
		// 全 unknown（刚发现还没探）：保持 starting
		if col != nil && cur == app.StatusStarting {
			col.Debug(fmt.Sprintf("[状态] 保持 starting（services=%d healthy=%d unhealthy=%d）", len(svcs), healthy, unhealthy))
		}
	}
}

// probeService 单次健康检查某 URL，返回含响应头/Title 的结果（供角色识别复用）。
// 返回 nil 表示 URL 为空；不可达时返回的 HealthResult.Reachable=false。
func (l *Launcher) probeService(url string) *probe.HealthResult {
	if url == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return probe.CheckHealth(cctx, url)
}

func (l *Launcher) probeRole(url string) *probe.HealthResult {
	if url == "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return probe.CheckRoot(cctx, url)
}

// healthResultToHeaders 把 HealthResult 的响应头字段拼成 ClassifyInput.Headers(键大小写不敏感)。
func healthResultToHeaders(hr *probe.HealthResult) map[string]string {
	if hr == nil {
		return nil
	}
	h := map[string]string{}
	if hr.Server != "" {
		h["Server"] = hr.Server
	}
	if hr.PoweredBy != "" {
		h["X-Powered-By"] = hr.PoweredBy
	}
	return h
}

// refineRoleWithProbe 异步用 HTTP 响应头/Title 重新 classify，仅在 role_source=auto 时升级。
// runID 用于升级成功后广播刷新前端。
func (l *Launcher) refineRoleWithProbe(appID, serviceID, runID, url string) {
	hr := l.probeRole(url)
	if hr == nil {
		return
	}
	role, conf := probe.Classify(probe.ClassifyInput{
		Headers: healthResultToHeaders(hr),
		Title:   hr.Title,
		BodyCT:  hr.ContentType,
		Body:    hr.Body,
	})
	// 仅中置信度(响应头/title/CT)以上才升级；低置信度(日志)不值得覆盖。
	if conf < probe.ConfMedium {
		return
	}
	if updated, err := l.Store.UpdateServiceRoleIfAuto(serviceID, string(role)); err != nil || !updated {
		return // 未实际更新(行已删/已锁定/值未变):不广播,避免 ghost broadcast
	}
	// 广播刷新前端（复用 app:services）。
	if l.Hub != nil {
		if svcs, err := l.Store.ListServicesByRun(runID); err == nil {
			l.Hub.BroadcastServices(appID, runID, svcs)
		}
	}
}

// tryUpgradeRole 在健康复查命中时用响应头升级 auto 且 unknown 的服务角色。
func (l *Launcher) tryUpgradeRole(svc *store.AppService) {
	hr := l.probeRole(svc.URL)
	if hr == nil {
		return
	}
	role, conf := probe.Classify(probe.ClassifyInput{
		Headers: healthResultToHeaders(hr),
		Title:   hr.Title,
		BodyCT:  hr.ContentType,
		Body:    hr.Body,
	})
	if conf < probe.ConfMedium {
		return
	}
	_, _ = l.Store.UpdateServiceRoleIfAuto(svc.ID, string(role))
}

// watchExit 等进程退出，更新状态。
func (l *Launcher) watchExit(appID string, rt *app.Runtime, handle *proc.Handle, cancel context.CancelFunc, col *logbus.Collector) {
	exitCode, waitErr := handle.Wait()
	cancel()
	_ = handle.Close()

	// 清理运行态（先取 col 兜底：参数 col 一般可用；runs 删除后 collectorOf 会空）
	l.mu.Lock()
	delete(l.runs, appID)
	l.mu.Unlock()

	cur := rt.GetStatus()
	if col != nil {
		if waitErr != nil {
			col.Warn(fmt.Sprintf("[退出] Wait 返回错误: %v（exitCode=%d prevStatus=%s）", waitErr, exitCode, cur))
		} else {
			col.Info(fmt.Sprintf("[退出] 进程结束 exitCode=%d prevStatus=%s", exitCode, cur))
		}
	}

	// 若是用户主动停止（status=stopping）则终态 stopped；否则按退出码判定
	var next string
	if cur == app.StatusStopping {
		next = app.StatusStopped
		l.Manager.Transition(rt, app.StatusStopped, &exitCode)
	} else if exitCode == 0 {
		next = app.StatusStopped
		l.Manager.Transition(rt, app.StatusStopped, &exitCode)
	} else {
		next = app.StatusFailed
		if col != nil {
			col.Error(fmt.Sprintf("[状态] %s → failed（非零退出码 %d）", cur, exitCode))
		}
		l.Manager.Transition(rt, app.StatusFailed, &exitCode)
	}
	if col != nil && next == app.StatusStopped {
		col.Info(fmt.Sprintf("[状态] %s → stopped", cur))
	}
	l.Manager.Registry.Remove(appID)
}

// Stop 停止一个 app。分级：Ctrl-Break -> grace -> Terminate(taskkill /t /f) -> 端口确认。
func (l *Launcher) Stop(appID string) error {
	l.mu.Lock()
	rs, ok := l.runs[appID]
	l.mu.Unlock()
	if !ok {
		if rt, ok := l.Manager.Registry.Get(appID); ok {
			l.finishStopped(appID, rt, 0)
			return nil
		}
		return nil // 幂等：应用已停止，直接返回成功
	}
	col := rs.collector
	rt, _ := l.Manager.Registry.Get(appID)
	if rt != nil {
		if col != nil {
			col.Info(fmt.Sprintf("[停止] 开始停止 pid=%d grace=%ds", rs.rootPID, l.graceSeconds))
		}
		l.Manager.Transition(rt, app.StatusStopping, nil)
	}

	// 1) 优雅：Ctrl-Break / Ctrl+C
	if err := rs.handle.GracefulStop(); err != nil {
		if col != nil {
			col.Warn(fmt.Sprintf("[停止] 优雅停止信号发送失败: %v", err))
		}
	} else if col != nil {
		col.Debug("[停止] 已发送优雅停止信号（Ctrl+C）")
	}

	// 2) 等待 grace period
	grace := time.Duration(l.graceSeconds) * time.Second
	waitCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if exited := isProcessGone(rs.rootPID); exited {
			if col != nil {
				col.Info("[停止] 进程在 grace 期内已退出")
			}
			if rt != nil {
				l.finishStopped(appID, rt, 0)
			}
			return nil
		}
		select {
		case <-waitCtx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}

	// 3) 强制：taskkill /t /f（terminateTree 内部）
	if col != nil {
		col.Warn("[停止] grace 超时，强制终止进程树")
	}
	if err := rs.handle.Terminate(); err != nil {
		if col != nil {
			col.Warn(fmt.Sprintf("[停止] 强制终止调用异常: %v（将继续收敛状态）", err))
		}
	}
	// 等待进程彻底退出（最多再等 3 秒）
	for i := 0; i < 10; i++ {
		time.Sleep(300 * time.Millisecond)
		if isProcessGone(rs.rootPID) {
			break
		}
	}
	if col != nil {
		if isProcessGone(rs.rootPID) {
			col.Info("[停止] 强制终止后进程已消失")
		} else {
			col.Error(fmt.Sprintf("[停止] 强制终止后进程仍存活 pid=%d，仍将状态收敛为 stopped", rs.rootPID))
		}
	}
	// 无论进程是否已退出，都收敛状态，避免卡在 stopping
	if rt != nil {
		l.finishStopped(appID, rt, 0)
	}
	return nil
}

func (l *Launcher) finishStopped(appID string, rt *app.Runtime, exitCode int) {
	l.Manager.Transition(rt, app.StatusStopped, &exitCode)
	l.Manager.Registry.Remove(appID)
	l.mu.Lock()
	delete(l.runs, appID)
	l.mu.Unlock()
}

// StopAll 停止所有正在运行的 app（用于退出前清理）。并发停止，等待全部完成或超时。
// 返回成功停止的数量。
func (l *Launcher) StopAll() int {
	runnings := l.Manager.Registry.All()
	if len(runnings) == 0 {
		return 0
	}
	type result struct{ ok bool }
	done := make(chan result, len(runnings))
	for _, rt := range runnings {
		appID := rt.AppID
		go func() {
			// Stop 对不在 runs 里的会报错，忽略——以 Registry 状态为准
			if err := l.Stop(appID); err == nil {
				done <- result{ok: true}
			} else {
				done <- result{ok: false}
			}
		}()
	}
	// 总超时：单个 Stop 最多 grace+几秒，这里给充裕上限
	deadline := time.NewTimer(time.Duration(l.graceSeconds)*time.Second + 5*time.Second)
	defer deadline.Stop()
	stopped := 0
	for i := 0; i < len(runnings); i++ {
		select {
		case r := <-done:
			if r.ok {
				stopped++
			}
		case <-deadline.C:
			return stopped // 超时，返回已停止的数量
		}
	}
	return stopped
}

// Restart = Stop + Start。
func (l *Launcher) Restart(ctx context.Context, appID string) error {
	if l.Manager.Registry.IsRunning(appID) {
		if err := l.Stop(appID); err != nil {
			return err
		}
		// 等待状态收敛到终态（Stop 内部的 watchExit 可能还在异步收尾）
		deadline := time.Now().Add(10 * time.Second)
		for l.Manager.Registry.IsRunning(appID) && time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
		}
		if l.Manager.Registry.IsRunning(appID) {
			return fmt.Errorf("stop timed out, cannot restart: %s", appID)
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
