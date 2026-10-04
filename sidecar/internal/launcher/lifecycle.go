package launcher

import (
	"context"
	"fmt"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/proc"
	"time"
)

// watchExit 等进程退出，更新状态。
func (l *Launcher) watchExit(appID string, rt *app.Runtime, handle *proc.Handle, cancel context.CancelFunc, col *logbus.Collector) {
	exitCode, waitErr := handle.Wait()
	cancel()
	_ = handle.Close()

	// Stop owns the final transition: it must also verify observed children.
	if rt.GetStatus() == app.StatusStopping {
		if col != nil {
			col.Info(fmt.Sprintf("[退出] 根进程已退出 exitCode=%d，等待子进程清理确认", exitCode))
		}
		return
	}

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
	if l.Diagnostics != nil {
		severity := "info"
		errorCode := ""
		if next == app.StatusFailed {
			severity = "error"
			errorCode = "process_exit_nonzero"
		}
		context := map[string]any{"exitCode": exitCode, "previousStatus": cur}
		if waitErr != nil {
			context["waitError"] = waitErr.Error()
		}
		l.Diagnostics.Record(diagnostics.Event{
			AppID: appID, RunID: rt.RunID, Kind: "lifecycle", Severity: severity, Source: "process",
			Operation: "app.run", Status: next, DurationMS: time.Since(rt.StartedAt).Milliseconds(),
			ErrorCode: errorCode, Message: "项目进程已退出",
			Context: context,
		})
	}
	if col != nil && next == app.StatusStopped {
		col.Info(fmt.Sprintf("[状态] %s → stopped", cur))
	}
	l.Manager.Registry.Remove(appID)
}

// Stop 停止一个 app。分级：Ctrl-Break -> grace -> Terminate(taskkill /t /f) -> 端口确认。
func (l *Launcher) Stop(appID string) (err error) {
	started := time.Now()
	defer func() {
		if l.Diagnostics == nil {
			return
		}
		status, severity, errorCode, message := "succeeded", "info", "", "项目停止操作完成"
		if err != nil {
			status, severity, errorCode, message = "failed", "error", "stop_failed", err.Error()
		}
		l.Diagnostics.Record(diagnostics.Event{
			AppID: appID, Kind: "performance", Severity: severity, Source: "launcher",
			Operation: "app.stop", Status: status, DurationMS: time.Since(started).Milliseconds(),
			ErrorCode: errorCode, Message: message,
		})
	}()
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
	gracePeriod := durationFromSetting(l.Store, "grace_period_seconds", 8)
	col := rs.collector
	rt, _ := l.Manager.Registry.Get(appID)
	if rt != nil {
		if col != nil {
			col.Info(fmt.Sprintf("[停止] 开始停止 pid=%d grace=%ds", rs.rootPID, int(gracePeriod/time.Second)))
		}
		l.Manager.Transition(rt, app.StatusStopping, nil)
	}

	// Remember only this run's process tree. Never kill a port's unrelated owner.
	owned := collectProcessTree(rs.rootPID)
	l.mu.Lock()
	seen := map[int]bool{}
	for _, pid := range rs.stopPIDs {
		seen[pid] = true
	}
	for _, pid := range owned {
		if !seen[pid] {
			rs.stopPIDs = append(rs.stopPIDs, pid)
			seen[pid] = true
		}
	}
	tracked := append([]int(nil), rs.stopPIDs...)
	l.mu.Unlock()

	finished := func() bool {
		select {
		case <-rs.exitDone:
		default:
			return false
		}
		for _, pid := range tracked {
			if !isProcessGone(pid) {
				return false
			}
		}
		return true
	}
	wait := func(duration time.Duration) bool {
		deadline := time.Now().Add(duration)
		for {
			if finished() {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	finish := func() error {
		if col != nil {
			col.Info("[停止] 根进程及已跟踪子进程均已退出")
		}
		if rt != nil {
			l.finishStopped(appID, rt, 0)
		}
		return nil
	}
	if finished() {
		return finish()
	}
	if err := rs.handle.GracefulStop(); err != nil {
		if col != nil {
			col.Warn(fmt.Sprintf("[停止] 优雅停止信号发送失败: %v", err))
		}
	} else if col != nil {
		col.Debug("[停止] 已发送优雅停止信号（Ctrl+C）")
	}
	if wait(gracePeriod) {
		return finish()
	}

	if col != nil {
		col.Warn("[停止] grace 超时，强制终止进程树")
	}
	select {
	case <-rs.exitDone: // Root already reaped; do not taskkill a potentially reused PID.
	default:
		if err := rs.handle.Terminate(); err != nil && col != nil {
			col.Warn(fmt.Sprintf("[停止] 强制终止返回: %v", err))
		}
	}
	if wait(3 * time.Second) {
		return finish()
	}
	// Keep the run tracked and retryable. Never claim stopped while children live.
	if rt != nil {
		l.Manager.Transition(rt, app.StatusDegraded, nil)
	}
	return fmt.Errorf("停止未完成：仍有进程或资源未退出，请查看日志后重试")
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
	deadline := time.NewTimer(durationFromSetting(l.Store, "grace_period_seconds", 8) + 5*time.Second)
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
func (l *Launcher) Restart(ctx context.Context, appID string) (err error) {
	started := time.Now()
	defer func() {
		if l.Diagnostics == nil {
			return
		}
		status, severity, errorCode, message := "succeeded", "info", "", "项目重启操作完成"
		if err != nil {
			status, severity, errorCode, message = "failed", "error", "restart_failed", err.Error()
		}
		l.Diagnostics.Record(diagnostics.Event{
			AppID: appID, Kind: "performance", Severity: severity, Source: "launcher",
			Operation: "app.restart", Status: status, DurationMS: time.Since(started).Milliseconds(),
			ErrorCode: errorCode, Message: message,
		})
	}()
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
