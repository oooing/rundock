package publisher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

type Service struct {
	store         *store.Store
	releaseConfig *releaseconfig.Service
	runner        commandRunner
	targetRunner  commandRunner
	mu            sync.Mutex
	active        map[string]bool
	diagnostics   *diagnostics.Service
}

const releasePushTimeout = 2 * time.Minute

func New(st *store.Store) *Service {
	return &Service{store: st, releaseConfig: releaseconfig.New(st), runner: execRunner{}, targetRunner: execRunner{}, active: map[string]bool{}}
}

// SetDiagnostics injects the optional project-local diagnostic sink. Writing
// diagnostics is best effort and never changes a release result.
func (s *Service) SetDiagnostics(value *diagnostics.Service) { s.diagnostics = value }

func (s *Service) Preflight(ctx context.Context, appID string) (*Preflight, error) {
	return s.preflight(ctx, appID, true)
}

// PreflightLocal performs the fast, local-only portion used to render the
// release panel. A full Preflight is still required before a release starts.
func (s *Service) PreflightLocal(ctx context.Context, appID string) (*Preflight, error) {
	return s.preflight(ctx, appID, false)
}

func (s *Service) preflight(ctx context.Context, appID string, checkRemote bool) (*Preflight, error) {
	a, err := s.store.GetApp(appID)
	if err != nil || a == nil {
		return nil, &Error{Code: "app_not_found", Message: "项目不存在"}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, &Error{Code: "git_not_found", Message: "未找到 Git，请先安装并加入 PATH"}
	}
	profile, err := s.store.GetReleaseProfile(appID)
	if err != nil {
		return nil, err
	}
	lookupCtx, cancel := commandContext(ctx, 15*time.Second)
	root, err := s.git(lookupCtx, a.Cwd, "rev-parse", "--show-toplevel")
	cancel()
	if err != nil || root == "" {
		return nil, &Error{Code: "not_repository", Message: "项目目录不在 Git 仓库中"}
	}
	root, _ = filepath.Abs(root)
	pf := &Preflight{
		RepoRoot: filepath.Clean(root), Profile: profile, RemoteName: profile.RemoteName,
		CurrentVersions: map[string]string{}, LatestGroupTags: map[string]string{}, SuggestedVersions: map[string]string{},
		UnpushedChanges: []CommittedFileChange{}, BlockingIssues: []Issue{}, RemoteChecked: checkRemote,
	}
	if pf.RemoteName == "" {
		pf.RemoteName = "origin"
	}

	ctx15, cancel15 := commandContext(ctx, 15*time.Second)
	defer cancel15()
	pf.Branch, _ = s.git(ctx15, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	pf.HeadSHA, _ = s.git(ctx15, root, "rev-parse", "HEAD")
	remotesRaw, _ := s.git(ctx15, root, "remote")
	pf.Remotes = nonEmptyLines(remotesRaw)
	if pf.Branch == "" {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "detached_head", Message: "当前仓库处于 detached HEAD，不能发布"})
	}
	if !contains(pf.Remotes, pf.RemoteName) {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "remote_missing", Message: "远程仓库不存在：" + pf.RemoteName})
	} else {
		url, _ := s.git(ctx15, root, "remote", "get-url", pf.RemoteName)
		pf.RemoteURL = redact(url)
	}
	if s.repositoryOperationActive(ctx15, root) {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "repository_operation", Message: "仓库正在进行合并、变基或其他 Git 操作，请先完成或中止"})
	}
	conflicts, _ := s.git(ctx15, root, "diff", "--name-only", "--diff-filter=U")
	if strings.TrimSpace(conflicts) != "" {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "merge_conflict", Message: "仓库存在未解决的合并冲突"})
	}
	statusRaw, statusErr := s.gitRaw(ctx15, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if statusErr != nil {
		return nil, &Error{Code: "git_status_failed", Message: "无法读取 Git 状态"}
	}
	pf.Changes = parseChanges(statusRaw)
	for _, ch := range pf.Changes {
		if ch.Staged {
			pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "staged_changes", Message: "存在已暂存内容，请先提交或取消暂存"})
			break
		}
	}
	pf.StatusFingerprint = statusFingerprint(root, pf.HeadSHA, statusRaw, pf.Changes)

	strategy, files, versions := detectVersionStrategy(root, profile.VersionStrategy)
	pf.VersionStrategy, pf.VersionFiles = strategy, files
	pf.CurrentVersions = map[string]string{}
	versionFilePaths := append([]string{}, files...)
	for path, version := range versions {
		pf.CurrentVersions[path] = version
	}
	var sourceOnlyVersionGroup *planVersionGroup
	repositoryScopedSourceOnly := false
	configuredVersionGroups := []releaseconfig.VersionGroup{}
	if cfg, configErr := s.releaseConfig.Get(ctx, appID); configErr != nil {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "release_config_invalid", Message: "已保存的发布配置无效：" + configErr.Error()})
	} else {
		configuredVersionGroups = append(configuredVersionGroups, cfg.VersionGroups...)
		configuredFiles := []releaseconfig.VersionFile{}
		for _, group := range cfg.VersionGroups {
			configuredFiles = append(configuredFiles, group.VersionFiles...)
			for _, file := range group.VersionFiles {
				versionFilePaths = append(versionFilePaths, file.Path)
			}
			if len(group.VersionFiles) == 0 && group.CurrentVersion != "" {
				pf.CurrentVersions["versionGroup:"+group.ID] = group.CurrentVersion
			}
		}
		for _, file := range (&executionPlan{VersionGroups: []planVersionGroup{{VersionFiles: configuredFiles}}}).versionFiles() {
			value, readErr := readConfiguredVersion(root, file)
			if readErr != nil {
				pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "version_file_invalid", Message: "无法读取版本文件：" + file.Path + "（" + readErr.Error() + "）"})
				continue
			}
			pf.CurrentVersions[file.Path] = value
		}
		if len(cfg.VersionGroups) == 1 {
			group := cfg.VersionGroups[0]
			frozen := planVersionGroup{
				ID: group.ID, Name: group.Name, TagPrefix: group.TagPrefix, CurrentVersion: group.CurrentVersion,
				VersionFiles: append([]releaseconfig.VersionFile{}, group.VersionFiles...),
			}
			sourceOnlyVersionGroup = &frozen
			// Allocate a fresh slice: pf.VersionFiles initially aliases the
			// auto-detected `files` slice, which is validated below.
			pf.VersionFiles = []string{}
			for _, file := range frozen.VersionFiles {
				pf.VersionFiles = append(pf.VersionFiles, filepath.ToSlash(file.Path))
			}
		} else if len(cfg.VersionGroups) > 1 {
			// With independent endpoint versions, a source-only release is a
			// repository Tag and must not silently advance any endpoint group.
			repositoryScopedSourceOnly = true
			pf.VersionFiles = []string{}
		}
	}
	for _, file := range files {
		if !fileExists(filepath.Join(root, filepath.FromSlash(file))) || versions[file] == "" {
			pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "version_file_invalid", Message: "无法读取版本文件：" + file})
		}
	}
	if ignored := s.ignoredUntrackedPaths(ctx15, root, versionFilePaths); len(ignored) > 0 {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{
			Code:    "version_file_ignored",
			Message: "版本文件未被 Git 跟踪且已被忽略：" + strings.Join(ignored, "、") + "；请改用可提交的版本源文件",
		})
	}
	if paths, diagnosticsErr := s.untrackedDiagnosticsPaths(ctx15, root, versionFilePaths); diagnosticsErr != nil {
		return nil, &Error{Code: "git_status_failed", Message: "无法确认诊断目录中的版本文件是否已被 Git 跟踪"}
	} else if len(paths) > 0 {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{
			Code:    "diagnostics_version_file_untracked",
			Message: "诊断目录中的未跟踪文件不能作为版本文件：" + strings.Join(paths, "、"),
		})
	}
	localTags, _ := s.git(ctx15, root, "tag", "--list")
	tagNames := nonEmptyLines(localTags)
	suggestionValues := mapValues(versions)
	if repositoryScopedSourceOnly {
		suggestionValues = nil
	} else if sourceOnlyVersionGroup != nil {
		if configuredValues, configuredErr := sourceOnlyVersionGroup.versionCandidates(root); configuredErr == nil && len(configuredValues) > 0 {
			suggestionValues = configuredValues
		}
	}
	refreshSuggestions := func() {
		pf.LatestGroupTags = map[string]string{}
		pf.SuggestedVersions = map[string]string{}
		pf.LatestTag, _ = latestTagForPrefix(tagNames, "")
		pf.SuggestedVersion = suggestReleaseVersion(suggestionValues, pf.LatestTag)
		if len(configuredVersionGroups) == 1 {
			pf.SuggestedVersions[configuredVersionGroups[0].ID] = pf.SuggestedVersion
		}
		if len(configuredVersionGroups) <= 1 {
			return
		}
		for _, group := range configuredVersionGroups {
			prefix := strings.TrimSpace(group.TagPrefix)
			if prefix == "" {
				prefix = group.ID
			}
			latestTag, latestVersion := latestTagForPrefix(tagNames, prefix)
			if latestTag != "" {
				pf.LatestGroupTags[group.ID] = latestTag
			}
			frozen := planVersionGroup{
				ID: group.ID, Name: group.Name, TagPrefix: group.TagPrefix, CurrentVersion: group.CurrentVersion,
				VersionFiles: append([]releaseconfig.VersionFile{}, group.VersionFiles...),
			}
			values, versionErr := frozen.versionCandidates(root)
			if versionErr == nil {
				if latestVersion != "" {
					values = append(values, latestVersion)
				}
				pf.SuggestedVersions[group.ID] = nextPatch(values...)
			}
		}
	}
	refreshSuggestions()

	if checkRemote && contains(pf.Remotes, pf.RemoteName) && pf.Branch != "" {
		fetchCtx, fetchCancel := commandContext(ctx, 45*time.Second)
		_, fetchErr := s.git(fetchCtx, root, "fetch", pf.RemoteName, pf.Branch, "--no-tags")
		fetchCancel()
		if fetchErr != nil {
			pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "fetch_failed", Message: "无法获取远程分支，请检查网络和 Git 凭据"})
		} else {
			tagCtx, tagCancel := commandContext(ctx, 30*time.Second)
			remoteTags, tagErr := s.git(tagCtx, root, "ls-remote", "--tags", pf.RemoteName)
			tagCancel()
			if tagErr != nil {
				pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "remote_tag_check_failed", Message: "无法检查远程 Tag，请检查网络和 Git 凭据"})
			} else {
				tagNames = dedupe(append(tagNames, remoteTagNames(remoteTags)...))
				refreshSuggestions()
			}
			countCtx, countCancel := commandContext(ctx, 10*time.Second)
			counts, _ := s.git(countCtx, root, "rev-list", "--left-right", "--count", "HEAD...FETCH_HEAD")
			countCancel()
			parts := strings.Fields(counts)
			if len(parts) == 2 {
				localAhead, _ := strconv.Atoi(parts[0])
				remoteAhead, _ := strconv.Atoi(parts[1])
				pf.AheadCount = localAhead
				if localAhead > 0 {
					diffCtx, diffCancel := commandContext(ctx, 10*time.Second)
					diffRaw, diffErr := s.gitRaw(diffCtx, root, "diff", "--name-status", "-z", "FETCH_HEAD...HEAD")
					diffCancel()
					if diffErr == nil {
						pf.UnpushedChanges = parseCommittedChanges(diffRaw)
					}
				}
				if remoteAhead > 0 {
					pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "branch_behind", Message: "当前分支落后或已与远程分叉，请先同步代码"})
				}
			}
		}
	}
	pf.CanRelease = len(pf.BlockingIssues) == 0
	return pf, nil
}

func (s *Service) repositoryOperationActive(ctx context.Context, repo string) bool {
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD", "rebase-merge", "rebase-apply"} {
		path, err := s.git(ctx, repo, "rev-parse", "--git-path", marker)
		if err != nil || path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(repo, path)
		}
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func (s *Service) ensureFrozenCommit(ctx context.Context, run *store.ReleaseRun) error {
	if run == nil || strings.TrimSpace(run.CommitSHA) == "" {
		return &Error{Code: "release_commit_missing", Message: "发布记录缺少已冻结的提交，请重新执行发布"}
	}
	head, err := s.git(ctx, run.RepoRoot, "rev-parse", "HEAD")
	if err != nil || head != run.CommitSHA {
		return &Error{Code: "release_commit_changed", Message: "仓库当前提交已变化，请切回本次发布提交后再重试"}
	}
	return nil
}

func (s *Service) Start(ctx context.Context, appID string, req CreateRequest) (*store.ReleaseRun, error) {
	pf, err := s.Preflight(ctx, appID)
	if err != nil {
		return nil, err
	}
	if !pf.CanRelease {
		issue := pf.BlockingIssues[0]
		return nil, &Error{Code: issue.Code, Message: issue.Message}
	}
	if req.StatusFingerprint == "" || req.StatusFingerprint != pf.StatusFingerprint {
		return nil, &Error{Code: "status_changed", Message: "仓库内容已变化，请重新检查后再发布"}
	}
	createTag := pf.Profile.CreateTag
	if req.CreateTag != nil {
		createTag = *req.CreateTag
	}
	pushRemote := true
	if req.PushRemote != nil {
		pushRemote = *req.PushRemote
	}
	if req.VersionMode == "" {
		req.VersionMode = pf.Profile.VersionMode
	}
	if req.VersionMode != "auto" && req.VersionMode != "manual" {
		return nil, &Error{Code: "invalid_version_mode", Message: "版本方式必须是自动递增或手动设置"}
	}
	selected, err := validateSelected(req.SelectedPaths, pf.Changes)
	if err != nil {
		return nil, err
	}
	selectedTargets, err := validateTargetSelections(req.SelectedTargets)
	if err != nil {
		return nil, err
	}
	if requiresExternalActionsConfirmation(selectedTargets) && !req.ExternalActionsConfirmed {
		return nil, &Error{Code: "external_actions_confirmation_required", Message: "上传或部署会影响外部环境，请明确确认后再继续"}
	}
	plan, err := s.freezeExecutionPlan(ctx, appID, pf.RepoRoot, selectedTargets)
	if err != nil {
		return nil, err
	}
	plan.RemoteURL = pf.RemoteURL
	plan.PushRemote = &pushRemote
	if plan.requiresTagPush() && !createTag {
		return nil, &Error{Code: "cloud_tag_required", Message: "所选云端构建由 Tag 触发，请开启“创建版本 Tag”"}
	}
	if createTag {
		if !req.ReleaseNotesConfirmed {
			return nil, &Error{Code: "release_notes_confirmation_required", Message: "请确认更新说明后再创建版本 Tag"}
		}
		notes, notesErr := normalizeReleaseNotes(req.ReleaseNotes)
		if notesErr != nil {
			return nil, notesErr
		}
		plan.ReleaseNotes = notes
		plan.ReleaseNotesConfirmed = true
		if pushRemote && plan.Automation != nil && plan.Automation.Trigger == releaseconfig.AutomationTriggerTag &&
			!strings.EqualFold(strings.TrimSpace(plan.Automation.ReleaseBranch), pf.Branch) {
			return nil, &Error{Code: "release_branch_mismatch", Message: "自动发布必须在分支 " + plan.Automation.ReleaseBranch + " 上执行"}
		}
	}
	if !pushRemote && plan.requiresGitPush() {
		return nil, &Error{Code: "remote_push_required", Message: "所选云端构建需要上传到远程仓库，请开启“提交后上传”"}
	}
	if err := plan.validateVersionScope(createTag); err != nil {
		return nil, err
	}
	requestedVersions := map[string]string{}
	for _, requested := range req.Versions {
		if requested.VersionGroupID == "" {
			return nil, &Error{Code: "invalid_version_group", Message: "版本组不能为空"}
		}
		if _, exists := requestedVersions[requested.VersionGroupID]; exists {
			return nil, &Error{Code: "invalid_version_group", Message: "版本组不能重复：" + requested.VersionGroupID}
		}
		requestedVersions[requested.VersionGroupID] = requested.TargetVersion
	}
	if len(plan.Targets) == 0 && len(plan.VersionGroups) == 1 {
		// The simple source-only UI calls this version "repository". Map it to
		// the sole configured group so custom version files remain authoritative.
		if repositoryVersion, ok := requestedVersions["repository"]; ok {
			if configuredVersion, exists := requestedVersions[plan.VersionGroups[0].ID]; exists && configuredVersion != repositoryVersion {
				return nil, &Error{Code: "invalid_version_group", Message: "仅提交代码时不能为同一版本组指定两个不同版本"}
			} else if !exists {
				requestedVersions[plan.VersionGroups[0].ID] = repositoryVersion
			}
			delete(requestedVersions, "repository")
		}
	}
	releaseVersions := []store.ReleaseVersion{}
	if createTag && plan.usesConfiguredVersionGroups() {
		selectedGroupIDs := map[string]bool{}
		for _, group := range plan.VersionGroups {
			selectedGroupIDs[group.ID] = true
			references, readErr := group.versionCandidates(pf.RepoRoot)
			if readErr != nil {
				return nil, &Error{Code: "version_file_invalid", Message: readErr.Error()}
			}
			prefix := strings.TrimSpace(group.TagPrefix)
			if prefix == "" {
				prefix = group.ID
			}
			version := requestedVersions[group.ID]
			if req.VersionMode == "auto" {
				if plan.NamespacedTags {
					version = pf.SuggestedVersions[group.ID]
					if version == "" {
						_, latestVersion := latestTagForPrefix([]string{pf.LatestGroupTags[group.ID]}, prefix)
						version = nextPatch(append(references, latestVersion)...)
					}
				} else {
					version = suggestReleaseVersion(references, pf.LatestTag)
				}
			} else if version == "" && len(plan.VersionGroups) == 1 {
				version = req.TargetVersion
			}
			if plan.NamespacedTags {
				_, latestVersion := latestTagForPrefix([]string{pf.LatestGroupTags[group.ID]}, prefix)
				if err := validateNewVersion(version, append(references, latestVersion)); err != nil {
					return nil, err
				}
			} else if err := validateReleaseVersion(version, references, pf.LatestTag); err != nil {
				return nil, err
			}
			tag := "v" + version
			if plan.NamespacedTags {
				prefix := strings.TrimSpace(group.TagPrefix)
				if prefix == "" {
					prefix = group.ID
				}
				tag = prefix + "/v" + version
			}
			name := group.Name
			if name == "" {
				name = group.ID
			}
			releaseVersions = append(releaseVersions, store.ReleaseVersion{VersionGroupID: group.ID, VersionGroupName: name, TargetVersion: version, TagName: tag})
		}
		for groupID := range requestedVersions {
			if !selectedGroupIDs[groupID] {
				return nil, &Error{Code: "invalid_version_group", Message: "版本组未被本次发布选中：" + groupID}
			}
		}
	} else if createTag {
		for groupID := range requestedVersions {
			if groupID != "repository" {
				return nil, &Error{Code: "invalid_version_group", Message: "仅提交代码时只能设置项目版本，不能设置版本组：" + groupID}
			}
		}
		versionReferences := []string{}
		for _, file := range pf.VersionFiles {
			versionReferences = append(versionReferences, pf.CurrentVersions[file])
		}
		version := req.TargetVersion
		if requested, ok := requestedVersions["repository"]; ok {
			version = requested
		}
		if req.VersionMode == "auto" {
			version = suggestReleaseVersion(versionReferences, pf.LatestTag)
		}
		if err := validateReleaseVersion(version, versionReferences, pf.LatestTag); err != nil {
			return nil, err
		}
		releaseVersions = append(releaseVersions, store.ReleaseVersion{VersionGroupID: "repository", VersionGroupName: "项目版本", TargetVersion: version, TagName: "v" + version})
	}
	plan.ReleaseVersions = releaseVersions
	if createTag {
		checkCtx, cancel := commandContext(ctx, 30*time.Second)
		defer cancel()
		for _, version := range releaseVersions {
			if _, err := s.git(checkCtx, pf.RepoRoot, "rev-parse", "--verify", "refs/tags/"+version.TagName); err == nil {
				return nil, &Error{Code: "tag_exists", Message: "本地已存在 tag：" + version.TagName}
			}
			remoteTag, err := s.git(checkCtx, pf.RepoRoot, "ls-remote", "--tags", pf.RemoteName, "refs/tags/"+version.TagName)
			if err != nil {
				return nil, &Error{Code: "remote_check_failed", Message: "无法检查远程 tag，请检查网络和 Git 凭据"}
			}
			if strings.TrimSpace(remoteTag) != "" {
				return nil, &Error{Code: "tag_exists", Message: "远程已存在 tag：" + version.TagName}
			}
		}
	}
	targetVersion, tag := "", ""
	if len(releaseVersions) > 0 {
		targetVersion, tag = releaseVersions[0].TargetVersion, releaseVersions[0].TagName
	}
	if !s.reserve(pf.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库已有发布任务正在执行"}
	}
	message := strings.TrimSpace(req.CommitMessage)
	if message == "" {
		if createTag {
			message = "chore(release): " + strings.Join(releaseTagNames(releaseVersions), ", ")
		} else {
			message = "chore: publish updates"
		}
	}
	planJSON, err := plan.marshal()
	if err != nil {
		s.release(pf.RepoRoot)
		return nil, &Error{Code: "execution_plan_invalid", Message: "无法冻结发布执行计划"}
	}
	run := &store.ReleaseRun{ID: app.NewRunID(), AppID: appID, RepoRoot: pf.RepoRoot, Branch: pf.Branch,
		RemoteName: pf.RemoteName, TargetVersion: targetVersion, TagName: tag, CreateTag: createTag, PushRemote: pushRemote, Versions: releaseVersions,
		SelectedTargets: selectedTargets, ExecutionPlan: planJSON, Status: "queued",
		Stage: "preparing", StatusFingerprint: pf.StatusFingerprint, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := s.store.CreateReleaseRun(run); err != nil {
		s.release(pf.RepoRoot)
		return nil, err
	}
	if err := s.store.CreateReleaseTargetRuns(run.ID, selectedTargets); err != nil {
		s.release(pf.RepoRoot)
		_ = s.store.UpdateReleaseRun(run.ID, "failed", "preparing", "", "target_state_failed", err.Error(), true)
		return nil, err
	}
	profile := pf.Profile
	go s.execute(run, pf, selected, message, profile.PreReleaseCommand)
	return run, nil
}

func (s *Service) execute(run *store.ReleaseRun, pf *Preflight, selected []string, message, checkCommand string) {
	defer s.release(run.RepoRoot)
	ctx := context.Background()
	releaseStarted := time.Now()
	currentStage := ""
	stageStarted := time.Time{}
	committed := false
	stagedByTool := false
	originals := map[string][]byte{}
	expectedVersionWrites := map[string][]byte{}
	plan, planErr := parseExecutionPlan(run.ExecutionPlan)
	configuredVersionFiles := []releaseconfig.VersionFile{}
	versionFiles := []string{}
	if run.CreateTag {
		if planErr == nil && plan.usesConfiguredVersionGroups() {
			configuredVersionFiles = plan.versionFiles()
			for _, file := range configuredVersionFiles {
				versionFiles = append(versionFiles, file.Path)
			}
		} else {
			versionFiles = pf.VersionFiles
		}
	}
	stagePaths := dedupe(append(append([]string{}, selected...), versionFiles...))
	finishStage := func(status, severity, code, msg string) {
		if currentStage == "" {
			return
		}
		kind := "performance"
		if status == "failed" {
			kind = "error"
		}
		s.recordRelease(run, diagnostics.Event{
			Kind: kind, Severity: severity, Source: "release", Operation: "release.stage",
			Stage: currentStage, Status: status, DurationMS: time.Since(stageStarted).Milliseconds(),
			ErrorCode: code, Message: msg,
		})
		currentStage = ""
	}
	fail := func(stage, code, msg string) {
		if currentStage == "" {
			stageStarted = releaseStarted
		}
		currentStage = stage
		finishStage("failed", "error", code, msg)
		if !committed {
			if stagedByTool && len(stagePaths) > 0 {
				s.unstageExactPaths(ctx, run.RepoRoot, stagePaths)
			}
			for _, path := range restoreFilesSafely(originals, expectedVersionWrites) {
				s.log(run.ID, "stderr", "版本文件在发布期间又被修改，已保留现场且未自动覆盖："+path)
			}
		}
		s.log(run.ID, "error", msg)
		_ = s.store.UpdateReleaseRun(run.ID, "failed", stage, run.CommitSHA, code, msg, true)
	}
	setStage := func(stage string) {
		finishStage("succeeded", "info", "", "发布阶段完成")
		currentStage = stage
		stageStarted = time.Now()
		_ = s.store.UpdateReleaseRun(run.ID, "running", stage, run.CommitSHA, "", "", false)
		s.log(run.ID, "event", stageText(stage))
		s.recordRelease(run, diagnostics.Event{
			Kind: "release", Severity: "info", Source: "release", Operation: "release.stage",
			Stage: stage, Status: "started", Message: stageText(stage),
		})
	}

	setStage("preparing")
	if planErr != nil {
		fail("preparing", "execution_plan_invalid", "冻结的发布执行计划无法读取")
		return
	}
	if paths, diagnosticsErr := s.untrackedDiagnosticsPaths(ctx, run.RepoRoot, stagePaths); diagnosticsErr != nil {
		fail("preparing", "git_status_failed", "无法确认诊断目录中的提交文件是否已被 Git 跟踪")
		return
	} else if len(paths) > 0 {
		fail("preparing", "diagnostics_path_untracked", "诊断目录中的未跟踪文件不能加入发布提交："+strings.Join(paths, "、"))
		return
	}
	currentHead, headErr := s.git(ctx, run.RepoRoot, "rev-parse", "HEAD")
	currentStatus, statusErr := s.gitRaw(ctx, run.RepoRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if headErr != nil || statusErr != nil || statusFingerprint(run.RepoRoot, currentHead, currentStatus, parseChanges(currentStatus)) != pf.StatusFingerprint {
		fail("preparing", "status_changed", "仓库内容已变化，请重新检查后再发布")
		return
	}
	if run.CreateTag {
		setStage("versioning")
		var err error
		if plan.usesConfiguredVersionGroups() {
			for _, group := range plan.VersionGroups {
				version, ok := plan.releaseVersionForGroup(group.ID)
				if !ok {
					err = fmt.Errorf("版本组 %s 缺少本次版本", group.ID)
					break
				}
				groupOriginals, updateErr := updateConfiguredVersionFiles(run.RepoRoot, group.VersionFiles, version.TargetVersion)
				for path, content := range groupOriginals {
					originals[path] = content
				}
				for path, content := range expectedConfiguredVersionWrites(run.RepoRoot, group.VersionFiles, groupOriginals, version.TargetVersion) {
					expectedVersionWrites[path] = content
				}
				if updateErr != nil {
					err = updateErr
					break
				}
			}
		} else {
			originals, err = updateVersionFiles(run.RepoRoot, versionFiles, run.TargetVersion)
			expectedVersionWrites = expectedLegacyVersionWrites(run.RepoRoot, versionFiles, originals, run.TargetVersion)
		}
		if err != nil {
			fail("versioning", "version_update_failed", err.Error())
			return
		}
	} else {
		s.log(run.ID, "event", "本次未创建版本 Tag，跳过版本文件更新")
	}

	setStage("checking")
	checkBaseline, baselineErr := s.worktreeSnapshot(ctx, run.RepoRoot)
	if baselineErr != nil {
		fail("checking", "git_status_failed", "无法记录发布前检查的仓库状态")
		return
	}
	if strings.TrimSpace(checkCommand) != "" {
		checkCtx, cancel := commandContext(ctx, 10*time.Minute)
		out, checkErr := runCheckCommand(checkCtx, s.runner, run.RepoRoot, checkCommand)
		cancel()
		if strings.TrimSpace(out) != "" {
			s.log(run.ID, "stdout", redact(out))
		}
		if checkErr != nil {
			fail("checking", "checks_failed", "发布前检查命令失败")
			return
		}
		afterCheck, statusErr := s.worktreeSnapshot(ctx, run.RepoRoot)
		if statusErr != nil {
			fail("checking", "git_status_failed", "无法检查发布前命令执行后的仓库状态")
			return
		}
		if ok, changed := verifyBuildSideEffects(checkBaseline, afterCheck, nil); !ok {
			fail("checking", "check_changed_tree", "发布前检查命令修改了仓库内容："+strings.Join(changed, "、"))
			return
		}
	}
	if len(plan.Targets) > 0 {
		if err := s.executeTargetChecks(ctx, run, plan); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("target_check", "target_execution_failed", err.Error())
			}
			return
		}
	}
	checkedHead, checkedHeadErr := s.git(ctx, run.RepoRoot, "rev-parse", "HEAD")
	if checkedHeadErr != nil || checkedHead != pf.HeadSHA {
		fail("checking", "release_commit_changed", "发布前检查命令改变了 Git 提交，已停止发布")
		return
	}

	setStage("committing")
	if len(stagePaths) > 0 {
		args := append([]string{"add", "--"}, stagePaths...)
		// git add is not atomic: it can update the index for earlier pathspecs
		// before a later ignored or invalid path makes the command fail. Mark the
		// staging attempt before running it so fail() also cleans up that partial
		// index state.
		stagedByTool = true
		if out, err := s.git(ctx, run.RepoRoot, args...); err != nil {
			fail("committing", "git_add_failed", redact(out))
			return
		}
	}
	_, diffErr := s.git(ctx, run.RepoRoot, "diff", "--cached", "--quiet")
	if diffErr != nil {
		if out, err := s.git(ctx, run.RepoRoot, "commit", "-m", message); err != nil {
			fail("committing", "commit_failed", redact(out))
			return
		}
	}
	sha, err := s.git(ctx, run.RepoRoot, "rev-parse", "HEAD")
	if err != nil {
		fail("committing", "commit_failed", "无法读取发布提交")
		return
	}
	run.CommitSHA = sha
	committed = true
	_ = s.store.UpdateReleaseRun(run.ID, "running", "committing", sha, "", "", false)

	if len(plan.Targets) > 0 {
		setStage("building_targets")
		if err := s.executeTargetPhase(ctx, run, plan, false); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("building_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}

	if run.CreateTag {
		setStage("tagging")
		for _, version := range releaseVersionsForRun(run, plan) {
			if err := s.ensureFrozenTag(ctx, run, plan, version, false); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("tagging", tagErr.Code, tagErr.Message)
				} else {
					fail("tagging", "tag_failed", err.Error())
				}
				return
			}
		}
	}
	if run.PushRemote {
		setStage("pushing_branch")
		pushCtx, cancelPush := commandContext(ctx, releasePushTimeout)
		out, err := s.git(pushCtx, run.RepoRoot, "push", run.RemoteName, run.CommitSHA+":refs/heads/"+run.Branch)
		cancelPush()
		if err != nil {
			fail("pushing_branch", "push_branch_failed", redact(out))
			return
		}
		if run.CreateTag {
			setStage("pushing_tag")
			for _, version := range releaseVersionsForRun(run, plan) {
				if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
					if tagErr, ok := err.(*tagOperationError); ok {
						fail("pushing_tag", tagErr.Code, tagErr.Message)
					} else {
						fail("pushing_tag", "tag_failed", err.Error())
					}
					return
				}
				pushTagCtx, cancelTag := commandContext(ctx, releasePushTimeout)
				out, err = s.git(pushTagCtx, run.RepoRoot, "push", run.RemoteName, "refs/tags/"+version.TagName)
				cancelTag()
				if err != nil {
					fail("pushing_tag", "push_tag_failed", redact(out))
					return
				}
			}
		}
	} else {
		s.log(run.ID, "event", "已保存在本机，未上传远程仓库")
	}
	if len(plan.Targets) > 0 {
		setStage("publishing_targets")
		if err := s.executeTargetPhase(ctx, run, plan, true); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("publishing_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}
	if automationHandoffApplies(run, plan) {
		s.log(run.ID, "event", "已交给 GitHub "+automationAction(plan)+"："+strings.Join(releaseTagNames(releaseVersionsForRun(run, plan)), "、"))
	} else if run.CreateTag && run.PushRemote {
		s.log(run.ID, "event", "代码与 Tag 已推送："+strings.Join(releaseTagNames(releaseVersionsForRun(run, plan)), "、"))
	} else if !run.CreateTag && run.PushRemote {
		s.log(run.ID, "event", "代码提交与推送完成（未创建 Tag）")
	} else if run.CreateTag {
		s.log(run.ID, "event", "本地提交与 Tag 创建完成（未上传远程仓库）")
	} else {
		s.log(run.ID, "event", "本地代码提交完成（未上传远程仓库）")
	}
	_ = s.store.UpdateReleaseRun(run.ID, "succeeded", "completed", sha, "", "", true)
	finishStage("succeeded", "info", "", "发布阶段完成")
	s.recordRelease(run, diagnostics.Event{
		Kind: "release", Severity: "info", Source: "release", Operation: "release.run",
		Stage: "completed", Status: "succeeded", DurationMS: time.Since(releaseStarted).Milliseconds(),
		Message: "发布流程完成", Context: map[string]any{
			"commitSha": sha, "tagName": run.TagName, "targetVersion": run.TargetVersion,
			"createTag": run.CreateTag, "pushRemote": run.PushRemote,
		},
	})
}

func (s *Service) Retry(runID string, requests ...RetryRequest) (*store.ReleaseRun, error) {
	run, err := s.store.GetReleaseRun(runID)
	if err != nil || run == nil {
		return nil, &Error{Code: "release_not_found", Message: "发布记录不存在"}
	}
	if run.Status != "failed" || run.CommitSHA == "" || !retryableStage(run.Stage) || run.ErrorCode == "build_changed_tree" {
		return nil, &Error{Code: "release_not_retryable", Message: "该失败阶段不能直接重试，请重新执行发布预检"}
	}
	confirmed := len(requests) > 0 && requests[0].ExternalActionsConfirmed
	plan, planErr := parseExecutionPlan(run.ExecutionPlan)
	if planErr != nil || (strings.HasPrefix(run.Stage, "target_") && len(plan.Targets) == 0) {
		return nil, &Error{Code: "execution_plan_invalid", Message: "冻结的发布执行计划无法用于重试"}
	}
	if run.CreateTag && (preTargetRetryStage(run.Stage) || run.Stage == "tagging" || run.Stage == "pushing_branch" || run.Stage == "pushing_tag") {
		if err := validateFrozenTagPlan(plan); err != nil {
			return nil, err
		}
	}
	if (run.PushRemote || requiresExternalActionsConfirmation(run.SelectedTargets)) && !confirmed {
		return nil, &Error{Code: "external_actions_confirmation_required", Message: "重试会再次操作远程仓库或发布目标，请确认远端状态后继续"}
	}
	commitCtx, cancelCommit := commandContext(context.Background(), 10*time.Second)
	commitErr := s.ensureFrozenCommit(commitCtx, run)
	cancelCommit()
	if commitErr != nil {
		return nil, commitErr
	}
	if !s.reserve(run.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库已有发布任务正在执行"}
	}
	_ = s.store.UpdateReleaseRun(run.ID, "queued", run.Stage, run.CommitSHA, "", "", false)
	run.Status = "queued"
	run.ErrorCode = ""
	run.ErrorMessage = ""
	run.FinishedAt = nil
	go s.retry(run)
	return run, nil
}

func (s *Service) retry(run *store.ReleaseRun) {
	defer s.release(run.RepoRoot)
	ctx := context.Background()
	retryStarted := time.Now()
	currentStage := ""
	stageStarted := time.Time{}
	originalStage := run.Stage
	plan, planErr := parseExecutionPlan(run.ExecutionPlan)
	finishStage := func(status, severity, code, msg string) {
		if currentStage == "" {
			return
		}
		kind := "performance"
		if status == "failed" {
			kind = "error"
		}
		s.recordRelease(run, diagnostics.Event{
			Kind: kind, Severity: severity, Source: "release", Operation: "release.retry_stage",
			Stage: currentStage, Status: status, DurationMS: time.Since(stageStarted).Milliseconds(),
			ErrorCode: code, Message: msg,
		})
		currentStage = ""
	}
	fail := func(stage, code, msg string) {
		if currentStage == "" {
			stageStarted = retryStarted
		}
		currentStage = stage
		finishStage("failed", "error", code, msg)
		s.log(run.ID, "error", msg)
		_ = s.store.UpdateReleaseRun(run.ID, "failed", stage, run.CommitSHA, code, msg, true)
	}
	set := func(stage string) {
		finishStage("succeeded", "info", "", "发布重试阶段完成")
		currentStage = stage
		stageStarted = time.Now()
		_ = s.store.UpdateReleaseRun(run.ID, "running", stage, run.CommitSHA, "", "", false)
		s.log(run.ID, "event", "重试："+stageText(stage))
		s.recordRelease(run, diagnostics.Event{
			Kind: "release", Severity: "info", Source: "release", Operation: "release.retry_stage",
			Stage: stage, Status: "started", Message: "重试：" + stageText(stage),
		})
	}
	if planErr != nil {
		fail("preparing", "execution_plan_invalid", "冻结的发布执行计划无法读取")
		return
	}
	if err := s.ensureFrozenCommit(ctx, run); err != nil {
		if commitErr, ok := err.(*Error); ok {
			fail(originalStage, commitErr.Code, commitErr.Message)
		} else {
			fail(originalStage, "release_commit_changed", err.Error())
		}
		return
	}
	preTargets := preTargetRetryStage(originalStage)
	if preTargets {
		set("building_targets")
		if err := s.executeTargetPhase(ctx, run, plan, false); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("building_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}
	if run.CreateTag && (preTargets || originalStage == "tagging") {
		set("tagging")
		for _, version := range releaseVersionsForRun(run, plan) {
			if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("tagging", tagErr.Code, tagErr.Message)
				} else {
					fail("tagging", "tag_failed", err.Error())
				}
				return
			}
		}
	}
	if run.CreateTag && run.PushRemote && originalStage == "pushing_branch" {
		// Validate the frozen Tag before retrying any remote mutation. It is
		// validated again immediately before the eventual Tag push.
		for _, version := range releaseVersionsForRun(run, plan) {
			if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("pushing_tag", tagErr.Code, tagErr.Message)
				} else {
					fail("pushing_tag", "tag_failed", err.Error())
				}
				return
			}
		}
	}
	pushBranch := run.PushRemote && (preTargets || originalStage == "tagging" || originalStage == "pushing_branch")
	if pushBranch {
		set("pushing_branch")
		pushCtx, cancelPush := commandContext(ctx, releasePushTimeout)
		out, err := s.git(pushCtx, run.RepoRoot, "push", run.RemoteName, run.CommitSHA+":refs/heads/"+run.Branch)
		cancelPush()
		if err != nil {
			fail("pushing_branch", "push_branch_failed", redact(out))
			return
		}
	}
	pushTag := run.PushRemote && run.CreateTag && (pushBranch || originalStage == "pushing_tag")
	if pushTag {
		set("pushing_tag")
		for _, version := range releaseVersionsForRun(run, plan) {
			if err := s.ensureFrozenTag(ctx, run, plan, version, true); err != nil {
				if tagErr, ok := err.(*tagOperationError); ok {
					fail("pushing_tag", tagErr.Code, tagErr.Message)
				} else {
					fail("pushing_tag", "tag_failed", err.Error())
				}
				return
			}
			pushTagCtx, cancelTag := commandContext(ctx, releasePushTimeout)
			out, err := s.git(pushTagCtx, run.RepoRoot, "push", run.RemoteName, "refs/tags/"+version.TagName)
			cancelTag()
			if err != nil {
				fail("pushing_tag", "push_tag_failed", redact(out))
				return
			}
		}
	}
	if len(plan.Targets) > 0 && (preTargets || originalStage == "tagging" || pushBranch || pushTag || postTargetRetryStage(originalStage)) {
		set("publishing_targets")
		if err := s.executeTargetPhase(ctx, run, plan, true); err != nil {
			if targetErr, ok := err.(*targetExecutionError); ok {
				fail(targetErr.Stage, targetErr.Code, targetErr.Message)
			} else {
				fail("publishing_targets", "target_execution_failed", err.Error())
			}
			return
		}
	}
	if automationHandoffApplies(run, plan) {
		s.log(run.ID, "event", "已交给 GitHub "+automationAction(plan)+"："+strings.Join(releaseTagNames(releaseVersionsForRun(run, plan)), "、"))
	} else if run.CreateTag && run.PushRemote {
		s.log(run.ID, "event", "代码与 Tag 已推送："+strings.Join(releaseTagNames(releaseVersionsForRun(run, plan)), "、"))
	} else if !run.CreateTag && run.PushRemote {
		s.log(run.ID, "event", "代码提交与推送完成（未创建 Tag）")
	} else if run.CreateTag {
		s.log(run.ID, "event", "本地提交与 Tag 创建完成（未上传远程仓库）")
	} else {
		s.log(run.ID, "event", "本地代码提交完成（未上传远程仓库）")
	}
	_ = s.store.UpdateReleaseRun(run.ID, "succeeded", "completed", run.CommitSHA, "", "", true)
	finishStage("succeeded", "info", "", "发布重试阶段完成")
	s.recordRelease(run, diagnostics.Event{
		Kind: "release", Severity: "info", Source: "release", Operation: "release.retry",
		Stage: "completed", Status: "succeeded", DurationMS: time.Since(retryStarted).Milliseconds(),
		Message: "发布重试完成", Context: map[string]any{"commitSha": run.CommitSHA, "originalStage": originalStage},
	})
}

func retryableStage(stage string) bool {
	return preTargetRetryStage(stage) || postTargetRetryStage(stage) || stage == "tagging" || stage == "pushing_branch" || stage == "pushing_tag"
}

func preTargetRetryStage(stage string) bool {
	return stage == "building_targets" || stage == "target_check" || stage == "target_build" || stage == "target_package"
}

func postTargetRetryStage(stage string) bool {
	return stage == "publishing_targets" || stage == "target_publish" || stage == "target_deploy"
}

func (s *Service) reserve(repo string) bool {
	key := strings.ToLower(filepath.Clean(repo))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[key] {
		return false
	}
	s.active[key] = true
	return true
}

func (s *Service) release(repo string) {
	key := strings.ToLower(filepath.Clean(repo))
	s.mu.Lock()
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
	return &RunView{Run: run, Targets: targets, Artifacts: artifacts, Logs: logs, Automation: automationHandoffView(run, plan)}, nil
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
		"target_deploy": "正在部署", "completed": "发布完成",
	}[stage]
}
