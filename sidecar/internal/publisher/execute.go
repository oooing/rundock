package publisher

import (
	"fmt"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"strings"
	"time"
)

func (s *Service) execute(run *store.ReleaseRun, pf *Preflight, selected []string, message, checkCommand string) {
	defer s.release(run.RepoRoot)
	ctx, done := s.runContext(run.ID)
	defer done()
	releaseStarted := time.Now()
	currentStage := ""
	stageStarted := time.Time{}
	committed := false
	stagedByTool := false
	originals := map[string][]byte{}
	expectedVersionWrites := map[string][]byte{}
	plan, planErr := parseExecutionPlan(run.ExecutionPlan)
	configuredVersionFiles := []releaseconfig.VersionFile{}
	versionFiles := []string{}
	if run.CreateTag {
		if planErr == nil && plan.usesConfiguredVersionGroups() {
			configuredVersionFiles = plan.versionFiles()
			for _, file := range configuredVersionFiles {
				versionFiles = append(versionFiles, file.Path)
			}
		} else {
			versionFiles = pf.VersionFiles
		}
	}
	stagePaths := dedupe(append(append([]string{}, selected...), versionFiles...))
	finishStage := func(status, severity, code, msg string) {
		if currentStage == "" {
			return
		}
		kind := "performance"
		if status == "failed" {
			kind = "error"
		}
		s.recordRelease(run, diagnostics.Event{
			Kind: kind, Severity: severity, Source: "release", Operation: "release.stage",
			Stage: currentStage, Status: status, DurationMS: time.Since(stageStarted).Milliseconds(),
			ErrorCode: code, Message: msg,
		})
		currentStage = ""
	}
	fail := func(stage, code, msg string) {
		if currentStage == "" {
			stageStarted = releaseStarted
		}
		currentStage = stage
		finishStage("failed", "error", code, msg)
		if !committed {
			if stagedByTool && len(stagePaths) > 0 {
				s.unstageExactPaths(ctx, run.RepoRoot, stagePaths)
			}
			for _, path := range restoreFilesSafely(originals, expectedVersionWrites) {
				s.log(run.ID, "stderr", "版本文件在发布期间又被修改，已保留现场且未自动覆盖："+path)
			}
		}
		s.log(run.ID, "error", msg)
		_ = s.store.UpdateReleaseRun(run.ID, "failed", stage, run.CommitSHA, code, msg, true)
	}
	setStage := func(stage string) {
		finishStage("succeeded", "info", "", "发布阶段完成")
		currentStage = stage
		stageStarted = time.Now()
		_ = s.store.UpdateReleaseRun(run.ID, "running", stage, run.CommitSHA, "", "", false)
		s.log(run.ID, "event", stageText(stage))
		s.recordRelease(run, diagnostics.Event{
			Kind: "release", Severity: "info", Source: "release", Operation: "release.stage",
			Stage: stage, Status: "started", Message: stageText(stage),
		})
	}

	setStage("preparing")
	if planErr != nil {
		fail("preparing", "execution_plan_invalid", "冻结的发布执行计划无法读取")
		return
	}
	if plan.hasDelivery() {
		if err := s.preflightDeliveryBuild(ctx, run, plan); err != nil {
			s.failDelivery(run, "delivery_preflight", err)
			return
		}
	}
	if paths, diagnosticsErr := s.untrackedDiagnosticsPaths(ctx, run.RepoRoot, stagePaths); diagnosticsErr != nil {
		fail("preparing", "git_status_failed", "无法确认诊断目录中的提交文件是否已被 Git 跟踪")
		return
	} else if len(paths) > 0 {
		fail("preparing", "diagnostics_path_untracked", "诊断目录中的未跟踪文件不能加入发布提交："+strings.Join(paths, "、"))
		return
	}
	currentHead, headErr := s.git(ctx, run.RepoRoot, "rev-parse", "HEAD")
	currentStatus, statusErr := s.gitRaw(ctx, run.RepoRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if headErr != nil || statusErr != nil || statusFingerprint(run.RepoRoot, currentHead, currentStatus, parseChanges(currentStatus)) != pf.StatusFingerprint {
		fail("preparing", "status_changed", "仓库内容已变化，请重新检查后再发布")
		return
	}
	if plan.CandidateID != "" {
		if plan.SkipChecks {
			s.log(run.ID, "event", "本次已跳过发布前检查，使用冻结的文件范围提交")
		} else {
			s.log(run.ID, "event", "使用已验收候选树提交，不改写工作区未选中文件")
		}
	} else if run.CreateTag {
		setStage("versioning")
		var err error
		if plan.usesConfiguredVersionGroups() {
			for _, group := range plan.VersionGroups {
				version, ok := plan.releaseVersionForGroup(group.ID)
				if !ok {
					err = fmt.Errorf("版本组 %s 缺少本次版本", group.ID)
					break
				}
				groupOriginals, updateErr := updateConfiguredVersionFiles(run.RepoRoot, group.VersionFiles, version.TargetVersion)
				for path, content := range groupOriginals {
					originals[path] = content
				}
				for path, content := range expectedConfiguredVersionWrites(run.RepoRoot, group.VersionFiles, groupOriginals, version.TargetVersion) {
					expectedVersionWrites[path] = content
				}
				if updateErr != nil {
					err = updateErr
					break
				}
			}
		} else {
			originals, err = updateVersionFiles(run.RepoRoot, versionFiles, run.TargetVersion)
			expectedVersionWrites = expectedLegacyVersionWrites(run.RepoRoot, versionFiles, originals, run.TargetVersion)
		}
		if err != nil {
			fail("versioning", "version_update_failed", err.Error())
			return
		}
	} else {
		s.log(run.ID, "event", "本次未创建版本 Tag，跳过版本文件更新")
	}

	setStage("checking")
	if plan.CandidateID == "" {
		checkBaseline, baselineErr := s.worktreeSnapshot(ctx, run.RepoRoot)
		if baselineErr != nil {
			fail("checking", "git_status_failed", "无法记录发布前检查的仓库状态")
			return
		}
		if plan.BuildMode != "github" && strings.TrimSpace(checkCommand) != "" {
			checkCtx, cancel := commandContext(ctx, 10*time.Minute)
			out, checkErr := runCheckCommand(checkCtx, s.runner, run.RepoRoot, checkCommand)
			cancel()
			if strings.TrimSpace(out) != "" {
				s.log(run.ID, "stdout", redact(out))
			}
			if checkErr != nil {
				fail("checking", "checks_failed", "发布前检查命令失败")
				return
			}
			afterCheck, statusErr := s.worktreeSnapshot(ctx, run.RepoRoot)
			if statusErr != nil {
				fail("checking", "git_status_failed", "无法检查发布前命令执行后的仓库状态")
				return
			}
			if ok, changed := verifyBuildSideEffects(checkBaseline, afterCheck, nil); !ok {
				fail("checking", "check_changed_tree", "发布前检查命令修改了仓库内容："+strings.Join(changed, "、"))
				return
			}
		}
	}
	if len(plan.Targets) > 0 {
		if err := s.executeTargetChecks(ctx, run, plan); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("target_check", "target_execution_failed", err.Error())
			}
			return
		}
	}
	checkedHead, checkedHeadErr := s.git(ctx, run.RepoRoot, "rev-parse", "HEAD")
	if checkedHeadErr != nil || checkedHead != pf.HeadSHA {
		fail("checking", "release_commit_changed", "发布前检查命令改变了 Git 提交，已停止发布")
		return
	}

	setStage("committing")
	var sha string
	if plan.CandidateID != "" {
		if err := s.verifyAcceptedCandidate(ctx, run, plan, pf, selected); err != nil {
			if typed, ok := err.(*Error); ok {
				fail("committing", typed.Code, typed.Message)
			} else {
				fail("committing", "candidate_not_accepted", err.Error())
			}
			return
		}
		committedSHA, commitErr := s.commitAcceptedCandidate(ctx, run, plan, pf, message)
		if commitErr != nil {
			if committedSHA != "" {
				run.CommitSHA = committedSHA
				committed = true
			}
			if typed, ok := commitErr.(*Error); ok {
				fail("committing", typed.Code, typed.Message)
			} else {
				fail("committing", "commit_failed", commitErr.Error())
			}
			return
		}
		sha = committedSHA
	} else {
		if len(stagePaths) > 0 {
			args := append([]string{"add", "--"}, stagePaths...)
			stagedByTool = true
			if out, err := s.git(ctx, run.RepoRoot, args...); err != nil {
				fail("committing", "git_add_failed", redact(out))
				return
			}
		}
		_, diffErr := s.git(ctx, run.RepoRoot, "diff", "--cached", "--quiet")
		if diffErr != nil {
			if out, err := s.git(ctx, run.RepoRoot, "commit", "-m", message); err != nil {
				fail("committing", "commit_failed", redact(out))
				return
			}
		}
		var err error
		sha, err = s.git(ctx, run.RepoRoot, "rev-parse", "HEAD")
		if err != nil {
			fail("committing", "commit_failed", "无法读取发布提交")
			return
		}
	}
	run.CommitSHA = sha
	committed = true
	_ = s.store.UpdateReleaseRun(run.ID, "running", "committing", sha, "", "", false)

	if len(plan.Targets) > 0 {
		setStage("building_targets")
		if err := s.executeTargetPhase(ctx, run, plan, false); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("building_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}

	if plan.hasUndeliveredLocalArtifacts() {
		setStage("local_saving")
		if err := s.sealUndeliveredLocalArtifacts(ctx, run, plan); err != nil {
			if typed, ok := err.(*Error); ok {
				fail("local_saving", typed.Code, typed.Message)
			} else {
				fail("local_saving", "local_artifact_save_failed", redact(err.Error()))
			}
			return
		}
	}

	if plan.hasDelivery() {
		setStage("delivery_preparing")
		if err := s.sealDeliveries(ctx, run, plan); err != nil {
			s.failDelivery(run, "delivery_preparing", err)
			return
		}
		setStage("delivery_preflight")
		if err := s.deliveryPreflight(ctx, run); err != nil {
			s.failDelivery(run, "delivery_preflight", err)
			return
		}
	}
	if run.CreateTag {
		setStage("tagging")
		for _, version := range releaseVersionsForRun(run, plan) {
			if err := s.ensureFrozenTag(ctx, run, plan, version, false); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("tagging", tagErr.Code, tagErr.Message)
				} else {
					fail("tagging", "tag_failed", err.Error())
				}
				return
			}
		}
	}
	if run.PushRemote {
		setStage("pushing_branch")
		pushCtx, cancelPush := commandContext(ctx, releasePushTimeout)
		out, err := s.git(pushCtx, run.RepoRoot, "push", run.RemoteName, run.CommitSHA+":refs/heads/"+run.Branch)
		cancelPush()
		if err != nil {
			fail("pushing_branch", "push_branch_failed", uploadFailureMessage(out, err))
			return
		}
		if run.CreateTag {
			setStage("pushing_tag")
			for _, version := range releaseVersionsForRun(run, plan) {
				if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
					if tagErr, ok := err.(*tagOperationError); ok {
						fail("pushing_tag", tagErr.Code, tagErr.Message)
					} else {
						fail("pushing_tag", "tag_failed", err.Error())
					}
					return
				}
				pushTagCtx, cancelTag := commandContext(ctx, releasePushTimeout)
				out, err = s.git(pushTagCtx, run.RepoRoot, "push", run.RemoteName, "refs/tags/"+version.TagName)
				cancelTag()
				if err != nil {
					fail("pushing_tag", "push_tag_failed", uploadFailureMessage(out, err))
					return
				}
			}
		}
	} else {
		s.log(run.ID, "event", "已保存在本机，未上传远程仓库")
	}
	if len(plan.Targets) > 0 {
		setStage("publishing_targets")
		if err := s.executeTargetPhase(ctx, run, plan, true); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("publishing_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}
	if plan.hasDelivery() {
		setStage("delivery_publish")
		if err := s.publishDeliveries(ctx, run); err != nil {
			s.failDelivery(run, "delivery_publish", err)
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
	_ = s.store.UpdateReleaseRun(run.ID, "succeeded", "completed", sha, "", "", true)
	finishStage("succeeded", "info", "", "发布阶段完成")
	s.recordRelease(run, diagnostics.Event{
		Kind: "release", Severity: "info", Source: "release", Operation: "release.run",
		Stage: "completed", Status: "succeeded", DurationMS: time.Since(releaseStarted).Milliseconds(),
		Message: "发布流程完成", Context: map[string]any{
			"commitSha": sha, "tagName": run.TagName, "targetVersion": run.TargetVersion,
			"createTag": run.CreateTag, "pushRemote": run.PushRemote,
		},
	})
}
