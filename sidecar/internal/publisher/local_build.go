package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

const LocalBuildIntent = "build-only"

const localBuildHelp = "复制当前源码到独立目录，执行项目已配置的检查、构建、打包和验证；不改版本、不提交、不创建 Tag、不上传。产物验证后保存在本机，重启仍可取回。首次安装依赖可能需要联网。"

type LocalBuildRequest struct {
	RequestID         string   `json:"requestId"`
	ConfigFingerprint string   `json:"configFingerprint"`
	TargetIDs         []string `json:"targetIds"`
}

type LocalBuildTarget struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	CurrentVersion string `json:"currentVersion"`
	Check          bool   `json:"check"`
	Build          bool   `json:"build"`
	Package        bool   `json:"package"`
	Available      bool   `json:"available"`
	Reason         string `json:"reason,omitempty"`
}

type LocalBuildPreparation struct {
	ProjectRoot       string             `json:"projectRoot"`
	ConfigFingerprint string             `json:"configFingerprint"`
	Targets           []LocalBuildTarget `json:"targets"`
	Help              string             `json:"help"`
}

type localBuildPlan struct {
	executionPlan
	LocalBuildRequestID string            `json:"localBuildRequestId"`
	RequestFingerprint  string            `json:"requestFingerprint"`
	ConfigFingerprint   string            `json:"configFingerprint"`
	SourceSHA256        string            `json:"sourceSha256"`
	SnapshotRoot        string            `json:"snapshotRoot"`
	SourceFiles         map[string]string `json:"sourceFiles"`
}

func parseLocalBuildPlan(run *store.ReleaseRun) (*localBuildPlan, error) {
	if run == nil {
		return nil, &Error{Code: "local_build_not_found", Message: "本地构建记录不存在"}
	}
	plan := &localBuildPlan{}
	if err := json.Unmarshal(run.ExecutionPlan, plan); err != nil || plan.Intent != LocalBuildIntent || plan.SchemaVersion != executionPlanSchemaVersion {
		return nil, &Error{Code: "local_build_not_found", Message: "该记录不是纯本地构建任务"}
	}
	if run.CreateTag || run.PushRemote || run.TagName != "" || plan.hasDelivery() || plan.requiresGitPush() {
		return nil, &Error{Code: "local_build_plan_invalid", Message: "本地构建计划包含不允许的发布操作"}
	}
	for _, target := range plan.Targets {
		if target.Selection.Publish || target.Selection.Deploy || target.Steps.Publish != "" || target.Steps.Deploy != "" || target.Delivery != nil || !strings.EqualFold(target.Runner.Type, releaseconfig.RunnerLocal) {
			return nil, &Error{Code: "local_build_plan_invalid", Message: "本地构建计划包含不允许的发布操作"}
		}
	}
	return plan, nil
}

func (s *Service) PrepareLocalBuild(ctx context.Context, appID string) (*LocalBuildPreparation, error) {
	cfg, err := s.localBuildConfig(ctx, appID)
	if err != nil {
		return nil, err
	}
	return localBuildPreparation(cfg)
}

func (s *Service) localBuildConfig(ctx context.Context, appID string) (*releaseconfig.Config, error) {
	cfg, err := s.releaseConfig.Get(ctx, appID)
	if err != nil {
		return nil, &Error{Code: "local_build_config_invalid", Message: "无法读取本地构建配置：" + err.Error()}
	}
	cfg.RepoRoot = canonicalRepositoryPath(cfg.RepoRoot)
	if cfg.Source == releaseconfig.SourceFile {
		if _, err := secureProjectPath(cfg.RepoRoot, releaseconfig.ManifestPath, false); err != nil {
			return nil, &Error{Code: "local_build_config_invalid", Message: "构建配置路径无效或经过目录链接"}
		}
	}
	return cfg, nil
}

func localBuildPreparation(cfg *releaseconfig.Config) (*LocalBuildPreparation, error) {
	view := &LocalBuildPreparation{ProjectRoot: cfg.RepoRoot, Targets: []LocalBuildTarget{}, Help: localBuildHelp}
	versions := map[string]string{}
	for _, target := range cfg.Targets {
		if !strings.EqualFold(strings.TrimSpace(target.Runner.Type), releaseconfig.RunnerLocal) {
			continue
		}
		entry := LocalBuildTarget{ID: target.ID, Name: target.Name, Kind: target.Kind,
			Check: strings.TrimSpace(target.Steps.Check) != "", Build: strings.TrimSpace(target.Steps.Build) != "", Package: strings.TrimSpace(target.Steps.Package) != ""}
		version, versionErr := localTargetVersion(cfg.RepoRoot, cfg.VersionGroups, target.VersionGroup)
		entry.CurrentVersion = version
		versions[target.VersionGroup] = version
		switch {
		case cfg.Source != releaseconfig.SourceFile:
			entry.Reason = "请先保存项目的本地构建配置，识别结果不会自动执行"
		case !target.Enabled:
			entry.Reason = "此目标已停用"
		case !entry.Build && !entry.Package:
			entry.Reason = "未配置本地构建或打包命令"
		case len(target.Artifacts) == 0 && len(target.ArtifactRules) == 0:
			entry.Reason = "请先配置最终产物的位置"
		case versionErr != nil:
			entry.Reason = versionErr.Error()
		default:
			if err := validateRunner(target.Runner); err != nil {
				entry.Reason = err.Error()
			} else if _, err := secureProjectPath(cfg.RepoRoot, target.WorkingDir, true); err != nil {
				entry.Reason = "目标工作目录不存在或经过目录链接"
			} else {
				entry.Available = true
			}
		}
		view.Targets = append(view.Targets, entry)
	}
	// Bind the portable configuration and current version contents, not Git status.
	// Staged/uncommitted files are normal inputs for a pure local build.
	raw, err := json.Marshal(struct {
		Config   *releaseconfig.Config
		Versions map[string]string
		Targets  []LocalBuildTarget
	}{cfg, versions, view.Targets})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	view.ConfigFingerprint = hex.EncodeToString(sum[:])
	return view, nil
}

func localTargetVersion(root string, groups []releaseconfig.VersionGroup, groupID string) (string, error) {
	for _, group := range groups {
		if group.ID != groupID {
			continue
		}
		return currentGroupVersion(root, planVersionGroup{ID: group.ID, Name: group.Name, CurrentVersion: group.CurrentVersion, VersionFiles: group.VersionFiles})
	}
	return "", fmt.Errorf("本地目标的版本组不存在")
}

func (s *Service) ListLocalBuilds(appID string) ([]*store.ReleaseRun, error) {
	if app, err := s.store.GetApp(appID); err != nil || app == nil {
		return nil, &Error{Code: "app_not_found", Message: "项目不存在"}
	}
	return s.store.ListLocalBuildRuns(appID)
}
