package publisher

import (
	"context"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func undeliveredLocalTarget(target planTarget) bool {
	return strings.EqualFold(strings.TrimSpace(target.Runner.Type), releaseconfig.RunnerLocal) &&
		!target.Selection.Publish && !target.Selection.Deploy && (target.Selection.Build || target.Selection.Package) &&
		(len(target.Artifacts) > 0 || len(target.ArtifactRules) > 0)
}

func (plan *executionPlan) hasUndeliveredLocalArtifacts() bool {
	for _, target := range plan.Targets {
		if undeliveredLocalTarget(target) {
			return true
		}
	}
	return false
}

// Formal release semantics remain in execute. This consumer only persists the
// already-built local outputs that the user did not select for upload/deploy.
// Legacy targets without declared artifacts keep their existing behavior.
func (s *Service) sealUndeliveredLocalArtifacts(ctx context.Context, run *store.ReleaseRun, plan *executionPlan) error {
	root := run.RepoRoot
	var candidate *releaseCandidate
	if plan.CandidateID != "" {
		candidate = s.lookupCandidate(plan.CandidateID)
		if candidate == nil {
			return &Error{Code: "candidate_not_found", Message: "构建候选已失效，无法保存本地产物，请重新构建"}
		}
		root = candidate.Work
	}
	for _, target := range plan.Targets {
		if !undeliveredLocalTarget(target) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		fail := func(err error) error {
			code, message := "local_artifact_save_failed", "保存本地产物失败："+redact(err.Error())
			if typed, ok := err.(*Error); ok {
				code, message = typed.Code, typed.Message
			}
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", "local_saving", code, message, true, true)
			return &Error{Code: code, Message: message}
		}
		working, err := secureProjectPath(root, target.WorkingDir, true)
		if err != nil {
			return fail(err)
		}
		targetRun := *run
		targetRun.RepoRoot = root
		if version, ok := plan.releaseVersionForGroup(target.VersionGroup); ok {
			targetRun.TargetVersion, targetRun.TagName = version.TargetVersion, version.TagName
		}
		if candidate != nil {
			if err := validateCandidateBytes(candidate); err != nil {
				return fail(&Error{Code: "build_changed_tree", Message: "冻结的候选源码已变化，已停止保存本地产物"})
			}
		}
		_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "running", "local_saving", "", "", true, false)
		for _, verification := range target.Verification {
			var baseline worktreeSnapshot
			if candidate == nil {
				var err error
				baseline, err = s.worktreeSnapshot(ctx, run.RepoRoot)
				if err != nil {
					return fail(&Error{Code: "git_status_failed", Message: "无法确认产物验证前的仓库状态"})
				}
			}
			s.log(run.ID, "event", target.Name+"：验证 "+verification.Name)
			seconds := verification.TimeoutSeconds
			if seconds == 0 {
				seconds = 600
			}
			verifyCtx, cancel := commandContext(ctx, time.Duration(seconds)*time.Second)
			out, commandErr := runTargetCommand(verifyCtx, s.targetRunner, working, expandTargetCommand(verification.Command, &targetRun, plan, target))
			cancel()
			if strings.TrimSpace(out) != "" {
				s.log(run.ID, "stdout", target.Name+"\n"+truncateTargetOutput(redactSensitiveLog(out)))
			}
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if commandErr != nil {
				return fail(&Error{Code: "artifact_verification_failed", Message: target.Name + " 的产物验证失败：" + verification.Name})
			}
			if candidate != nil {
				if err := validateCandidateBytes(candidate); err != nil {
					return fail(&Error{Code: "build_changed_tree", Message: "产物验证修改了冻结的候选源码，已停止保存"})
				}
			} else {
				after, err := s.worktreeSnapshot(ctx, run.RepoRoot)
				if err != nil {
					return fail(&Error{Code: "git_status_failed", Message: "无法确认产物验证后的仓库状态"})
				}
				allowed := targetArtifactPatterns(target)
				for i := range allowed {
					allowed[i] = expandTargetCommand(allowed[i], &targetRun, plan, target)
				}
				if ok, changed := verifyBuildSideEffects(baseline, after, allowed); !ok {
					return fail(&Error{Code: "build_changed_tree", Message: "产物验证修改了未声明的仓库文件：" + strings.Join(changed, "、")})
				}
			}
			if run.CommitSHA != "" {
				if err := s.ensureFrozenCommit(ctx, run); err != nil {
					return fail(&Error{Code: "release_commit_changed", Message: "产物验证改变了 Git 提交，已停止发布"})
				}
			}
		}
		s.log(run.ID, "event", target.Name+"：保存本地产物（不上传、不部署）")
		if err := s.sealLocalBuildArtifacts(ctx, &targetRun, target, working); err != nil {
			return fail(err)
		}
		if err := s.store.UpdateReleaseTargetRun(run.ID, target.ID, "succeeded", "completed", "", "", true, true); err != nil {
			return fail(err)
		}
	}
	return nil
}
