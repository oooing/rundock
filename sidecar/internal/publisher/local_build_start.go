package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

var localBuildRequestPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func localBuildRequestFingerprint(req LocalBuildRequest) (string, error) {
	if !localBuildRequestPattern.MatchString(req.RequestID) || len(req.ConfigFingerprint) != 64 || len(req.TargetIDs) == 0 || len(req.TargetIDs) > 64 {
		return "", &Error{Code: "local_build_request_invalid", Message: "请刷新本地构建设置，并至少选择一个构建目标"}
	}
	seen := map[string]bool{}
	for _, id := range req.TargetIDs {
		if !localBuildRequestPattern.MatchString(id) || seen[id] {
			return "", &Error{Code: "local_build_request_invalid", Message: "构建目标标识无效或重复"}
		}
		seen[id] = true
	}
	ids := append([]string{}, req.TargetIDs...)
	sort.Strings(ids)
	raw, _ := json.Marshal(struct {
		ConfigFingerprint string
		TargetIDs         []string
	}{req.ConfigFingerprint, ids})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) previousLocalBuild(appID string, req LocalBuildRequest, fingerprint string) (*store.ReleaseRun, error) {
	run, err := s.store.LocalBuildForRequest(appID, req.RequestID)
	if err != nil || run == nil {
		return run, err
	}
	plan, err := parseLocalBuildPlan(run)
	if err != nil {
		return nil, err
	}
	if plan.RequestFingerprint != fingerprint {
		return nil, &Error{Code: "local_build_request_conflict", Message: "该构建请求已用于其他选择，请刷新后重新开始"}
	}
	return run, nil
}

// StartLocalBuild is deliberately independent of Start/execute/candidates. A
// local build must never acquire Git commit, Tag or external-delivery authority.
func (s *Service) StartLocalBuild(ctx context.Context, appID string, req LocalBuildRequest) (*store.ReleaseRun, error) {
	fingerprint, err := localBuildRequestFingerprint(req)
	if err != nil {
		return nil, err
	}
	if previous, err := s.previousLocalBuild(appID, req, fingerprint); err != nil || previous != nil {
		return previous, err
	}
	cfg, err := s.localBuildConfig(ctx, appID)
	if err != nil {
		return nil, err
	}
	prepared, err := localBuildPreparation(cfg)
	if err != nil {
		return nil, err
	}
	if req.ConfigFingerprint != prepared.ConfigFingerprint {
		return nil, &Error{Code: "local_build_config_changed", Message: "构建配置或当前版本已变化，请刷新后再开始"}
	}
	plan, err := freezeLocalBuildPlan(cfg, prepared, req, fingerprint)
	if err != nil {
		return nil, err
	}
	if !s.reserve(cfg.RepoRoot) {
		if previous, err := s.previousLocalBuild(appID, req, fingerprint); err != nil || previous != nil {
			return previous, err
		}
		return nil, &Error{Code: "release_in_progress", Message: "该项目已有构建或发布任务进行中，请稍后重试"}
	}
	if previous, err := s.previousLocalBuild(appID, req, fingerprint); err != nil || previous != nil {
		s.release(cfg.RepoRoot)
		return previous, err
	}
	manifest, err := secureProjectPath(cfg.RepoRoot, releaseconfig.ManifestPath, false)
	if err != nil {
		s.release(cfg.RepoRoot)
		return nil, &Error{Code: "local_build_config_invalid", Message: "已保存的构建配置无法读取"}
	}
	configHash, err := localSourceHash(ctx, manifest)
	if err != nil {
		s.release(cfg.RepoRoot)
		return nil, err
	}
	// Recheck semantics after reading manifest bytes, closing the read/config race.
	current, err := s.PrepareLocalBuild(ctx, appID)
	if err != nil || current.ConfigFingerprint != req.ConfigFingerprint {
		s.release(cfg.RepoRoot)
		return nil, &Error{Code: "local_build_config_changed", Message: "构建配置正在变化，请刷新后再开始"}
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		s.release(cfg.RepoRoot)
		return nil, err
	}
	run := &store.ReleaseRun{ID: app.NewRunID(), AppID: appID, RepoRoot: cfg.RepoRoot, PushRemote: false,
		Intent: LocalBuildIntent, CreateTag: false, Versions: plan.ReleaseVersions, SelectedTargets: localBuildSelections(plan.Targets),
		ExecutionPlan: planJSON, Status: "queued", Stage: "local_freezing", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if len(plan.ReleaseVersions) > 0 {
		run.TargetVersion = plan.ReleaseVersions[0].TargetVersion
	}
	if err := s.store.CreateReleaseRun(run); err != nil {
		s.release(cfg.RepoRoot)
		if previous, lookupErr := s.previousLocalBuild(appID, req, fingerprint); lookupErr != nil || previous != nil {
			return previous, lookupErr
		}
		return nil, err
	}
	if err := s.store.CreateReleaseTargetRuns(run.ID, run.SelectedTargets); err != nil {
		s.release(cfg.RepoRoot)
		_ = s.store.UpdateReleaseRun(run.ID, "failed", "local_freezing", "", "local_build_state_failed", "无法保存构建目标记录", true)
		return nil, err
	}
	// Register cancellation before publishing the accepted response or launching
	// the goroutine. A fast cancel cannot miss a build that has not started yet.
	buildCtx, done := s.runContext(run.ID)
	s.log(run.ID, "event", "纯本机构建：使用当前源码和版本，不提交、不创建 Tag、不上传")
	workerRun := *run // HTTP response and asynchronous execution never share mutable fields.
	go s.executeLocalBuild(buildCtx, done, &workerRun, plan, configHash)
	return run, nil
}

func freezeLocalBuildPlan(cfg *releaseconfig.Config, prepared *LocalBuildPreparation, req LocalBuildRequest, fingerprint string) (*localBuildPlan, error) {
	wanted := map[string]bool{}
	for _, id := range req.TargetIDs {
		wanted[id] = true
	}
	availability := map[string]LocalBuildTarget{}
	for _, target := range prepared.Targets {
		availability[target.ID] = target
	}
	plan := &localBuildPlan{executionPlan: executionPlan{SchemaVersion: executionPlanSchemaVersion,
		Intent: LocalBuildIntent, BuildMode: "local", ConfigPath: releaseconfig.ManifestPath, PushRemote: boolPtr(false),
		Targets: []planTarget{}, VersionGroups: []planVersionGroup{}, ReleaseVersions: []store.ReleaseVersion{}},
		LocalBuildRequestID: req.RequestID, RequestFingerprint: fingerprint, ConfigFingerprint: req.ConfigFingerprint}
	groups := map[string]bool{}
	for _, target := range cfg.Targets {
		if !wanted[target.ID] {
			continue
		}
		view, exists := availability[target.ID]
		if !exists || !view.Available {
			reason := view.Reason
			if reason == "" {
				reason = "只能选择已配置的本地构建目标"
			}
			return nil, &Error{Code: "local_build_target_unavailable", Message: target.Name + "：" + reason}
		}
		selection := store.ReleaseTargetSelection{TargetID: target.ID, Build: view.Build, Package: view.Package}
		plan.Targets = append(plan.Targets, planTarget{ID: target.ID, Name: target.Name, Kind: target.Kind,
			VersionGroup: target.VersionGroup, WorkingDir: target.WorkingDir, Runner: target.Runner,
			Timeouts: cloneTimeouts(target.Timeouts), Artifacts: append([]string{}, target.Artifacts...),
			ArtifactRules: append([]releaseconfig.ArtifactRule{}, target.ArtifactRules...), Verification: append([]releaseconfig.Verification{}, target.Verification...),
			Steps: releaseconfig.Steps{Check: target.Steps.Check, Build: target.Steps.Build, Package: target.Steps.Package}, Selection: selection})
		if !groups[target.VersionGroup] {
			plan.ReleaseVersions = append(plan.ReleaseVersions, store.ReleaseVersion{VersionGroupID: target.VersionGroup, TargetVersion: view.CurrentVersion})
			groups[target.VersionGroup] = true
		}
		delete(wanted, target.ID)
	}
	if len(wanted) > 0 || len(plan.Targets) == 0 {
		return nil, &Error{Code: "local_build_target_unavailable", Message: "所选本地构建目标不在当前已保存配置中"}
	}
	for _, group := range cfg.VersionGroups {
		if groups[group.ID] {
			plan.VersionGroups = append(plan.VersionGroups, planVersionGroup{ID: group.ID, Name: group.Name,
				CurrentVersion: group.CurrentVersion, VersionFiles: append([]releaseconfig.VersionFile{}, group.VersionFiles...)})
		}
	}
	return plan, nil
}

func localBuildSelections(targets []planTarget) []store.ReleaseTargetSelection {
	out := make([]store.ReleaseTargetSelection, 0, len(targets))
	for _, target := range targets {
		out = append(out, target.Selection)
	}
	return out
}
