package publisher

import (
	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/store"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Service) repositoryBusy(repo string) bool {
	key := gitPathKey(canonicalRepositoryPath(repo))
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[key]
}

func (s *Service) reserve(repo string) bool {
	key := gitPathKey(canonicalRepositoryPath(repo))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[key] {
		return false
	}
	unlock, err := delivery.Lock(s.store.ReleaseDataDir(), "repo:"+key)
	if err != nil {
		return false
	}
	if s.fileLocks == nil {
		s.fileLocks = map[string]func(){}
	}
	s.fileLocks[key] = unlock
	s.active[key] = true
	return true
}

func (s *Service) release(repo string) {
	key := gitPathKey(canonicalRepositoryPath(repo))
	s.mu.Lock()
	if unlock := s.fileLocks[key]; unlock != nil {
		unlock()
		delete(s.fileLocks, key)
	}
	delete(s.active, key)
	s.mu.Unlock()
}

func (s *Service) log(runID, stream, text string) {
	text = strings.TrimSpace(redact(text))
	if text == "" {
		return
	}
	_, _ = s.store.AddReleaseLog(runID, stream, text)
}

func (s *Service) recordRelease(run *store.ReleaseRun, event diagnostics.Event) {
	if s.diagnostics == nil || run == nil {
		return
	}
	event.AppID = run.AppID
	event.ReleaseRunID = run.ID
	s.diagnostics.Record(event)
}

func validateSelected(paths []string, changes []FileChange) ([]string, error) {
	allowed := map[string]bool{}
	for _, ch := range changes {
		if isUntrackedDiagnosticsChange(ch) {
			continue
		}
		allowed[filepath.ToSlash(ch.Path)] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range paths {
		path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
		if path == "." || path == "" || filepath.IsAbs(raw) || strings.HasPrefix(path, "../") || !allowed[path] {
			return nil, &Error{Code: "invalid_path", Message: "提交文件不在预检列表中：" + raw}
		}
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

func validateTargetSelections(values []store.ReleaseTargetSelection) ([]store.ReleaseTargetSelection, error) {
	out := make([]store.ReleaseTargetSelection, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value.TargetID = strings.TrimSpace(value.TargetID)
		if value.TargetID == "" || strings.ContainsAny(value.TargetID, "\\/\x00") {
			return nil, &Error{Code: "invalid_target", Message: "发布目标标识无效"}
		}
		if seen[value.TargetID] {
			return nil, &Error{Code: "duplicate_target", Message: "发布目标重复：" + value.TargetID}
		}
		if !value.Build && !value.Package && !value.Publish && !value.Deploy {
			return nil, &Error{Code: "target_action_required", Message: "发布目标至少选择一个执行动作：" + value.TargetID}
		}
		seen[value.TargetID] = true
		out = append(out, value)
	}
	return out, nil
}

func requiresExternalActionsConfirmation(values []store.ReleaseTargetSelection) bool {
	for _, value := range values {
		if value.Publish || value.Deploy {
			return true
		}
	}
	return false
}

func (s *Service) SaveProfile(p *store.ReleaseProfile) error {
	if p.BuildMode == "" {
		p.BuildMode = "github"
	}
	if p.BuildMode != "github" && p.BuildMode != "local" {
		return &Error{Code: "invalid_build_mode", Message: "请选择 GitHub 云端构建或本地构建"}
	}
	if p.RemoteName == "" {
		p.RemoteName = "origin"
	}
	if p.VersionStrategy == "" {
		p.VersionStrategy = StrategyAuto
	}
	if p.VersionMode == "" {
		p.VersionMode = "auto"
	}
	if p.VersionMode != "auto" && p.VersionMode != "manual" {
		return &Error{Code: "invalid_version_mode", Message: "版本方式必须是自动递增或手动设置"}
	}
	if p.VersionStrategy != StrategyAuto && p.VersionStrategy != StrategyManual && p.VersionStrategy != StrategyNode && p.VersionStrategy != StrategyTauri {
		return &Error{Code: "invalid_strategy", Message: "不支持的版本策略"}
	}
	return s.store.UpsertReleaseProfile(p)
}

func (s *Service) GetRun(runID string, since int64) (*RunView, error) {
	run, err := s.store.GetReleaseRun(runID)
	if err != nil || run == nil {
		return nil, &Error{Code: "release_not_found", Message: "发布记录不存在"}
	}
	logs, err := s.store.ReleaseLogs(runID, since, 500)
	if err != nil {
		return nil, err
	}
	if logs == nil {
		logs = []*store.ReleaseLog{}
	}
	targets, err := s.store.ReleaseTargetRuns(runID)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.store.ReleaseArtifacts(runID)
	if err != nil {
		return nil, err
	}
	plan, _ := parseExecutionPlan(run.ExecutionPlan)
	confirmationTargets := retryCustomExternalTargets(run, plan, targets)
	cloudBuild, _ := s.store.GetCloudBuild(runID)
	deliveries, err := s.store.ReleaseDeliveries(runID)
	if err != nil {
		return nil, err
	}
	return &RunView{Deliveries: deliveries, Run: run, Targets: targets, Artifacts: artifacts, Logs: logs, Automation: automationHandoffView(run, plan),
		CloudBuild:                cloudBuild,
		RetryConfirmationRequired: len(confirmationTargets) > 0, RetryConfirmationTargets: confirmationTargets}, nil
}

func nonEmptyLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func contains(values []string, wanted string) bool {
	for _, v := range values {
		if v == wanted {
			return true
		}
	}
	return false
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = filepath.ToSlash(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func stageText(stage string) string {
	return map[string]string{
		"preparing": "正在准备发布", "versioning": "正在更新版本文件", "checking": "正在执行发布前检查",
		"committing": "正在创建提交", "tagging": "正在创建 tag", "pushing_branch": "正在推送分支",
		"pushing_tag": "正在推送 tag", "building_targets": "正在构建所选发布目标",
		"target_check": "正在检查", "target_build": "正在构建", "target_package": "正在打包",
		"publishing_targets": "正在交付所选发布目标", "target_publish": "正在上传",
		"local_saving":  "正在保存本地产物",
		"target_deploy": "正在部署", "completed": "发布完成",
		"delivery_preparing": "正在验证并保存产物", "delivery_preflight": "正在核对交付条件", "delivery_publish": "正在上传并核验 GitHub Release",
	}[stage]
}
