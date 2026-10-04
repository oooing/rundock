package launcher

import (
	"fmt"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
	"sort"
	"strconv"
	"strings"
	"time"
)

// watchServices 多服务监测核心循环。
// 周期性扫描本进程树新增的所有监听端口，每个端口记为一个 service 并做健康检查。
// 项目状态按木桶原则综合：所有 service healthy => running；任一 unhealthy => degraded。
//
// 与旧逻辑区别：不再只盯第一个端口，而是发现全部端口，每个独立判定，综合出项目状态。
func (l *Launcher) watchServices(appID string, rt *app.Runtime, before []probe.PortListener, manualRoles map[int]string, declaredRoles map[int]probe.Role, hintedPorts map[int]bool, col *logbus.Collector, readiness *startupReadiness) {
	readiness.deadline = rt.StartedAt.Add(readiness.timeout)
	deadline := time.NewTimer(time.Until(readiness.deadline)) // 显式就绪声明的等待时限
	defer deadline.Stop()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	rejectedPorts := map[int]int{}             // port -> PID；同一进程的非服务端口不重复探测
	checks := map[string]*serviceHealthCheck{} // 仅由本次运行的监测循环使用
	scanRound := 0
	for port, url := range readiness.urls {
		hintedPorts[port] = true
		if col != nil {
			col.Info("[就绪] 必须就绪: " + url)
		}
	}

	if col != nil {
		col.Info(fmt.Sprintf("[监测] 开始服务发现 tick=3s discoverTimeout=%s", readiness.timeout))
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
			if cur == app.StatusStopped || cur == app.StatusFailed || cur == app.StatusStopping {
				return
			}
			scanRound++
			// 扫描新增端口，登记为 service
			l.discoverServices(appID, rt, before, manualRoles, declaredRoles, hintedPorts, rejectedPorts, col, scanRound)
			// 对所有 service 做健康检查，并综合出项目状态
			l.recheckAndAggregate(appID, rt, col, checks, time.Now(), readiness)
		case <-deadline.C:
			l.recheckAndAggregate(appID, rt, col, checks, time.Now(), readiness)
			if col != nil && len(readiness.urls) == 0 && rt.GetStatus() == app.StatusStarting {
				col.Warn("[监测] 尚未发现健康服务，继续后台扫描；可用 rundock:ready 明确就绪条件")
			}
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

const (
	healthyCheckInterval   = 15 * time.Second
	healthRetryInterval    = 3 * time.Second
	healthFailureThreshold = 2
)

// serviceHealthCheck 的重试计数和时间只属于一次运行，不跨重启或项目共享。
type serviceHealthCheck struct {
	nextCheck time.Time
	failures  int
}
