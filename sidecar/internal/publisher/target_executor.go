package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func (s *Service) worktreeSnapshot(ctx context.Context, repo string) (worktreeSnapshot, error) {
	raw, err := s.gitRaw(ctx, repo, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	out := worktreeSnapshot{}
	for _, change := range parseChanges(raw) {
		path := filepath.ToSlash(change.Path)
		hash := "<missing>"
		if data, readErr := os.ReadFile(filepath.Join(repo, filepath.FromSlash(path))); readErr == nil {
			sum := sha256.Sum256(data)
			hash = hex.EncodeToString(sum[:])
		}
		out[path] = worktreeEntry{Status: change.Status, Tracked: change.Tracked, Hash: hash}
	}
	return out, nil
}

func verifyBuildSideEffects(before, after worktreeSnapshot, allowedPatterns []string) (bool, []string) {
	keys := map[string]bool{}
	for path := range before {
		keys[path] = true
	}
	for path := range after {
		keys[path] = true
	}
	unsafe := []string{}
	for path := range keys {
		oldEntry, oldOK := before[path]
		newEntry, newOK := after[path]
		if oldOK == newOK && oldEntry == newEntry {
			continue
		}
		tracked := (oldOK && oldEntry.Tracked) || (newOK && newEntry.Tracked)
		if !tracked && matchesAnyArtifact(path, allowedPatterns) {
			continue
		}
		unsafe = append(unsafe, path)
	}
	sort.Strings(unsafe)
	return len(unsafe) == 0, unsafe
}

func (s *Service) executeTargetChecks(ctx context.Context, run *store.ReleaseRun, plan *executionPlan) error {
	if plan.SkipChecks {
		return nil
	}
	if plan.CandidateID != "" {
		for _, target := range plan.Targets {
			if target.Steps.Check != "" {
				cand := s.lookupCandidate(plan.CandidateID)
				passed := false
				if cand != nil {
					cand.mu.Lock()
					for _, check := range cand.View.CheckResults {
						if check.ID == "target:"+target.ID && check.Status == CheckPassed {
							passed = true
						}
					}
					cand.mu.Unlock()
				}
				if !passed {
					return &targetExecutionError{Stage: "target_check", Code: "checks_unverified", Message: target.Name + " 的目标检查尚未在候选中通过"}
				}
				if err := s.store.MarkReleaseTargetStepDone(run.ID, target.ID, "check"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return s.executeTargetPhaseInternal(ctx, run, plan, false, true)
}

func (s *Service) executeTargetPhase(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, postPush bool) error {
	return s.executeTargetPhaseInternal(ctx, run, plan, postPush, false)
}

func (s *Service) executeTargetPhaseInternal(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, postPush, checksOnly bool) error {
	if len(plan.Targets) == 0 {
		return nil
	}
	if postPush && s.sealedDeliveryReady(run, plan) {
		return nil
	}
	executionRoot := run.RepoRoot
	var candidate *releaseCandidate
	// Cloud handoff consumes only the frozen Git commit and Tag. Its retry must
	// survive candidate eviction or a sidecar restart.
	if plan.CandidateID != "" && !(postPush && plan.cloudOnly()) {
		candidate = s.lookupCandidate(plan.CandidateID)
		if candidate == nil {
			return &targetExecutionError{Stage: "target_build", Code: "candidate_not_found", Message: "隔离候选已失效，请重新检查后开始新发布"}
		}
		if err := validateCandidateBytes(candidate); err != nil {
			return &targetExecutionError{Stage: "target_build", Code: "candidate_stale", Message: err.Error()}
		}
		executionRoot = candidate.Work
	}
	artifactRun := *run
	artifactRun.RepoRoot = executionRoot
	headStage := "target_build"
	if checksOnly {
		headStage = "target_check"
	} else if postPush {
		headStage = "target_publish"
	}
	if run.CommitSHA != "" {
		if err := s.ensureFrozenCommit(ctx, run); err != nil {
			return &targetExecutionError{Stage: headStage, Code: "release_commit_changed", Message: err.Error()}
		}
	}
	runs, err := s.store.ReleaseTargetRuns(run.ID)
	if err != nil {
		return err
	}
	state := map[string]*store.ReleaseTargetRun{}
	for _, targetRun := range runs {
		state[targetRun.TargetID] = targetRun
	}
	baseline, err := s.worktreeSnapshot(ctx, run.RepoRoot)
	if err != nil {
		return &targetExecutionError{Stage: "target_check", Code: "git_status_failed", Message: "无法检查构建副作用"}
	}
	for _, target := range plan.Targets {
		targetRun := state[target.ID]
		if version, ok := plan.releaseVersionForGroup(target.VersionGroup); ok {
			artifactRun.TargetVersion, artifactRun.TagName = version.TargetVersion, version.TagName
		}
		if postPush && target.Delivery != nil && target.Selection.Publish {
			continue
		}
		if targetRun == nil {
			return &targetExecutionError{Stage: "target_check", Code: "target_state_missing", Message: "发布目标状态不存在：" + target.ID, TargetID: target.ID}
		}
		if strings.EqualFold(strings.TrimSpace(target.Runner.Type), releaseconfig.RunnerGitPush) {
			if checksOnly {
				continue
			}
			if !postPush {
				_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "waiting", "waiting_publish", "", "", true, false)
				continue
			}
			if strings.HasPrefix(target.Steps.Publish, "workflow-dispatch:") {
				if err := s.dispatchCloudTarget(ctx, run, plan, target, targetRun); err != nil {
					code := "cloud_dispatch_unconfirmed"
					if dispatchErr, ok := err.(*Error); ok {
						code = dispatchErr.Code
					}
					return &targetExecutionError{Stage: "target_publish", Code: code, Message: err.Error(), TargetID: target.ID}
				}
			}
			_ = s.store.UpdateReleaseRun(run.ID, "running", "target_publish", run.CommitSHA, "", "", false)
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "running", "publish", "", "", true, false)
			s.log(run.ID, "event", target.Name+"：代码已推送；云端执行结果尚未确认")
			if err := s.store.MarkReleaseTargetStepDone(run.ID, target.ID, "publish"); err != nil {
				return err
			}
			targetRun.PublishDone = true
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "handed_off", "cloud_pending", "", "", true, true)
			continue
		}
		workingDir, err := secureProjectPath(executionRoot, target.WorkingDir, true)
		if err != nil {
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", "checking", "target_working_dir_invalid", err.Error(), true, true)
			return &targetExecutionError{Stage: "target_check", Code: "target_working_dir_invalid", Message: err.Error(), TargetID: target.ID}
		}
		steps := targetStepsForPhase(target, targetRun, postPush, checksOnly)
		for _, step := range steps {
			stage := "target_" + step.name
			if postPush && (step.name == "publish" || step.name == "deploy") {
				if err := s.verifyFrozenTargetArtifacts(&artifactRun, target); err != nil {
					message := target.Name + " 的构建产物已变化：" + err.Error()
					_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", step.name, "artifact_changed", message, true, true)
					return &targetExecutionError{Stage: stage, Code: "artifact_changed", Message: message, TargetID: target.ID}
				}
			}
			_ = s.store.UpdateReleaseRun(run.ID, "running", stage, run.CommitSHA, "", "", false)
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "running", step.name, "", "", true, false)
			s.log(run.ID, "event", target.Name+"："+stageText(stage))
			command := expandTargetCommand(step.command, run, plan, target)
			stepStarted := time.Now()
			stepCtx, cancel := commandContext(ctx, stepTimeout(target, step.name))
			out, commandErr := runTargetCommand(stepCtx, s.targetRunner, workingDir, command)
			cancel()
			if candidate != nil {
				if err := validateCandidateBytes(candidate); err != nil {
					return &targetExecutionError{Stage: stage, Code: "build_changed_tree", Message: "构建命令修改了已验收的候选源码，已停止后续上传", TargetID: target.ID}
				}
			}
			stepStatus, stepSeverity, stepCode, stepMessage, stepKind := "succeeded", "info", "", target.Name+" 的命令执行完成", "performance"
			if commandErr != nil {
				stepStatus, stepSeverity, stepCode, stepMessage, stepKind = "failed", "error", "target_step_failed", target.Name+" 的命令执行失败", "error"
			}
			s.recordRelease(run, diagnostics.Event{
				Kind: stepKind, Severity: stepSeverity, Source: "release", Operation: "release.target_step",
				Stage: stage, Status: stepStatus, DurationMS: time.Since(stepStarted).Milliseconds(),
				ErrorCode: stepCode, Message: stepMessage,
				Context: map[string]any{"targetId": target.ID, "targetName": target.Name, "step": step.name},
			})
			if strings.TrimSpace(out) != "" {
				s.log(run.ID, "stdout", target.Name+"\n"+truncateTargetOutput(out))
			}
			if run.CommitSHA != "" {
				if err := s.ensureFrozenCommit(ctx, run); err != nil {
					message := target.Name + " 的命令改变了 Git 提交，已停止发布"
					_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", step.name, "release_commit_changed", message, true, true)
					return &targetExecutionError{Stage: stage, Code: "release_commit_changed", Message: message, TargetID: target.ID}
				}
			}
			after, statusErr := s.worktreeSnapshot(ctx, run.RepoRoot)
			if statusErr != nil {
				commandErr = errors.New("无法检查命令执行后的仓库状态")
			}
			allowed := targetArtifactPatterns(target)
			for i := range allowed {
				allowed[i] = expandTargetCommand(allowed[i], run, plan, target)
			}
			if ok, changed := verifyBuildSideEffects(baseline, after, allowed); !ok {
				message := target.Name + " 的命令修改了未声明的仓库文件：" + strings.Join(changed, "、")
				_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", step.name, "build_changed_tree", message, true, true)
				return &targetExecutionError{Stage: stage, Code: "build_changed_tree", Message: message, TargetID: target.ID}
			}
			baseline = after
			if commandErr != nil {
				message := target.Name + " 的“" + stepLabel(step.name) + "”命令执行失败"
				_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", step.name, "target_step_failed", message, true, true)
				return &targetExecutionError{Stage: stage, Code: "target_step_failed", Message: message, TargetID: target.ID}
			}
			if err := s.store.MarkReleaseTargetStepDone(run.ID, target.ID, step.name); err != nil {
				return err
			}
			markTargetStepDone(targetRun, step.name)
		}
		if checksOnly {
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "waiting", "checked", "", "", true, false)
			continue
		}
		if !postPush {
			if err := s.captureTargetArtifacts(&artifactRun, target, workingDir); err != nil {
				_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", "artifacts", "artifact_scan_failed", err.Error(), true, true)
				artifactStage := "target_build"
				if target.Selection.Package {
					artifactStage = "target_package"
				}
				return &targetExecutionError{Stage: artifactStage, Code: "artifact_scan_failed", Message: err.Error(), TargetID: target.ID}
			}
			if target.Selection.Publish || target.Selection.Deploy {
				_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "waiting", "waiting_publish", "", "", true, false)
			} else {
				_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "succeeded", "completed", "", "", true, true)
			}
		} else {
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "succeeded", "completed", "", "", true, true)
		}
	}
	return nil
}

type targetStep struct {
	name    string
	command string
}

func targetStepsForPhase(target planTarget, state *store.ReleaseTargetRun, postPush, checksOnly bool) []targetStep {
	steps := []targetStep{}
	if checksOnly {
		if target.Steps.Check != "" && !state.CheckDone {
			steps = append(steps, targetStep{"check", target.Steps.Check})
		}
		return steps
	}
	if !postPush {
		if target.Steps.Check != "" && !state.CheckDone {
			steps = append(steps, targetStep{"check", target.Steps.Check})
		}
		if target.Selection.Build && !state.BuildDone {
			steps = append(steps, targetStep{"build", target.Steps.Build})
		}
		if target.Selection.Package && !state.PackageDone {
			steps = append(steps, targetStep{"package", target.Steps.Package})
		}
	} else {
		if target.Selection.Publish && !state.PublishDone {
			steps = append(steps, targetStep{"publish", target.Steps.Publish})
		}
		if target.Selection.Deploy && !state.DeployDone {
			steps = append(steps, targetStep{"deploy", target.Steps.Deploy})
		}
	}
	return steps
}

func markTargetStepDone(state *store.ReleaseTargetRun, step string) {
	switch step {
	case "check":
		state.CheckDone = true
	case "build":
		state.BuildDone = true
	case "package":
		state.PackageDone = true
	case "publish":
		state.PublishDone = true
	case "deploy":
		state.DeployDone = true
	}
}

func expandTargetCommand(command string, run *store.ReleaseRun, plan *executionPlan, target planTarget) string {
	version, tag := run.TargetVersion, run.TagName
	if selected, ok := plan.releaseVersionForGroup(target.VersionGroup); ok {
		version, tag = selected.TargetVersion, selected.TagName
	}
	return strings.NewReplacer(
		"${VERSION}", version,
		"${TAG}", tag,
		"${COMMIT_SHA}", run.CommitSHA,
		"${TARGET_ID}", target.ID,
	).Replace(command)
}

func targetArtifactPatterns(target planTarget) []string {
	out := make([]string, 0, len(target.Artifacts))
	patterns := append([]string{}, target.Artifacts...)
	for _, rule := range target.ArtifactRules {
		patterns = append(patterns, rule.Pattern)
	}
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(filepath.Clean(filepath.FromSlash(pattern)))
		if target.WorkingDir != "" && target.WorkingDir != "." {
			pattern = strings.TrimSuffix(filepath.ToSlash(target.WorkingDir), "/") + "/" + pattern
		}
		out = append(out, pattern)
	}
	return out
}

func matchesAnyArtifact(path string, patterns []string) bool {
	path = filepath.ToSlash(path)
	for _, pattern := range patterns {
		if globPattern(pattern).MatchString(path) {
			return true
		}
	}
	return false
}

func globPattern(pattern string) *regexp.Regexp {
	pattern = filepath.ToSlash(pattern)
	var out strings.Builder
	out.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				out.WriteString(".*")
				i++
			} else {
				out.WriteString("[^/]*")
			}
		case '?':
			out.WriteString("[^/]")
		default:
			out.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	out.WriteByte('$')
	return regexp.MustCompile(out.String())
}
