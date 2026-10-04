package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

// Called only while RecoverReleases owns the repository's process/OS lock.
// Recovery repairs metadata, never executes commands or upgrades run success.
func (s *Service) recoverLocalBuild(run *store.ReleaseRun) error {
	plan, planErr := parseLocalBuildPlan(run)
	if planErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := s.reconcileLocalBuildArtifacts(ctx, run, &plan.executionPlan); err != nil {
			s.log(run.ID, "error", "中断产物未恢复："+redact(err.Error()))
		}
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		if err := cleanupRecordedLocalSnapshot(ctx, plan); err != nil {
			s.log(run.ID, "error", "临时源码未清理，已保留目录："+redact(err.Error()))
		}
		cancel()
	} else {
		s.log(run.ID, "error", "本地构建计划无效，未读取或清理磁盘目录")
	}
	if run.Status != "queued" && run.Status != "running" {
		return nil // A cancelled/failed operation keeps its original result.
	}
	if err := s.store.UpdateReleaseRun(run.ID, "failed", "local_interrupted", run.CommitSHA, "local_build_interrupted", "本地构建被中断；已保存产物仍可取回，需要继续构建时请重新开始", true); err != nil {
		return err
	}
	targets, err := s.store.ReleaseTargetRuns(run.ID)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if target.Status != "succeeded" {
			if err := s.store.UpdateReleaseTargetRun(run.ID, target.TargetID, "failed", "interrupted", "local_build_interrupted", "构建进程已结束，请重新开始", false, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// Formal tasks can also have completed local, undelivered outputs. Recover
// only saved bytes for unfinished tasks; their candidate, Git and remote state
// remain the responsibility of the existing explicit retry path.
func (s *Service) recoverFormalLocalArtifacts(run *store.ReleaseRun) {
	plan, err := parseExecutionPlan(run.ExecutionPlan)
	if err != nil {
		s.log(run.ID, "error", "发布计划无效，未核验中断的本地产物")
		return
	}
	if !plan.hasUndeliveredLocalArtifacts() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.reconcileLocalBuildArtifacts(ctx, run, plan); err != nil {
		s.log(run.ID, "error", "中断的本地产物未恢复："+redact(err.Error()))
	}
}

func (s *Service) reconcileLocalBuildArtifacts(ctx context.Context, run *store.ReleaseRun, plan *executionPlan) error {
	if _, _, err := s.localArtifactRun(run.ID); err != nil {
		return err
	}
	rows, err := s.store.LocalBuildArtifactManifests(run.ID)
	if err != nil {
		return err
	}
	registered := map[string]bool{}
	for _, row := range rows {
		registered[row.TargetID] = true
	}
	artifacts, err := s.store.ReleaseArtifacts(run.ID)
	if err != nil {
		return err
	}
	var problems []error
	for _, target := range plan.Targets {
		if registered[target.ID] || !undeliveredLocalTarget(target) {
			continue
		}
		dir, err := s.localArtifactTargetDirectory(run.ID, target.ID)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			continue // A directory alone never proves a complete, verified output.
		}
		if err := s.reconcileLocalArtifactTarget(ctx, run, plan, target, artifacts, dir); err != nil {
			problems = append(problems, fmt.Errorf("%s：%w", target.Name, err))
		}
	}
	return errors.Join(problems...)
}

func (s *Service) reconcileLocalArtifactTarget(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, target planTarget, artifacts []*store.ReleaseArtifact, dir string) error {
	if !localArtifactTargetBelongs(plan, target.ID) || !undeliveredLocalTarget(target) {
		return errors.New("产物目标不属于本次未上传的本地构建")
	}
	if _, err := checkedLocalPath(s.store.ReleaseDataDir(), filepath.Join("local-builds", run.ID, target.ID), true); err != nil {
		return err
	}
	lease, err := lockLocalArtifactVault(s.store.ReleaseDataDir(), run.ID)
	if err != nil {
		return err
	}
	defer lease.release()
	if err := lease.holdTree(ctx, dir); err != nil {
		return err
	}
	manifest, err := expectedRecoveredManifest(run, plan, target, artifacts)
	if err != nil {
		return err
	}
	expected, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	// The disk document supplies no authority: all fields and final file names
	// are reconstructed from the frozen target and captured DB metadata. Hold
	// those exact verified bytes and directories through the visibility commit,
	// just as normal saving does; recovery cannot reopen a check-to-commit race.
	if err := lease.verifyAndHoldManifest(dir, expected); err != nil {
		return fmt.Errorf("磁盘清单与数据库已捕获产物不一致或不可读取，未登记：%w", err)
	}
	if err := lease.verifyAndHoldFiles(ctx, dir, manifest); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.store.SealLocalBuildArtifacts(run.ID, target.ID, expected); err != nil {
		return err
	}
	s.log(run.ID, "event", target.Name+"：已核验并恢复保存的本地产物；任务仍为中断，未继续执行")
	return nil
}

func expectedRecoveredManifest(run *store.ReleaseRun, plan *executionPlan, target planTarget, artifacts []*store.ReleaseArtifact) (localArtifactManifest, error) {
	manifest := localArtifactManifest{SchemaVersion: 1, RunID: run.ID, AppID: run.AppID, TargetID: target.ID, Files: []localArtifactFile{}}
	selected := map[string]*store.ReleaseArtifact{}
	version, _ := plan.releaseVersionForGroup(target.VersionGroup)
	rules := target.ArtifactRules
	for _, artifact := range artifacts {
		if artifact.ReleaseRunID != run.ID || artifact.TargetID != target.ID {
			continue
		}
		if !validLocalArtifactName(artifact.Path) {
			return manifest, errors.New("数据库捕获元数据无效")
		}
		name, err := filepath.Rel(filepath.Clean(target.WorkingDir), filepath.FromSlash(artifact.Path))
		if err != nil || !validLocalArtifactName(filepath.ToSlash(name)) {
			return manifest, errors.New("数据库捕获路径不属于目标目录")
		}
		if len(rules) == 0 {
			selected[artifact.Path] = artifact
		}
	}
	for _, rule := range rules {
		pattern := strings.NewReplacer("${VERSION}", version.TargetVersion, "${TAG}", version.TagName).Replace(rule.Pattern)
		count := 0
		for _, artifact := range artifacts {
			if artifact.ReleaseRunID != run.ID || artifact.TargetID != target.ID {
				continue
			}
			name, _ := filepath.Rel(filepath.Clean(target.WorkingDir), filepath.FromSlash(artifact.Path))
			if globPattern(pattern).MatchString(filepath.ToSlash(name)) {
				selected[artifact.Path] = artifact
				count++
			}
		}
		if count < rule.Min || count > rule.Max {
			return manifest, errors.New("数据库捕获的最终产物数量不满足已冻结规则")
		}
	}
	paths := make([]string, 0, len(selected))
	for path := range selected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) == 0 || len(paths) > 2000 {
		return manifest, errors.New("缺少已捕获的最终产物")
	}
	names := map[string]bool{}
	for _, path := range paths {
		artifact := selected[path]
		if artifact.SizeBytes <= 0 || !validLocalDigest(artifact.SHA256) {
			return manifest, errors.New("数据库捕获的最终产物元数据无效")
		}
		name, _ := filepath.Rel(filepath.Clean(target.WorkingDir), filepath.FromSlash(path))
		name = filepath.ToSlash(name)
		if names[strings.ToLower(name)] {
			return manifest, errors.New("数据库捕获产物名称冲突")
		}
		names[strings.ToLower(name)] = true
		manifest.Files = append(manifest.Files, localArtifactFile{ID: localArtifactID(run.ID, target.ID, name), Name: name, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256})
	}
	return manifest, nil
}
