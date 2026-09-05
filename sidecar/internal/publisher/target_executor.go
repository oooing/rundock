package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

const executionPlanSchemaVersion = 1

// executionPlan is frozen into release_runs before any asynchronous work
// starts. Retries consume this snapshot instead of a possibly edited project
// manifest.
type executionPlan struct {
	SchemaVersion         int                       `json:"schemaVersion"`
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
	ID           string                       `json:"id"`
	Name         string                       `json:"name"`
	Kind         string                       `json:"kind"`
	VersionGroup string                       `json:"versionGroup"`
	WorkingDir   string                       `json:"workingDir"`
	Runner       releaseconfig.Runner         `json:"runner"`
	Steps        releaseconfig.Steps          `json:"steps"`
	Artifacts    []string                     `json:"artifacts"`
	Selection    store.ReleaseTargetSelection `json:"selection"`
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
	versionFilePaths := []string{}
	for _, group := range cfg.VersionGroups {
		for _, file := range group.VersionFiles {
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
			Steps: target.Steps, Artifacts: append([]string{}, target.Artifacts...), Selection: selection})
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
		{selection.Publish, target.Steps.Publish, "上传"},
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

func samePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func secureProjectPath(root, relativePath string, requireDir bool) (string, error) {
	if strings.TrimSpace(relativePath) == "" {
		relativePath = "."
	}
	if filepath.IsAbs(relativePath) || filepath.VolumeName(relativePath) != "" {
		return "", errors.New("必须使用项目内的相对路径")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(rootAbs, filepath.FromSlash(relativePath))
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("路径不能跳出项目目录")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if requireDir && !info.IsDir() {
		return "", errors.New("路径不是目录")
	}
	// Treat the selected repository root as the trust boundary, but reject any
	// symlink or Windows reparse point below it. This avoids following a
	// repository-owned link outside the project and works even where resolving
	// ancestors (for example a sandboxed system Temp directory) is forbidden.
	current := rootAbs
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			linkInfo, linkErr := os.Lstat(current)
			if linkErr != nil {
				return "", linkErr
			}
			if isPathLink(linkInfo) {
				return "", errors.New("项目内路径不能经过符号链接或目录联接")
			}
		}
	}
	return filepath.Clean(path), nil
}

var (
	jsonPackageVersionRE = regexp.MustCompile(`(?m)("version"\s*:\s*")([0-9]+\.[0-9]+\.[0-9]+)(")`)
	jsonNestedVersionRE  = regexp.MustCompile(`(?ms)("package"\s*:\s*\{.*?"version"\s*:\s*")([0-9]+\.[0-9]+\.[0-9]+)(")`)
	gradleVersionRE      = regexp.MustCompile(`(?m)(versionName\s*(?:=\s*)?["'])([^"']+)(["'])`)
)

func configuredVersionPattern(file releaseconfig.VersionFile) (*regexp.Regexp, error) {
	switch strings.ToLower(strings.TrimSpace(file.Format)) {
	case "json":
		switch file.JSONPointer {
		case "", "/version":
			return jsonPackageVersionRE, nil
		case "/package/version":
			return jsonNestedVersionRE, nil
		default:
			return nil, fmt.Errorf("unsupported JSON version pointer %s", file.JSONPointer)
		}
	case "cargo", "toml":
		return cargoPackageRE, nil
	case "npm-lock", "cargo-lock":
		return nil, nil
	case "gradle":
		return gradleVersionRE, nil
	default:
		return nil, fmt.Errorf("unsupported version file format %s", file.Format)
	}
}

func readConfiguredVersion(repo string, file releaseconfig.VersionFile) (string, error) {
	path, err := secureProjectPath(repo, file.Path, false)
	if err != nil {
		return "", fmt.Errorf("read version file %s: %w", file.Path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	format := strings.ToLower(strings.TrimSpace(file.Format))
	if format == "npm-lock" {
		value := readNpmLockVersion(path)
		if value == "" {
			return "", fmt.Errorf("version field not found or inconsistent in %s", file.Path)
		}
		return value, nil
	}
	if format == "cargo-lock" {
		manifest, manifestErr := cargoManifestForLock(repo, file.Path, nil)
		if manifestErr != nil {
			return "", fmt.Errorf("version file %s: %w", file.Path, manifestErr)
		}
		target, targetErr := findCargoLockRootPackage(raw, manifest)
		if targetErr != nil {
			return "", fmt.Errorf("version file %s: %w", file.Path, targetErr)
		}
		return target.version, nil
	}
	pattern, err := configuredVersionPattern(file)
	if err != nil {
		return "", err
	}
	match := pattern.FindSubmatch(raw)
	if len(match) < 3 {
		return "", fmt.Errorf("version field not found in %s", file.Path)
	}
	return string(match[2]), nil
}

func updateConfiguredVersionFiles(repo string, files []releaseconfig.VersionFile, version string) (map[string][]byte, error) {
	originals := map[string][]byte{}
	type pendingWrite struct {
		path  string
		after []byte
	}
	pending := make([]pendingWrite, 0, len(files))

	// Read every configured source before changing anything. Cargo.lock must be
	// matched against the pre-update Cargo.toml version even when Cargo.toml is
	// listed first in the group.
	for _, file := range files {
		path, err := secureProjectPath(repo, file.Path, false)
		if err != nil {
			return originals, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			return originals, err
		}
		originals[path] = append([]byte(nil), before...)
	}
	for _, file := range files {
		path, err := secureProjectPath(repo, file.Path, false)
		if err != nil {
			return originals, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		before := originals[path]
		after, err := configuredVersionBytes(repo, file, before, originals, version)
		if err != nil {
			return originals, err
		}
		pending = append(pending, pendingWrite{path: path, after: after})
	}
	for _, write := range pending {
		path := write.path
		info, _ := os.Stat(path)
		mode := fs.FileMode(0o644)
		if info != nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(path, write.after, mode); err != nil {
			return originals, err
		}
	}
	return originals, nil
}

func expectedConfiguredVersionWrites(repo string, files []releaseconfig.VersionFile, originals map[string][]byte, version string) map[string][]byte {
	expected := map[string][]byte{}
	for _, file := range files {
		path, err := secureProjectPath(repo, file.Path, false)
		if err != nil {
			continue
		}
		before, ok := originals[path]
		if !ok {
			continue
		}
		if after, err := configuredVersionBytes(repo, file, before, originals, version); err == nil {
			expected[path] = after
		}
	}
	return expected
}

func configuredVersionBytes(repo string, file releaseconfig.VersionFile, before []byte, originals map[string][]byte, version string) ([]byte, error) {
	format := strings.ToLower(strings.TrimSpace(file.Format))
	switch format {
	case "npm-lock":
		after, ok := replaceVersionBytesForPath(file.Path, before, version)
		if !ok {
			return nil, fmt.Errorf("version field not found in %s", file.Path)
		}
		return after, nil
	case "cargo-lock":
		manifest, err := cargoManifestForLock(repo, file.Path, originals)
		if err != nil {
			return nil, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		after, err := replaceCargoLockRootVersion(before, manifest, version)
		if err != nil {
			return nil, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		return after, nil
	default:
		pattern, err := configuredVersionPattern(file)
		if err != nil {
			return nil, err
		}
		if !pattern.Match(before) {
			return nil, fmt.Errorf("version field not found in %s", file.Path)
		}
		after, _ := replaceFirstVersion(before, pattern, version)
		return after, nil
	}
}

type worktreeEntry struct {
	Status  string
	Tracked bool
	Hash    string
}

type worktreeSnapshot map[string]worktreeEntry

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
	return s.executeTargetPhaseInternal(ctx, run, plan, false, true)
}

func (s *Service) executeTargetPhase(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, postPush bool) error {
	return s.executeTargetPhaseInternal(ctx, run, plan, postPush, false)
}

func (s *Service) executeTargetPhaseInternal(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, postPush, checksOnly bool) error {
	if len(plan.Targets) == 0 {
		return nil
	}
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
			_ = s.store.UpdateReleaseRun(run.ID, "running", "target_publish", run.CommitSHA, "", "", false)
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "running", "publish", "", "", true, false)
			s.log(run.ID, "event", target.Name+"：分支已推送，云端流水线已触发")
			if err := s.store.MarkReleaseTargetStepDone(run.ID, target.ID, "publish"); err != nil {
				return err
			}
			targetRun.PublishDone = true
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "handed_off", "cloud_pending", "", "", true, true)
			continue
		}
		workingDir, err := secureProjectPath(run.RepoRoot, target.WorkingDir, true)
		if err != nil {
			_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", "checking", "target_working_dir_invalid", err.Error(), true, true)
			return &targetExecutionError{Stage: "target_check", Code: "target_working_dir_invalid", Message: err.Error(), TargetID: target.ID}
		}
		steps := targetStepsForPhase(target, targetRun, postPush, checksOnly)
		for _, step := range steps {
			stage := "target_" + step.name
			if postPush && (step.name == "publish" || step.name == "deploy") {
				if err := s.verifyFrozenTargetArtifacts(run, target); err != nil {
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
			stepCtx, cancel := commandContext(ctx, 10*time.Minute)
			out, commandErr := runCheckCommand(stepCtx, s.targetRunner, workingDir, command)
			cancel()
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
			if err := s.captureTargetArtifacts(run, target, workingDir); err != nil {
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
	for _, pattern := range target.Artifacts {
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

func (s *Service) captureTargetArtifacts(run *store.ReleaseRun, target planTarget, workingDir string) error {
	canonicalRoot, err := secureProjectPath(run.RepoRoot, ".", true)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	count := 0
	for _, pattern := range target.Artifacts {
		base := artifactWalkBase(workingDir, pattern)
		if _, err := os.Stat(base); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		baseRel, err := filepath.Rel(canonicalRoot, base)
		if err != nil || baseRel == ".." || strings.HasPrefix(baseRel, ".."+string(filepath.Separator)) {
			return errors.New("产物路径通过符号链接跳出了项目目录")
		}
		base, err = secureProjectPath(canonicalRoot, baseRel, true)
		if err != nil {
			return errors.New("产物路径无效：" + err.Error())
		}
		err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			linkInfo, linkErr := os.Lstat(path)
			if linkErr != nil {
				return linkErr
			}
			if isPathLink(linkInfo) {
				return errors.New("产物目录包含符号链接或目录联接")
			}
			entryRel, relErr := filepath.Rel(canonicalRoot, path)
			if relErr != nil || entryRel == ".." || strings.HasPrefix(entryRel, ".."+string(filepath.Separator)) {
				return errors.New("产物目录包含指向项目外部的链接")
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			relWorking, err := filepath.Rel(workingDir, path)
			if err != nil || !globPattern(pattern).MatchString(filepath.ToSlash(relWorking)) {
				return nil
			}
			relRepo, err := filepath.Rel(canonicalRoot, path)
			if err != nil || relRepo == ".." || strings.HasPrefix(relRepo, ".."+string(filepath.Separator)) || seen[relRepo] {
				return nil
			}
			seen[relRepo] = true
			count++
			if count > 2000 {
				return errors.New("产物文件超过 2000 个，请缩小产物匹配范围")
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			hash, err := hashArtifact(path)
			if err != nil {
				return err
			}
			return s.store.AddReleaseArtifact(run.ID, target.ID, filepath.ToSlash(relRepo), info.Size(), hash)
		})
		if err != nil {
			return err
		}
	}
	if len(target.Artifacts) > 0 && count == 0 {
		return errors.New(target.Name + " 未找到配置中声明的构建产物")
	}
	return nil
}

func (s *Service) verifyFrozenTargetArtifacts(run *store.ReleaseRun, target planTarget) error {
	artifacts, err := s.store.ReleaseArtifacts(run.ID)
	if err != nil {
		return err
	}
	found := 0
	for _, artifact := range artifacts {
		if artifact.TargetID != target.ID {
			continue
		}
		found++
		path, pathErr := secureProjectPath(run.RepoRoot, artifact.Path, false)
		if pathErr != nil {
			return fmt.Errorf("%s 不可读取", artifact.Path)
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != artifact.SizeBytes {
			return fmt.Errorf("%s 的大小或类型与构建完成时不一致", artifact.Path)
		}
		hash, hashErr := hashArtifact(path)
		if hashErr != nil || !strings.EqualFold(hash, artifact.SHA256) {
			return fmt.Errorf("%s 的内容与构建完成时不一致", artifact.Path)
		}
	}
	if len(target.Artifacts) > 0 && found == 0 {
		return errors.New("缺少构建完成时记录的产物")
	}
	return nil
}

func artifactWalkBase(workingDir, pattern string) string {
	pattern = filepath.FromSlash(pattern)
	index := strings.IndexAny(pattern, "*?")
	prefix := pattern
	if index >= 0 {
		prefix = pattern[:index]
	}
	base := ""
	if strings.HasSuffix(prefix, string(filepath.Separator)) {
		base = strings.TrimSuffix(prefix, string(filepath.Separator))
	} else {
		base = filepath.Dir(prefix)
	}
	if base == "" || base == "." {
		return workingDir
	}
	return filepath.Join(workingDir, base)
}

func hashArtifact(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func stepLabel(step string) string {
	return map[string]string{"check": "检查", "build": "构建", "package": "打包", "publish": "上传", "deploy": "部署"}[step]
}

func truncateTargetOutput(value string) string {
	const limit = 64 * 1024
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n…输出过长，已截断…"
}
