package launcher

import (
	"context"
	"fmt"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
	"time"
)

// recheckAndAggregate 持续复查 service，并按已确认的健康状态综合项目状态。
func (l *Launcher) recheckAndAggregate(appID string, rt *app.Runtime, col *logbus.Collector, checks map[string]*serviceHealthCheck, checkedAt time.Time, readiness *startupReadiness) {
	if cur := rt.GetStatus(); cur == app.StatusStopped || cur == app.StatusFailed || cur == app.StatusStopping {
		return
	}
	svcs, err := l.Store.ListServicesByRun(rt.RunID)
	if err != nil {
		return
	}
	if len(svcs) == 0 {
		l.waitForReadiness(rt, readiness, svcs, checkedAt, col)
		return
	}

	now := checkedAt.UTC().Format(time.RFC3339)
	healthy, unhealthy := 0, 0
	for _, svc := range svcs {
		check := checks[svc.ID]
		if check == nil {
			check = &serviceHealthCheck{}
			checks[svc.ID] = check
		}
		if checkedAt.Before(check.nextCheck) {
			switch svc.Health {
			case "healthy":
				healthy++
			case "unhealthy":
				unhealthy++
			}
			continue
		}
		prev := svc.Health
		probeStarted := time.Now()
		var hr *probe.HealthResult
		if readiness != nil && readiness.urls[svc.Port] != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			hr = probe.CheckURL(ctx, readiness.urls[svc.Port])
			cancel()
		} else {
			hr = l.probeService(svc.URL)
		}
		probeDuration := time.Since(probeStarted)
		// 停止期间返回的探测结果不能再改写服务健康状态。
		if cur := rt.GetStatus(); cur == app.StatusStopped || cur == app.StatusFailed || cur == app.StatusStopping {
			return
		}
		check.nextCheck = checkedAt.Add(probeDuration).Add(healthRetryInterval)
		ok := hr != nil && hr.Reachable
		if l.Diagnostics != nil && (probeDuration >= 500*time.Millisecond || !ok) {
			status := "reachable"
			severity := "info"
			if !ok {
				status = "unreachable"
				severity = "warn"
			}
			l.Diagnostics.Record(diagnostics.Event{
				AppID: appID, RunID: rt.RunID, Kind: "performance", Severity: severity, Source: "health",
				Operation: "health.probe", Status: status, DurationMS: probeDuration.Milliseconds(),
				Message: "服务健康检查完成", Context: map[string]any{"port": svc.Port, "role": svc.Role},
			})
		}
		if ok {
			check.failures = 0
			check.nextCheck = checkedAt.Add(probeDuration).Add(healthyCheckInterval)
			svc.Health = "healthy"
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
			if check.failures < healthFailureThreshold {
				check.failures++
			}
			if prev == "healthy" && check.failures < healthFailureThreshold {
				// 单次失败暂不翻转运行状态，但记录探测时间并快速复查。
				_ = l.Store.UpdateServiceHealth(svc.ID, prev, now)
				healthy++
				if col != nil {
					col.Warn(fmt.Sprintf("[健康] %s port=%d 首次失败，等待复查确认", svc.URL, svc.Port))
				}
			} else {
				svc.Health = "unhealthy"
				_ = l.Store.UpdateServiceHealth(svc.ID, "unhealthy", now)
				unhealthy++
				if col != nil && prev != "unhealthy" {
					col.Warn(fmt.Sprintf("[健康] %s port=%d %s → unhealthy（不可达）", svc.URL, svc.Port, prev))
				}
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
	if l.waitForReadiness(rt, readiness, svcs, checkedAt, col) {
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
