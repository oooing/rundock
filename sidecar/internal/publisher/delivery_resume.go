package publisher

import (
	"context"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

// The sealed path never re-enters target execution or candidate lookup.
func (s *Service) resumeDelivery(run *store.ReleaseRun, plan *executionPlan) {
	defer s.release(run.RepoRoot)
	ctx, done := s.runContext(run.ID)
	defer done()
	stage := "delivery_preflight"
	set := func(next string) {
		stage = next
		_ = s.store.UpdateReleaseRun(run.ID, "running", stage, run.CommitSHA, "", "", false)
	}
	fail := func(err error) { s.failDelivery(run, stage, err) }
	set(stage)
	if err := s.deliveryPreflight(ctx, run); err != nil {
		fail(err)
		return
	}
	set("tagging")
	for _, version := range releaseVersionsForRun(run, plan) {
		if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
			fail(err)
			return
		}
	}
	state, err := s.inspectRetryRemote(ctx, run, plan)
	if err != nil {
		fail(err)
		return
	}
	if !state.branchUploaded {
		set("pushing_branch")
		pushCtx, cancel := commandContext(ctx, releasePushTimeout)
		_, err = s.git(pushCtx, run.RepoRoot, "push", run.RemoteName, run.CommitSHA+":refs/heads/"+run.Branch)
		cancel()
		if err != nil {
			fail(&Error{Code: "push_branch_failed", Message: "代码同步尚未确认，产物已保存"})
			return
		}
	}
	for _, version := range releaseVersionsForRun(run, plan) {
		if state.uploadedTags[version.TagName] {
			continue
		}
		set("pushing_tag")
		pushCtx, cancel := commandContext(ctx, releasePushTimeout)
		_, err = s.git(pushCtx, run.RepoRoot, "push", run.RemoteName, "refs/tags/"+version.TagName)
		cancel()
		if err != nil {
			fail(&Error{Code: "push_tag_failed", Message: "版本 Tag 同步尚未确认，产物已保存"})
			return
		}
	}
	set("delivery_publish")
	if err = s.publishDeliveries(ctx, run); err != nil {
		fail(err)
		return
	}
	_ = s.store.UpdateReleaseRun(run.ID, "succeeded", "completed", run.CommitSHA, "", "", true)
}

// Recover only abandons rows whose repository OS lock can be acquired. Another
// sidecar sharing the data directory may still own a queued/running operation.
func (s *Service) RecoverReleases() error {
	ids, err := s.store.UnfinishedReleaseIDs()
	if err != nil {
		return err
	}
	localIDs, err := s.store.LocalBuildRecoveryIDs()
	if err != nil {
		return err
	}
	ids = append(ids, localIDs...)
	for _, id := range ids {
		run, err := s.store.GetReleaseRun(id)
		if err != nil {
			return err
		}
		if run == nil || !s.reserve(run.RepoRoot) {
			continue
		}
		if IsLocalBuild(run) {
			err = s.recoverLocalBuild(run)
		} else {
			// Reconcile only local output manifests. No candidate cleanup, Git,
			// command execution or remote publication is authorized by recovery.
			s.recoverFormalLocalArtifacts(run)
			err = s.store.UpdateReleaseRun(id, "failed", run.Stage, run.CommitSHA, "release_interrupted", "执行进程已结束；继续操作将先核对保存的产物和远端结果", true)
		}
		s.release(run.RepoRoot)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) runContext(runID string) (context.Context, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	if s.runCancels == nil {
		s.runCancels = map[string]context.CancelFunc{}
	}
	if s.cancelRequested[runID] {
		cancel()
	}
	s.runCancels[runID] = cancel
	return ctx, func() {
		cancel()
		s.mu.Lock()
		delete(s.runCancels, runID)
		delete(s.cancelRequested, runID)
		s.mu.Unlock()
	}
}

func (s *Service) Cancel(runID string) error {
	run, err := s.store.GetReleaseRun(runID)
	if err != nil {
		return err
	}
	if run == nil {
		return &Error{Code: "release_not_found", Message: "发布任务不存在"}
	}
	if run.Status != "running" && run.Status != "queued" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelRequested == nil {
		s.cancelRequested = map[string]bool{}
	}
	s.cancelRequested[runID] = true
	if cancel := s.runCancels[runID]; cancel != nil {
		cancel()
	}
	if IsLocalBuild(run) {
		s.log(runID, "event", "正在取消本地构建；已验证保存的产物会保留")
	} else {
		s.log(runID, "event", "正在取消；已保存产物和远端草稿将保留")
	}
	return nil
}

func deliveryStage(stage string) bool { return strings.HasPrefix(stage, "delivery_") }
