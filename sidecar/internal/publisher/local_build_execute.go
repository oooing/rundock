package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func (s *Service) executeLocalBuild(ctx context.Context, done func(), run *store.ReleaseRun, plan *localBuildPlan, configHash string) {
	defer s.release(run.RepoRoot)
	defer done()
	stage := "local_freezing"
	fail := func(err error) {
		code, message := "local_build_failed", "本地构建失败，请查看构建日志"
		status, severity := "failed", "error"
		if typed, ok := err.(*Error); ok {
			code, message = typed.Code, typed.Message
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			code, message = "local_build_cancelled", "本地构建已取消；已经验证并保存的产物仍可取回"
			status, severity = "cancelled", "info"
		}
		s.log(run.ID, severity, message)
		_ = s.store.UpdateReleaseRun(run.ID, status, stage, run.CommitSHA, code, message, true)
		if targets, err := s.store.ReleaseTargetRuns(run.ID); err == nil {
			for _, target := range targets {
				if target.Status != "succeeded" {
					_ = s.store.UpdateReleaseTargetRun(run.ID, target.TargetID, status, stage, code, message, false, true)
				}
			}
		}
		// Task logs and fault state stay in the instance database. Project-local
		// diagnostics would mutate the source directory of this read-only build.
	}
	setStage := func(value, message string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		stage = value
		if err := s.store.UpdateReleaseRun(run.ID, "running", stage, run.CommitSHA, "", "", false); err != nil {
			return err
		}
		s.log(run.ID, "event", message)
		return nil
	}
	if err := setStage("local_freezing", "复制当前源码到独立构建目录"); err != nil {
		fail(err)
		return
	}
	if _, err := os.Stat(filepath.Join(run.RepoRoot, ".git")); err == nil {
		gitCtx, cancel := commandContext(ctx, 10*time.Second)
		run.CommitSHA, _ = s.git(gitCtx, run.RepoRoot, "-c", "core.fsmonitor=false", "rev-parse", "--verify", "HEAD")
		cancel()
	}
	patterns := []string{}
	for _, target := range plan.Targets {
		for _, pattern := range targetArtifactPatterns(target) {
			patterns = append(patterns, expandTargetCommand(pattern, run, &plan.executionPlan, target))
		}
	}
	snapshotCtx, cancelSnapshot := commandContext(ctx, 5*time.Minute)
	snapshot, err := s.createLocalSnapshot(snapshotCtx, run.RepoRoot, patterns)
	cancelSnapshot()
	if err != nil {
		fail(err)
		return
	}
	defer snapshot.cleanup()
	if snapshot.Files[releaseconfig.ManifestPath] != configHash {
		fail(&Error{Code: "local_build_config_changed", Message: "复制期间构建配置发生变化，请刷新设置后重新构建"})
		return
	}
	for _, target := range plan.Targets {
		groups := make([]releaseconfig.VersionGroup, 0, len(plan.VersionGroups))
		for _, group := range plan.VersionGroups {
			groups = append(groups, releaseconfig.VersionGroup{ID: group.ID, Name: group.Name, CurrentVersion: group.CurrentVersion, VersionFiles: group.VersionFiles})
		}
		version, err := localTargetVersion(snapshot.Root, groups, target.VersionGroup)
		expected, _ := plan.releaseVersionForGroup(target.VersionGroup)
		if err != nil || expected.TargetVersion != version {
			fail(&Error{Code: "local_build_version_changed", Message: "源码中的当前版本发生变化或无法读取，请刷新后重新构建"})
			return
		}
	}
	plan.SourceSHA256, plan.SourceFiles, plan.SnapshotRoot = snapshot.SHA256, snapshot.Files, snapshot.Root
	raw, err := json.Marshal(plan)
	if err == nil {
		err = s.store.FreezeLocalBuildSnapshot(run.ID, raw, run.CommitSHA)
	}
	if err != nil {
		fail(err)
		return
	}
	run.ExecutionPlan = raw
	s.log(run.ID, "event", "源码快照已冻结；使用当前版本，不改变开发目录")
	for _, target := range plan.Targets {
		version, _ := plan.releaseVersionForGroup(target.VersionGroup)
		targetRun := *run
		targetRun.RepoRoot, targetRun.TargetVersion = snapshot.Root, version.TargetVersion
		working, err := secureProjectPath(snapshot.Root, target.WorkingDir, true)
		if err != nil {
			fail(&Error{Code: "local_build_working_dir_invalid", Message: target.Name + " 的工作目录未包含在源码快照中"})
			return
		}
		steps := []targetStep{}
		if target.Steps.Check != "" {
			steps = append(steps, targetStep{"check", target.Steps.Check})
		}
		if target.Selection.Build {
			steps = append(steps, targetStep{"build", target.Steps.Build})
		}
		if target.Selection.Package {
			steps = append(steps, targetStep{"package", target.Steps.Package})
		}
		for _, step := range steps {
			if err := setStage("local_"+step.name, target.Name+"："+stepLabel(step.name)); err != nil {
				fail(err)
				return
			}
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "running", step.name, "", "", true, false)
			command := expandTargetCommand(step.command, &targetRun, &plan.executionPlan, target)
			stepCtx, cancel := commandContext(ctx, stepTimeout(target, step.name))
			out, commandErr := runTargetCommand(stepCtx, s.targetRunner, working, command)
			cancel()
			if out != "" {
				s.log(run.ID, "stdout", target.Name+"\n"+truncateTargetOutput(out))
			}
			if commandErr != nil {
				if errors.Is(commandErr, context.DeadlineExceeded) {
					fail(&Error{Code: "local_build_timeout", Message: target.Name + " 的“" + stepLabel(step.name) + "”超时，请检查日志或调整项目步骤的超时设置"})
				} else {
					fail(&Error{Code: "local_build_step_failed", Message: target.Name + " 的“" + stepLabel(step.name) + "”失败，请查看构建日志"})
				}
				return
			}
			if err := validateLocalSnapshotSources(ctx, snapshot); err != nil {
				fail(err)
				return
			}
			if err := s.store.MarkReleaseTargetStepDone(run.ID, target.ID, step.name); err != nil {
				fail(err)
				return
			}
		}
		if err := setStage("local_verifying", target.Name+"：验证最终产物"); err != nil {
			fail(err)
			return
		}
		for _, verification := range target.Verification {
			s.log(run.ID, "event", target.Name+"：验证 "+verification.Name)
			seconds := verification.TimeoutSeconds
			if seconds == 0 {
				seconds = 600
			}
			verifyCtx, cancel := commandContext(ctx, time.Duration(seconds)*time.Second)
			out, verifyErr := runTargetCommand(verifyCtx, s.targetRunner, working, expandTargetCommand(verification.Command, &targetRun, &plan.executionPlan, target))
			cancel()
			if out != "" {
				s.log(run.ID, "stdout", target.Name+"\n"+truncateTargetOutput(out))
			}
			if verifyErr != nil {
				fail(&Error{Code: "artifact_verification_failed", Message: target.Name + " 的产物验证失败：" + verification.Name})
				return
			}
		}
		if err := validateLocalSnapshotSources(ctx, snapshot); err != nil {
			fail(err)
			return
		}
		if err := setStage("local_saving", target.Name+"：可靠保存已验证产物"); err != nil {
			fail(err)
			return
		}
		if err := s.sealLocalBuildArtifacts(ctx, &targetRun, target, working); err != nil {
			fail(err)
			return
		}
		_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "succeeded", "completed", "", "", true, true)
	}
	if err := ctx.Err(); err != nil {
		fail(err)
		return
	}
	if err := s.store.UpdateReleaseRun(run.ID, "succeeded", "completed", run.CommitSHA, "", "", true); err != nil {
		fail(err)
		return
	}
	s.log(run.ID, "event", "本地构建完成，产物已保存；未提交代码、未创建 Tag、未上传")
}

func validateLocalSnapshotSources(ctx context.Context, snapshot *localBuildSnapshot) error {
	for rel, expected := range snapshot.Files {
		path, err := secureProjectPath(snapshot.Root, rel, false)
		if err != nil {
			return &Error{Code: "local_build_source_changed", Message: "构建命令删除或替换了冻结源码，已停止保存产物"}
		}
		hash, err := localSourceHash(ctx, path)
		if err != nil || hash != expected {
			return &Error{Code: "local_build_source_changed", Message: "构建命令修改了冻结源码，已停止保存产物"}
		}
	}
	return nil
}

// Keep plan inspection separate from normal release execution and retry logic.
func IsLocalBuild(run *store.ReleaseRun) bool {
	var meta struct {
		Intent string `json:"intent"`
	}
	return run != nil && json.Unmarshal(run.ExecutionPlan, &meta) == nil && strings.TrimSpace(meta.Intent) == LocalBuildIntent
}
