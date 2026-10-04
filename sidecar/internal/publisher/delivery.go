package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func (p *executionPlan) hasDelivery() bool {
	for _, t := range p.Targets {
		if t.Delivery != nil && t.Selection.Publish {
			return true
		}
	}
	return false
}

// SetDeliveryClient is an in-process composition boundary. It is not exposed by
// HTTP or project config; acceptance uses it to simulate network failures.
func (s *Service) SetDeliveryClient(client delivery.GitHub) { s.delivery.Client = client }

func validateDeliveryPlan(plan *executionPlan, createTag, push bool) error {
	if !plan.hasDelivery() {
		return nil
	}
	if !createTag || !push {
		return &Error{Code: "delivery_tag_required", Message: "GitHub 交付需要创建版本 Tag 并同步代码"}
	}
	groups := map[string]string{}
	for _, t := range plan.Targets {
		if t.Delivery == nil || !t.Selection.Publish {
			continue
		}
		if t.Selection.Deploy {
			return &Error{Code: "delivery_deploy_unsupported", Message: "安装包交付与服务器部署分开执行"}
		}
		if !strings.EqualFold(githubRepository(plan.RemoteURL), t.Delivery.Repository) {
			return &Error{Code: "delivery_repository_mismatch", Message: "发布仓库必须与当前 Git 远端一致"}
		}
		raw, _ := json.Marshal(t.Delivery)
		if previous, ok := groups[t.VersionGroup]; ok && previous != string(raw) {
			return &Error{Code: "delivery_group_conflict", Message: "同一版本组必须使用相同的交付设置"}
		}
		groups[t.VersionGroup] = string(raw)
	}
	for _, t := range plan.Targets {
		if strings.EqualFold(t.Runner.Type, "git-push") || t.Selection.Deploy || (t.Selection.Publish && t.Delivery == nil) {
			return &Error{Code: "delivery_mixed_execution", Message: "本次交付包含其他远程执行方式，请按构建方式分别发起任务"}
		}
		if _, ok := groups[t.VersionGroup]; ok && (t.Delivery == nil || !t.Selection.Publish) {
			return &Error{Code: "delivery_group_conflict", Message: "同一个 Tag 的全部所选目标必须一起交付，首版不支持混合本地和云端产物"}
		}
	}
	return nil
}

func (s *Service) sealDeliveries(ctx context.Context, run *store.ReleaseRun, plan *executionPlan) error {
	root := run.RepoRoot
	if plan.CandidateID != "" {
		cand := s.lookupCandidate(plan.CandidateID)
		if cand == nil {
			return &Error{Code: "candidate_not_found", Message: "构建候选已失效，尚未封存的产物需要重新构建"}
		}
		root = cand.Work
	}
	workflows, err := delivery.Workflows(root)
	if err != nil {
		return err
	}
	groups := map[string]delivery.Batch{}
	sources := map[string][]delivery.Source{}
	for _, target := range plan.Targets {
		if target.Delivery == nil || !target.Selection.Publish {
			continue
		}
		version, ok := plan.releaseVersionForGroup(target.VersionGroup)
		if !ok {
			return fmt.Errorf("delivery version missing")
		}
		working, err := secureProjectPath(root, target.WorkingDir, true)
		if err != nil {
			return err
		}
		for _, check := range target.Verification {
			s.log(run.ID, "event", target.Name+"：验证 "+check.Name)
			seconds := check.TimeoutSeconds
			if seconds == 0 {
				seconds = 600
			}
			stepCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
			out, commandErr := runBuildCommand(stepCtx, working, expandTargetCommand(check.Command, run, plan, target))
			cancel()
			if out != "" {
				s.log(run.ID, "stdout", redactSensitiveLog(out))
			}
			if commandErr != nil {
				return &Error{Code: "artifact_verification_failed", Message: target.Name + " 的产物验证失败：" + check.Name}
			}
		}
		artifactRun := *run
		if plan.CandidateID != "" {
			candidate := s.lookupCandidate(plan.CandidateID)
			if candidate == nil {
				return &Error{Code: "candidate_not_found", Message: "验证期间候选已失效，请重新构建"}
			}
			if err := validateCandidateBytes(candidate); err != nil {
				return &Error{Code: "build_changed_tree", Message: "产物验证命令修改了冻结源码，已停止发布"}
			}
		}
		artifactRun.RepoRoot = root
		if err = s.verifyFrozenTargetArtifacts(&artifactRun, target); err != nil {
			return &Error{Code: "artifact_changed", Message: err.Error()}
		}
		artifacts, err := s.store.ReleaseArtifacts(run.ID)
		if err != nil {
			return err
		}
		matches := map[string]delivery.Source{}
		for _, rule := range target.ArtifactRules {
			pattern := expandTargetCommand(rule.Pattern, run, plan, target)
			count := 0
			for _, a := range artifacts {
				if a.TargetID != target.ID {
					continue
				}
				path, err := secureProjectPath(root, a.Path, false)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(working, path)
				if err != nil {
					return err
				}
				if globPattern(pattern).MatchString(filepath.ToSlash(rel)) {
					count++
					matches[path] = delivery.Source{TargetID: target.ID, Path: path, SHA256: a.SHA256}
				}
			}
			if count < rule.Min || count > rule.Max {
				return &Error{Code: "required_artifact_missing", Message: fmt.Sprintf("%s 必需产物 %s 需要 %d–%d 个，实际 %d 个", target.Name, pattern, rule.Min, rule.Max, count)}
			}
		}
		d := target.Delivery
		batch := delivery.Batch{RunID: run.ID, AppID: run.AppID, GroupID: target.VersionGroup, Repository: d.Repository, Account: d.Account, Commit: run.CommitSHA, Tag: version.TagName, Version: version.TargetVersion, Notes: plan.ReleaseNotes, Prerelease: d.Prerelease, MakeLatest: d.MakeLatest, Workflows: workflows}
		if d.Sync != nil {
			batch.SyncURL = d.Sync.URL
			batch.SyncPointer = d.Sync.JSONPointer
		}
		if d.Deployment != nil {
			batch.DeploymentStrategy = d.Deployment.Strategy
			batch.DeploymentWorkflow = d.Deployment.Workflow
			if batch.DeploymentWorkflow != "" && workflows[batch.DeploymentWorkflow] == "" {
				return &Error{Code: "deployment_workflow_missing", Message: "发布配置中的服务器部署工作流不存在"}
			}
		}
		groups[target.VersionGroup] = batch
		for _, source := range matches {
			sources[target.VersionGroup] = append(sources[target.VersionGroup], source)
		}
	}
	for id, batch := range groups {
		if err = s.delivery.Seal(ctx, batch, sources[id]); err != nil {
			return err
		}
	}
	return nil
}

// Check tool identity and workflow migration before spending time compiling.
// Repeat the checks after sealing and before each external publication.
func (s *Service) preflightDeliveryBuild(ctx context.Context, run *store.ReleaseRun, plan *executionPlan) error {
	root := run.RepoRoot
	if plan.CandidateID != "" {
		candidate := s.lookupCandidate(plan.CandidateID)
		if candidate == nil {
			return &Error{Code: "candidate_not_found", Message: "构建候选已失效，请重新预检"}
		}
		root = candidate.Work
	}
	workflows, err := delivery.Workflows(root)
	if err != nil {
		return err
	}
	checked := map[string]bool{}
	for _, target := range plan.Targets {
		if target.Delivery == nil || !target.Selection.Publish || checked[target.VersionGroup] {
			continue
		}
		d := target.Delivery
		if err = s.delivery.Preflight(ctx, delivery.Batch{Repository: d.Repository, Account: d.Account, Workflows: workflows}); err != nil {
			return err
		}
		checked[target.VersionGroup] = true
	}
	return nil
}

func (s *Service) deliveryPreflight(ctx context.Context, run *store.ReleaseRun) error {
	batches, err := s.delivery.Load(run.ID)
	if err != nil {
		return err
	}
	for _, b := range batches {
		if err = s.delivery.Verify(ctx, b); err != nil {
			return err
		}
		if err = s.delivery.Preflight(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) publishDeliveries(ctx context.Context, run *store.ReleaseRun) error {
	batches, err := s.delivery.Load(run.ID)
	if err != nil {
		return err
	}
	for _, b := range batches {
		unlock, err := delivery.Lock(s.store.ReleaseDataDir(), "github:"+strings.ToLower(b.Repository)+":"+b.Tag)
		if err != nil {
			return &Error{Code: "release_in_progress", Message: "该仓库版本已有交付任务执行中"}
		}
		err = s.delivery.Publish(ctx, b)
		unlock()
		if err != nil {
			return err
		}
		for _, f := range b.Files {
			if err = s.store.MarkReleaseTargetStepDone(run.ID, f.TargetID, "publish"); err != nil {
				return err
			}
			if err = s.store.UpdateReleaseTargetRun(run.ID, f.TargetID, "succeeded", "completed", "", "", true, true); err != nil {
				return err
			}
		}
		s.log(run.ID, "event", b.Tag+"：GitHub Release 已发布并核验")
		if err = s.delivery.Sync(ctx, b); err != nil {
			s.log(run.ID, "error", "同步检查状态保存失败")
		}
	}
	return nil
}

func (s *Service) sealedDeliveryReady(run *store.ReleaseRun, plan *executionPlan) bool {
	if plan == nil || !plan.hasDelivery() {
		return false
	}
	// Completed build-only targets need no additional external actions on resume.
	groups := map[string]bool{}
	for _, t := range plan.Targets {
		if strings.EqualFold(t.Runner.Type, "git-push") || t.Selection.Deploy || (t.Selection.Publish && t.Delivery == nil) {
			return false
		}
		if t.Delivery != nil && t.Selection.Publish {
			groups[t.VersionGroup] = true
		}
	}
	batches, err := s.delivery.Load(run.ID)
	if err != nil || len(batches) != len(groups) {
		return false
	}
	for _, b := range batches {
		if !groups[b.GroupID] || b.AppID != run.AppID || b.Commit != run.CommitSHA {
			return false
		}
	}
	return true
}

func (s *Service) failDelivery(run *store.ReleaseRun, stage string, err error) {
	code, message := delivery.ErrorInfo(err)
	if pe, ok := err.(*Error); ok {
		code, message = pe.Code, pe.Message
	}
	s.log(run.ID, "error", message)
	_ = s.store.UpdateReleaseRun(run.ID, "failed", stage, run.CommitSHA, code, message, true)
}

func (s *Service) RetrySync(ctx context.Context, runID string) error {
	batches, err := s.delivery.Load(runID)
	if err != nil {
		return err
	}
	rows, err := s.store.ReleaseDeliveries(runID)
	if err != nil {
		return err
	}
	for _, b := range batches {
		for _, r := range rows {
			if r.GroupID == b.GroupID && r.State == "published" {
				if err = s.delivery.Sync(ctx, b); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func stepTimeout(target planTarget, step string) time.Duration {
	seconds := target.Timeouts[step]
	if seconds == 0 {
		seconds = 600
	}
	return time.Duration(seconds) * time.Second
}

func cloneDelivery(d *releaseconfig.Delivery) *releaseconfig.Delivery {
	if d == nil {
		return nil
	}
	copy := *d
	if d.Sync != nil {
		sync := *d.Sync
		copy.Sync = &sync
	}
	if d.Deployment != nil {
		deployment := *d.Deployment
		copy.Deployment = &deployment
	}
	return &copy
}

func cloneTimeouts(values map[string]int) map[string]int {
	copy := make(map[string]int, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
