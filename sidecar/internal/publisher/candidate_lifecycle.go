package publisher

import (
	"context"
	"os"
	"time"

	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/store"
)

const candidateIdleLifetime = time.Hour

// Only the API-owned service starts maintenance, not short-lived test fixtures.
func (s *Service) StartScratchMaintenance() {
	s.mu.Lock()
	if s.scratchCancel != nil || s.restarting {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.scratchCancel, s.scratchDone = cancel, done
	s.mu.Unlock()
	go func() {
		defer close(done)
		s.sweepScratch(ctx)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepScratch(ctx)
			}
		}
	}()
}

func (s *Service) sweepScratch(ctx context.Context) {
	s.cleanIdleCandidates(time.Now())
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	n, err := recoverScratch(ctx, os.TempDir())
	if s.diagnostics != nil && (n > 0 || err != nil && ctx.Err() == nil) {
		event := diagnostics.Event{Kind: "maintenance", Severity: "info", Source: "release", Operation: "release.scratch_cleanup", Status: "succeeded", Message: "已回收不再使用的构建临时目录", Context: map[string]any{"directories": n}}
		if err != nil {
			event.Status, event.Severity, event.Message = "pending", "warning", "部分临时文件仍被占用，将自动重试清理"
		}
		s.diagnostics.Record(event)
	}
	if err := s.pruneSavedArtifacts(ctx, time.Now()); err != nil && ctx.Err() == nil && s.diagnostics != nil {
		s.diagnostics.Record(diagnostics.Event{Kind: "maintenance", Severity: "warning", Source: "release", Operation: "release.artifact_retention", Status: "pending", Message: "部分旧安装包暂未清理，发布记录仍保留，下次维护时重试"})
	}
}

// Running workers retain their leases and clean on exit; abandoned leases are
// recovered next startup. Never destroy another instance's active files.
func (s *Service) Close() {
	s.mu.Lock()
	s.restarting = true
	cancel, done := s.scratchCancel, s.scratchDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	s.cleanIdleCandidates(time.Now())
}

func (s *Service) cleanIdleCandidates(now time.Time) {
	s.mu.Lock()
	s.candidatesMu.Lock()
	var dropped []*releaseCandidate
	for id, cand := range s.candidates {
		if s.active[gitPathKey(canonicalRepositoryPath(cand.RepoRoot))] {
			continue
		}
		cand.mu.Lock()
		running, cancelled := cand.View.Status == CheckRunning, cand.View.Status == CheckCancelled
		cand.mu.Unlock()
		if !running && (s.restarting || cancelled || !cand.lastUsed.IsZero() && now.Sub(cand.lastUsed) >= candidateIdleLifetime) {
			delete(s.candidates, id)
			dropped = append(dropped, cand)
		}
	}
	s.candidatesMu.Unlock()
	s.mu.Unlock()
	for _, cand := range dropped {
		cand.cleanup()
	}
}

// Caller holds the repository reservation. Release the previous tree before
// copying its replacement, reducing peak space from two snapshots to one.
func (s *Service) discardSupersededCandidates(appID, repo string) {
	s.candidatesMu.Lock()
	var dropped []*releaseCandidate
	for id, cand := range s.candidates {
		if cand.AppID == appID && gitPathKey(cand.RepoRoot) == gitPathKey(repo) {
			delete(s.candidates, id)
			dropped = append(dropped, cand)
		}
	}
	s.candidatesMu.Unlock()
	for _, cand := range dropped {
		cand.cleanup()
	}
}

func (s *Service) finishReleaseCandidate(run *store.ReleaseRun) {
	plan, err := parseExecutionPlan(run.ExecutionPlan)
	if err != nil || plan.CandidateID == "" {
		return
	}
	final, err := s.store.GetReleaseRun(run.ID)
	if err != nil || final == nil || final.Status == "running" || final.Status == "queued" {
		return
	}
	// Legacy custom commands can need their workspace for direct retry. Keep
	// only that candidate until idle expiry; sealed GitHub delivery uses
	// the durable vault and never needs the source tree on retry.
	if final.Status == "failed" && !plan.hasDelivery() && len(plan.Targets) > 0 {
		s.log(run.ID, "event", "构建临时副本暂留用于重试，闲置 1 小时后自动清理；过期后需重新检查，已保存安装包不受影响。")
		return
	}
	s.candidatesMu.Lock()
	cand := s.candidates[plan.CandidateID]
	delete(s.candidates, plan.CandidateID)
	s.candidatesMu.Unlock()
	if cand == nil {
		return
	}
	if cand.scratch != nil {
		if err := cand.scratch.cleanup(); err != nil {
			s.log(run.ID, "event", "部分临时文件仍被占用，RunDock 将自动重试清理。")
			return
		}
	} else {
		cand.cleanup()
	}
	s.log(run.ID, "event", "已清理本次临时副本；发布记录和已保存安装包仍保留。")
}
