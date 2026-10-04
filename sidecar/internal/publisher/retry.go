package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/store"
	"strings"
	"time"
)

func (s *Service) Retry(runID string, requests ...RetryRequest) (*store.ReleaseRun, error) {
	run, err := s.store.GetReleaseRun(runID)
	if err != nil || run == nil {
		return nil, &Error{Code: "release_not_found", Message: "发布记录不存在"}
	}
	if IsLocalBuild(run) {
		return nil, &Error{Code: "local_build_not_retryable", Message: "本地构建请开始新构建；已保存产物仍可取回"}
	}
	if run.Status != "failed" || run.CommitSHA == "" || !retryableStage(run.Stage) || run.ErrorCode == "build_changed_tree" {
		return nil, &Error{Code: "release_not_retryable", Message: "该失败阶段不能直接重试，请重新执行发布预检"}
	}
	confirmed := len(requests) > 0 && requests[0].ExternalActionsConfirmed
	plan, planErr := parseExecutionPlan(run.ExecutionPlan)
	if planErr != nil || (strings.HasPrefix(run.Stage, "target_") && len(plan.Targets) == 0) {
		return nil, &Error{Code: "execution_plan_invalid", Message: "冻结的发布执行计划无法用于重试"}
	}
	if s.sealedDeliveryReady(run, plan) {
		if !s.reserve(run.RepoRoot) {
			return nil, &Error{Code: "release_in_progress", Message: "该仓库已有任务执行中"}
		}
		if err = s.store.UpdateReleaseRun(run.ID, "queued", run.Stage, run.CommitSHA, "", "", false); err != nil {
			s.release(run.RepoRoot)
			return nil, err
		}
		run.Status = "queued"
		run.ErrorCode = ""
		run.ErrorMessage = ""
		run.FinishedAt = nil
		go s.resumeDelivery(run, plan)
		return run, nil
	}
	if plan.hasDelivery() {
		return nil, &Error{Code: "delivery_not_sealed", Message: "产物尚未完整封存，或封存清单已损坏；请重新预检并构建，不能直接继续发布"}
	}
	if run.CreateTag && (preTargetRetryStage(run.Stage) || run.Stage == "tagging" || retryNeedsGitUpload(run, plan)) {
		if err := validateFrozenTagPlan(plan); err != nil {
			return nil, err
		}
	}
	states, err := s.store.ReleaseTargetRuns(run.ID)
	if err != nil {
		return nil, err
	}
	if targets := retryCustomExternalTargets(run, plan, states); len(targets) > 0 && !confirmed {
		return nil, &Error{Code: "external_actions_confirmation_required", Message: "重试将重新执行这些操作，可能再次更新线上服务：" + strings.Join(targets, "、")}
	}
	commitCtx, cancelCommit := commandContext(context.Background(), 10*time.Second)
	commitErr := s.ensureFrozenCommit(commitCtx, run)
	cancelCommit()
	if commitErr != nil {
		return nil, commitErr
	}
	if !s.reserve(run.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库已有发布任务正在执行"}
	}
	_ = s.store.UpdateReleaseRun(run.ID, "queued", run.Stage, run.CommitSHA, "", "", false)
	run.Status = "queued"
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.FinishedAt = nil
	go s.retry(run)
	return run, nil
}

func (s *Service) retry(run *store.ReleaseRun) {
	defer s.release(run.RepoRoot)
	ctx, done := s.runContext(run.ID)
	defer done()
	retryStarted := time.Now()
	currentStage := ""
	stageStarted := time.Time{}
	originalStage := run.Stage
	plan, planErr := parseExecutionPlan(run.ExecutionPlan)
	finishStage := func(status, severity, code, msg string) {
		if currentStage == "" {
			return
		}
		kind := "performance"
		if status == "failed" {
			kind = "error"
		}
		s.recordRelease(run, diagnostics.Event{
			Kind: kind, Severity: severity, Source: "release", Operation: "release.retry_stage",
			Stage: currentStage, Status: status, DurationMS: time.Since(stageStarted).Milliseconds(),
			ErrorCode: code, Message: msg,
		})
		currentStage = ""
	}
	fail := func(stage, code, msg string) {
		if currentStage == "" {
			stageStarted = retryStarted
		}
		currentStage = stage
		finishStage("failed", "error", code, msg)
		s.log(run.ID, "error", msg)
		_ = s.store.UpdateReleaseRun(run.ID, "failed", stage, run.CommitSHA, code, msg, true)
	}
	set := func(stage string) {
		finishStage("succeeded", "info", "", "发布重试阶段完成")
		currentStage = stage
		stageStarted = time.Now()
		_ = s.store.UpdateReleaseRun(run.ID, "running", stage, run.CommitSHA, "", "", false)
		s.log(run.ID, "event", "重试："+stageText(stage))
		s.recordRelease(run, diagnostics.Event{
			Kind: "release", Severity: "info", Source: "release", Operation: "release.retry_stage",
			Stage: stage, Status: "started", Message: "重试：" + stageText(stage),
		})
	}
	if planErr != nil {
		fail("preparing", "execution_plan_invalid", "冻结的发布执行计划无法读取")
		return
	}
	if err := s.ensureFrozenCommit(ctx, run); err != nil {
		if commitErr, ok := err.(*Error); ok {
			fail(originalStage, commitErr.Code, commitErr.Message)
		} else {
			fail(originalStage, "release_commit_changed", err.Error())
		}
		return
	}
	preTargets := preTargetRetryStage(originalStage)
	if preTargets {
		set("building_targets")
		if err := s.executeTargetPhase(ctx, run, plan, false); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("building_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}
	if run.CreateTag && (preTargets || originalStage == "tagging") {
		set("tagging")
		for _, version := range releaseVersionsForRun(run, plan) {
			if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("tagging", tagErr.Code, tagErr.Message)
				} else {
					fail("tagging", "tag_failed", err.Error())
				}
				return
			}
		}
	}
	resumeGit := retryNeedsGitUpload(run, plan)
	if run.CreateTag && resumeGit {
		// Validate the frozen Tag before retrying any remote mutation. It is
		// validated again immediately before the eventual Tag push.
		for _, version := range releaseVersionsForRun(run, plan) {
			if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("pushing_tag", tagErr.Code, tagErr.Message)
				} else {
					fail("pushing_tag", "tag_failed", err.Error())
				}
				return
			}
		}
	}
	remoteState := &retryRemoteState{uploadedTags: map[string]bool{}}
	if resumeGit {
		set(originalStage)
		s.log(run.ID, "event", "正在自动核对上传结果，已上传的内容会跳过")
		var checkErr error
		remoteState, checkErr = s.inspectRetryRemote(ctx, run, plan)
		if checkErr != nil {
			if typed, ok := checkErr.(*Error); ok {
				fail(originalStage, typed.Code, typed.Message)
			} else {
				fail(originalStage, "remote_check_failed", checkErr.Error())
			}
			return
		}
		if err := s.ensureFrozenCommit(ctx, run); err != nil {
			fail(originalStage, "release_commit_changed", err.Error())
			return
		}
		if run.CreateTag {
			for _, version := range releaseVersionsForRun(run, plan) {
				if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
					fail(originalStage, "tag_collision", err.Error())
					return
				}
			}
		}
	}
	pushBranch := resumeGit
	if pushBranch {
		set("pushing_branch")
		if remoteState.branchUploaded {
			s.log(run.ID, "event", "远端分支已包含本次提交，跳过重复上传")
		} else {
			pushCtx, cancelPush := commandContext(ctx, releasePushTimeout)
			out, err := s.git(pushCtx, run.RepoRoot, "push", run.RemoteName, run.CommitSHA+":refs/heads/"+run.Branch)
			cancelPush()
			if err != nil {
				fail("pushing_branch", "push_branch_failed", uploadFailureMessage(out, err))
				return
			}
		}
	}
	pushTag := resumeGit && run.CreateTag
	if pushTag {
		set("pushing_tag")
		for _, version := range releaseVersionsForRun(run, plan) {
			if remoteState.uploadedTags[version.TagName] {
				s.log(run.ID, "event", "版本 "+version.TagName+" 已上传且内容一致，跳过重复上传")
				continue
			}
			if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("pushing_tag", tagErr.Code, tagErr.Message)
				} else {
					fail("pushing_tag", "tag_failed", err.Error())
				}
				return
			}
			pushTagCtx, cancelTag := commandContext(ctx, releasePushTimeout)
			out, err := s.git(pushTagCtx, run.RepoRoot, "push", run.RemoteName, "refs/tags/"+version.TagName)
			cancelTag()
			if err != nil {
				fail("pushing_tag", "push_tag_failed", uploadFailureMessage(out, err))
				return
			}
		}
	}
	if len(plan.Targets) > 0 && (preTargets || originalStage == "tagging" || pushBranch || pushTag || postTargetRetryStage(originalStage)) {
		set("publishing_targets")
		if err := s.executeTargetPhase(ctx, run, plan, true); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("publishing_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}
	if automationHandoffApplies(run, plan) {
		s.log(run.ID, "event", "已交给 GitHub "+automationAction(plan)+"："+strings.Join(releaseTagNames(releaseVersionsForRun(run, plan)), "、"))
	} else if run.CreateTag && run.PushRemote {
		s.log(run.ID, "event", "代码与 Tag 已推送："+strings.Join(releaseTagNames(releaseVersionsForRun(run, plan)), "、"))
	} else if !run.CreateTag && run.PushRemote {
		s.log(run.ID, "event", "代码提交与推送完成（未创建 Tag）")
	} else if run.CreateTag {
		s.log(run.ID, "event", "本地提交与 Tag 创建完成（未上传远程仓库）")
	} else {
		s.log(run.ID, "event", "本地代码提交完成（未上传远程仓库）")
	}
	_ = s.store.UpdateReleaseRun(run.ID, "succeeded", "completed", run.CommitSHA, "", "", true)
	finishStage("succeeded", "info", "", "发布重试阶段完成")
	s.recordRelease(run, diagnostics.Event{
		Kind: "release", Severity: "info", Source: "release", Operation: "release.retry",
		Stage: "completed", Status: "succeeded", DurationMS: time.Since(retryStarted).Milliseconds(),
		Message: "发布重试完成", Context: map[string]any{"commitSha": run.CommitSHA, "originalStage": originalStage},
	})
}

func retryableStage(stage string) bool {
	return deliveryStage(stage) || preTargetRetryStage(stage) || postTargetRetryStage(stage) || stage == "tagging" || stage == "pushing_branch" || stage == "pushing_tag"
}

func preTargetRetryStage(stage string) bool {
	return stage == "building_targets" || stage == "target_check" || stage == "target_build" || stage == "target_package"
}

func postTargetRetryStage(stage string) bool {
	return stage == "publishing_targets" || stage == "target_publish" || stage == "target_deploy"
}
