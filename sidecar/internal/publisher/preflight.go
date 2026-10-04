package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) Preflight(ctx context.Context, appID string) (*Preflight, error) {
	return s.preflight(ctx, appID, true, true, true)
}

// PreflightLocal performs the fast, local-only portion used to render the
// release panel. Start checks only the resources required by the chosen actions.
func (s *Service) PreflightLocal(ctx context.Context, appID string) (*Preflight, error) {
	return s.preflight(ctx, appID, false, true, true)
}

func (s *Service) preflight(ctx context.Context, appID string, checkRemote, checkTags, checkConfig bool) (*Preflight, error) {
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
	root = canonicalRepositoryPath(root)
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
		if checkRemote {
			pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "remote_missing", Message: "远程仓库不存在：" + pf.RemoteName + "；仅本地提交可关闭“提交后上传”"})
		}
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
	if !checkRemote {
		s.readCachedRemoteChanges(ctx15, pf)
	}
	if !checkConfig {
		// A plain commit does not read, update or execute release configuration.
		if checkRemote && len(pf.BlockingIssues) == 0 {
			s.checkRemote(ctx, pf, false)
		}
		pf.Classifications = classifyChanges(root, pf.Changes, nil, nil)
		pf.CanRelease = len(pf.BlockingIssues) == 0
		return pf, nil
	}

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
		inactive := disabledOnlyVersionGroups(cfg)
		for _, group := range cfg.VersionGroups {
			if inactive[group.ID] {
				continue
			}
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
	localTags := ""
	if checkTags {
		localTags, _ = s.git(ctx15, root, "tag", "--list")
	}
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

	if checkRemote && len(pf.BlockingIssues) == 0 {
		s.checkRemote(ctx, pf, checkTags)
		for tag := range pf.remoteTags {
			tagNames = append(tagNames, tag)
		}
		refreshSuggestions()
	}
	if checkTags {
		s.compareReleaseContent(ctx, pf, pf.remoteTags)
	}
	rules := []releaseconfig.FileRule{}
	if cfg, configErr := s.releaseConfig.Get(ctx, appID); configErr == nil && cfg != nil {
		rules = cfg.FileRules
	}
	pf.Classifications = classifyChanges(root, pf.Changes, rules, nil)
	if pf.Classifications == nil {
		pf.Classifications = []FileClassification{}
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
