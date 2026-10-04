package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const executionPlanSchemaVersion = 1

// executionPlan is frozen into release_runs before any asynchronous work
// starts. Retries consume this snapshot instead of a possibly edited project
// manifest.
type executionPlan struct {
	SkipChecks            bool                      `json:"skipChecks,omitempty"`
	SchemaVersion         int                       `json:"schemaVersion"`
	BuildMode             string                    `json:"buildMode,omitempty"`
	ConfigPath            string                    `json:"configPath"`
	RemoteURL             string                    `json:"remoteUrl,omitempty"`
	PushRemote            *bool                     `json:"pushRemote,omitempty"`
	NamespacedTags        bool                      `json:"namespacedTags,omitempty"`
	ReleaseNotes          string                    `json:"releaseNotes,omitempty"`
	ReleaseNotesConfirmed bool                      `json:"releaseNotesConfirmed,omitempty"`
	Automation            *releaseconfig.Automation `json:"automation,omitempty"`
	VersionGroups         []planVersionGroup        `json:"versionGroups"`
	ReleaseVersions       []store.ReleaseVersion    `json:"releaseVersions,omitempty"`
	Targets               []planTarget              `json:"targets"`
	CandidateID           string                    `json:"candidateId,omitempty"`
	CandidateFingerprint  string                    `json:"candidateFingerprint,omitempty"`
	CandidateTreeHash     string                    `json:"candidateTreeHash,omitempty"`
	Intent                string                    `json:"intent,omitempty"`
}

func (p *executionPlan) requiresGitPush() bool {
	for _, target := range p.Targets {
		if strings.EqualFold(strings.TrimSpace(target.Runner.Type), releaseconfig.RunnerGitPush) {
			return true
		}
	}
	return false
}

// requiresTagPush reports whether a selected cloud target is explicitly
// triggered by a Git tag. This is part of the frozen execution plan, so the
// decision remains stable even if the project manifest is edited later.
func (p *executionPlan) requiresTagPush() bool {
	if p == nil {
		return false
	}
	for _, target := range p.Targets {
		if strings.EqualFold(strings.TrimSpace(target.Runner.Type), releaseconfig.RunnerGitPush) &&
			strings.EqualFold(strings.TrimSpace(target.Steps.Publish), "tag-push") {
			return true
		}
	}
	return false
}

type planVersionGroup struct {
	ID             string                      `json:"id"`
	Name           string                      `json:"name"`
	TagPrefix      string                      `json:"tagPrefix,omitempty"`
	CurrentVersion string                      `json:"currentVersion,omitempty"`
	VersionFiles   []releaseconfig.VersionFile `json:"versionFiles"`
}

type planTarget struct {
	Delivery      *releaseconfig.Delivery      `json:"delivery,omitempty"`
	Timeouts      map[string]int               `json:"timeouts,omitempty"`
	ArtifactRules []releaseconfig.ArtifactRule `json:"artifactRules,omitempty"`
	Verification  []releaseconfig.Verification `json:"verification,omitempty"`
	ID            string                       `json:"id"`
	Name          string                       `json:"name"`
	Kind          string                       `json:"kind"`
	VersionGroup  string                       `json:"versionGroup"`
	WorkingDir    string                       `json:"workingDir"`
	Runner        releaseconfig.Runner         `json:"runner"`
	Steps         releaseconfig.Steps          `json:"steps"`
	Artifacts     []string                     `json:"artifacts"`
	Selection     store.ReleaseTargetSelection `json:"selection"`
}

type targetExecutionError struct {
	Stage    string
	Code     string
	Message  string
	TargetID string
}

func (e *targetExecutionError) Error() string { return e.Message }

func (s *Service) freezeExecutionPlan(ctx context.Context, appID, repoRoot string, selections []store.ReleaseTargetSelection) (*executionPlan, error) {
	plan := &executionPlan{SchemaVersion: executionPlanSchemaVersion, ConfigPath: releaseconfig.ManifestPath,
		VersionGroups: []planVersionGroup{}, Targets: []planTarget{}}
	cfg, err := s.releaseConfig.Get(ctx, appID)
	if err != nil {
		return nil, &Error{Code: "release_config_invalid", Message: "无法读取发布配置：" + err.Error()}
	}
	if !samePath(cfg.RepoRoot, repoRoot) {
		return nil, &Error{Code: "release_config_mismatch", Message: "发布配置所属仓库与当前 Git 仓库不一致"}
	}
	if cfg.Automation != nil {
		automation := *cfg.Automation
		plan.Automation = &automation
	}
	frozenGroups := make(map[string]planVersionGroup, len(cfg.VersionGroups))
	selectedGroups := map[string]bool{}
	for _, selection := range selections {
		for _, target := range cfg.Targets {
			if target.ID == selection.TargetID {
				selectedGroups[target.VersionGroup] = true
			}
		}
	}
	versionFilePaths := []string{}
	for _, group := range cfg.VersionGroups {
		for _, file := range group.VersionFiles {
			// Other targets' files are not part of this selected operation.
			if len(selections) > 0 && !selectedGroups[group.ID] {
				continue
			}
			if _, err := secureProjectPath(repoRoot, file.Path, false); err != nil {
				return nil, &Error{Code: "version_file_invalid", Message: "版本文件无效：" + file.Path}
			}
			versionFilePaths = append(versionFilePaths, file.Path)
		}
		frozenGroups[group.ID] = planVersionGroup{
			ID: group.ID, Name: group.Name, TagPrefix: group.TagPrefix, CurrentVersion: group.CurrentVersion,
			VersionFiles: append([]releaseconfig.VersionFile{}, group.VersionFiles...),
		}
	}
	if ignored := s.ignoredUntrackedPaths(ctx, repoRoot, versionFilePaths); len(ignored) > 0 {
		return nil, &Error{
			Code:    "version_file_ignored",
			Message: "版本文件未被 Git 跟踪且已被忽略：" + strings.Join(ignored, "、") + "；请改用可提交的版本源文件",
		}
	}
	if len(selections) == 0 {
		// “仅提交代码”仍需冻结版本配置，避免配置在异步执行或重试前被修改。
		// 单版本组沿用该组的版本文件；多个独立版本组则保留项目级
		// vX.Y.Z 语义，不能在用户没有选择平台时任意提升其中一组。
		for _, group := range cfg.VersionGroups {
			plan.VersionGroups = append(plan.VersionGroups, frozenGroups[group.ID])
		}
		return plan, nil
	}
	plan.NamespacedTags = len(cfg.VersionGroups) > 1
	targets := make(map[string]releaseconfig.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets[target.ID] = target
	}
	usedGroups := map[string]bool{}
	for _, selection := range selections {
		target, ok := targets[selection.TargetID]
		if !ok {
			return nil, &Error{Code: "target_not_configured", Message: "发布目标不在已保存配置中：" + selection.TargetID}
		}
		if !target.Enabled {
			return nil, &Error{Code: "target_disabled", Message: "发布目标当前不可用：" + target.Name}
		}
		_, ok = frozenGroups[target.VersionGroup]
		if !ok {
			return nil, &Error{Code: "version_group_missing", Message: "发布目标引用的版本组不存在：" + target.VersionGroup}
		}
		if err := validateRunner(target.Runner); err != nil {
			return nil, err
		}
		if _, err := secureProjectPath(repoRoot, target.WorkingDir, true); err != nil {
			return nil, &Error{Code: "target_working_dir_invalid", Message: target.Name + " 的工作目录无效：" + err.Error()}
		}
		if err := validateSelectedCommands(target, selection); err != nil {
			return nil, err
		}
		plan.Targets = append(plan.Targets, planTarget{ID: target.ID, Name: target.Name, Kind: target.Kind,
			VersionGroup: target.VersionGroup, WorkingDir: target.WorkingDir, Runner: target.Runner,
			Delivery: cloneDelivery(target.Delivery), Timeouts: cloneTimeouts(target.Timeouts), ArtifactRules: append([]releaseconfig.ArtifactRule{}, target.ArtifactRules...), Verification: append([]releaseconfig.Verification{}, target.Verification...), Steps: target.Steps, Artifacts: append([]string{}, target.Artifacts...), Selection: selection})
		usedGroups[target.VersionGroup] = true
	}
	for _, group := range cfg.VersionGroups {
		if usedGroups[group.ID] {
			plan.VersionGroups = append(plan.VersionGroups, frozenGroups[group.ID])
		}
	}
	return plan, nil
}

// usesConfiguredVersionGroups reports whether this run has an unambiguous
// configured version scope. A source-only run with several independent groups
// is repository-scoped; choosing which group advances requires a target.
func (p *executionPlan) usesConfiguredVersionGroups() bool {
	if p == nil || len(p.VersionGroups) == 0 {
		return false
	}
	return len(p.Targets) > 0 || len(p.VersionGroups) == 1
}

func validateRunner(r releaseconfig.Runner) error {
	runnerType := strings.ToLower(strings.TrimSpace(r.Type))
	if runnerType == releaseconfig.RunnerGitPush {
		return nil
	}
	if runnerType != releaseconfig.RunnerLocal {
		return &Error{Code: "runner_unavailable", Message: "当前版本仅支持本机 Runner"}
	}
	if len(r.OS) == 0 {
		return nil
	}
	for _, value := range r.OS {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "any" || value == runtime.GOOS {
			return nil
		}
	}
	return &Error{Code: "runner_unavailable", Message: "当前电脑无法执行所选发布目标"}
}

func validateSelectedCommands(target releaseconfig.Target, selection store.ReleaseTargetSelection) error {
	if strings.EqualFold(strings.TrimSpace(target.Runner.Type), releaseconfig.RunnerGitPush) {
		if !selection.Publish || selection.Build || selection.Package || selection.Deploy {
			return &Error{Code: "target_action_unconfigured", Message: target.Name + " 只能使用“推送后触发云端构建”"}
		}
		return nil
	}
	required := []struct {
		selected bool
		command  string
		name     string
	}{
		{selection.Build, target.Steps.Build, "构建"},
		{selection.Package, target.Steps.Package, "打包"},
		{selection.Publish && target.Delivery == nil, target.Steps.Publish, "上传"},
		{selection.Deploy, target.Steps.Deploy, "部署"},
	}
	for _, step := range required {
		if step.selected && strings.TrimSpace(step.command) == "" {
			return &Error{Code: "target_action_unconfigured", Message: target.Name + " 尚未配置“" + step.name + "”命令"}
		}
	}
	return nil
}

func (p *executionPlan) marshal() (json.RawMessage, error) {
	raw, err := json.Marshal(p)
	return json.RawMessage(raw), err
}

func (p *executionPlan) validateVersionScope(createTag bool) error {
	return nil
}

func parseExecutionPlan(raw json.RawMessage) (*executionPlan, error) {
	plan := &executionPlan{SchemaVersion: executionPlanSchemaVersion, VersionGroups: []planVersionGroup{}, Targets: []planTarget{}}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return plan, nil
	}
	if err := json.Unmarshal(raw, plan); err != nil {
		return nil, fmt.Errorf("invalid frozen execution plan: %w", err)
	}
	if plan.SchemaVersion != executionPlanSchemaVersion {
		return nil, fmt.Errorf("unsupported execution plan schema: %d", plan.SchemaVersion)
	}
	return plan, nil
}

func (p *executionPlan) versionCandidates(repo string) ([]string, error) {
	values := []string{}
	for _, group := range p.VersionGroups {
		if len(group.VersionFiles) == 0 && group.CurrentVersion != "" {
			values = append(values, group.CurrentVersion)
		}
		for _, file := range group.VersionFiles {
			value, err := readConfiguredVersion(repo, file)
			if err != nil {
				return nil, err
			}
			if value != "" {
				values = append(values, value)
			}
		}
	}
	return values, nil
}

func (g planVersionGroup) versionCandidates(repo string) ([]string, error) {
	values := []string{}
	if len(g.VersionFiles) == 0 && g.CurrentVersion != "" {
		values = append(values, g.CurrentVersion)
	}
	for _, file := range g.VersionFiles {
		value, err := readConfiguredVersion(repo, file)
		if err != nil {
			return nil, err
		}
		if value != "" {
			values = append(values, value)
		}
	}
	return values, nil
}

func (p *executionPlan) releaseVersionForGroup(groupID string) (store.ReleaseVersion, bool) {
	for _, version := range p.ReleaseVersions {
		if version.VersionGroupID == groupID {
			return version, true
		}
	}
	return store.ReleaseVersion{}, false
}

func (p *executionPlan) versionFiles() []releaseconfig.VersionFile {
	seen := map[string]bool{}
	out := []releaseconfig.VersionFile{}
	for _, group := range p.VersionGroups {
		for _, file := range group.VersionFiles {
			path := filepath.ToSlash(file.Path)
			if path != "" && !seen[path] {
				seen[path] = true
				file.Path = path
				out = append(out, file)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
