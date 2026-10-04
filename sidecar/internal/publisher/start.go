package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"strings"
	"time"
)

func (s *Service) Start(ctx context.Context, appID string, req CreateRequest) (*store.ReleaseRun, error) {
	if previous, err := s.store.ReleaseForCandidate(appID, req.CandidateID); err != nil {
		return nil, err
	} else if previous != nil {
		return previous, nil
	}
	profile, err := s.store.GetReleaseProfile(appID)
	if err != nil {
		return nil, err
	}
	createTag := profile.CreateTag
	if req.CreateTag != nil {
		createTag = *req.CreateTag
	}
	pushRemote := true
	if req.PushRemote != nil {
		pushRemote = *req.PushRemote
	}
	intent := strings.TrimSpace(req.Intent)
	if intent != "" && intent != IntentFormal && intent != IntentSaveProgress {
		return nil, &Error{Code: "invalid_intent", Message: "发布意图必须是保存进度或正式发布"}
	}
	if intent == IntentSaveProgress {
		createTag = false
		req.VersionMode = VersionModeUnchanged
		req.BuildMode = BuildModeNone
		req.SelectedTargets = nil
		req.CreateTag = boolPtr(false)
		if req.PushRemote == nil {
			pushRemote = false
			req.PushRemote = boolPtr(false)
		}
	}
	checkConfig := createTag || len(req.SelectedTargets) > 0
	// Preparing a release is local-only. Normal (non-force) pushes enforce
	// branch and tag conflicts when uploading, without a blocking fetch/query.
	pf, err := s.preflight(ctx, appID, false, createTag, checkConfig)
	if err != nil {
		return nil, err
	}
	if pushRemote && !contains(pf.Remotes, pf.RemoteName) {
		return nil, &Error{Code: "remote_missing", Message: "尚未配置上传位置。可以选择“仅提交到本机”先保存代码"}
	}
	if !pf.CanRelease {
		issue := pf.BlockingIssues[0]
		return nil, &Error{Code: issue.Code, Message: issue.Message}
	}
	if req.StatusFingerprint == "" || req.StatusFingerprint != pf.StatusFingerprint {
		return nil, &Error{Code: "status_changed", Message: "仓库内容已变化，请重新检查后再发布"}
	}
	if s.repositoryBusy(pf.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库已有发布任务正在执行"}
	}
	if req.VersionMode == "" {
		req.VersionMode = pf.Profile.VersionMode
	}
	if req.VersionMode != "auto" && req.VersionMode != "manual" && req.VersionMode != VersionModeUnchanged {
		return nil, &Error{Code: "invalid_version_mode", Message: "版本方式必须是自动递增、手动设置或保持不变"}
	}
	selected, err := validateSelected(req.SelectedPaths, pf.Changes)
	if err != nil {
		return nil, err
	}
	selectedTargets, err := validateTargetSelections(req.SelectedTargets)
	if err != nil {
		return nil, err
	}
	if intent == "" {
		intent = IntentFormal
	}
	if intent == IntentFormal && strings.TrimSpace(req.CandidateID) == "" {
		return nil, &Error{Code: "check_required", Message: "正式发布必须先构建并验收候选版本，不能省略候选标识或在发布时自动执行检查"}
	}
	candidateView, candErr := s.ensureReleaseCandidate(ctx, appID, req, selected, intent)
	if candErr != nil {
		return nil, candErr
	}
	if len(candidateView.SensitiveFindings) > 0 {
		return nil, &Error{Code: "sensitive_content", Message: "候选版本含有高可信敏感内容，已脱敏阻断，不能提交或发布"}
	}
	if intent == IntentFormal && hasBlockingDependency(candidateView.DependencyFindings) {
		return nil, &Error{Code: "missing_dependency", Message: "候选版本缺少确定的配套文件，或引用了不能自动加入的本地/敏感内容"}
	}
	if intent == IntentFormal {
		if !candidateReleaseReady(candidateView, req.SkipChecks) {
			if unresolved := unresolvedReview(candidateView.Classifications, selectedSet(selected), req.ManualDecisions); len(unresolved) > 0 {
				return nil, &Error{Code: "review_required", Message: "存在需要确认的文件，正式发布前请记录纳入或排除决定"}
			}
			if candidateView.MutationDetected {
				return nil, &Error{Code: "check_changed_tree", Message: "检查命令修改了候选源内容，必须重新验证"}
			}
			if candidateView.Status == CheckFailed {
				return nil, &Error{Code: "checks_failed", Message: "必需检查失败，不能正式发布"}
			}
			if candidateView.Status == CheckUnverified || candidateView.Status == CheckCancelled || candidateView.Status == CheckPending {
				return nil, &Error{Code: "checks_unverified", Message: "候选版本尚未通过内容绑定检查，不能正式发布"}
			}
			return nil, &Error{Code: "candidate_not_accepted", Message: "正式发布必须使用已验收且内容未变化的候选版本"}
		}
	}
	if requiresExternalActionsConfirmation(selectedTargets) && !req.ExternalActionsConfirmed {
		return nil, &Error{Code: "external_actions_confirmation_required", Message: "上传或部署会影响外部环境，请明确确认后再继续"}
	}
	plan := &executionPlan{SchemaVersion: executionPlanSchemaVersion, ConfigPath: releaseconfig.ManifestPath,
		VersionGroups: []planVersionGroup{}, Targets: []planTarget{}}
	if checkConfig {
		plan, err = s.freezeExecutionPlan(ctx, appID, pf.RepoRoot, selectedTargets)
		if err != nil {
			return nil, err
		}
	}
	plan.RemoteURL = pf.RemoteURL
	plan.PushRemote = &pushRemote
	plan.CandidateID = candidateView.ID
	plan.CandidateFingerprint = candidateView.Fingerprint
	plan.CandidateTreeHash = candidateView.TreeHash
	plan.Intent = intent
	plan.SkipChecks = req.SkipChecks
	if plan.SkipChecks {
		for i := range plan.Targets {
			plan.Targets[i].Steps.Check = ""
		}
	}
	if err := validateBuildMode(req.BuildMode, plan, pushRemote); err != nil {
		return nil, err
	}
	plan.BuildMode = req.BuildMode
	if err := validateDeliveryPlan(plan, createTag, pushRemote); err != nil {
		return nil, err
	}
	if (plan.requiresTagPush() || plan.requiresDispatch()) && !createTag {
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
		if pushRemote && plan.Automation != nil && (plan.Automation.Trigger == releaseconfig.AutomationTriggerTag || plan.requiresDispatch()) &&
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
			expectedVersion := version
			if expectedVersion == "" && len(plan.VersionGroups) == 1 {
				expectedVersion = req.TargetVersion
			}
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
			if req.VersionMode == "auto" && expectedVersion != "" && expectedVersion != version {
				return nil, versionPlanChanged(pf)
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
		expectedVersion := version
		if req.VersionMode == "auto" {
			version = suggestReleaseVersion(versionReferences, pf.LatestTag)
			if expectedVersion != "" && expectedVersion != version {
				return nil, versionPlanChanged(pf)
			}
		}
		if err := validateReleaseVersion(version, versionReferences, pf.LatestTag); err != nil {
			return nil, err
		}
		releaseVersions = append(releaseVersions, store.ReleaseVersion{VersionGroupID: "repository", VersionGroupName: "项目版本", TargetVersion: version, TagName: "v" + version})
	}
	if !createTag && len(plan.Targets) > 0 {
		// No Tag/version write is requested, but commands and artifact patterns
		// still need the current version from the accepted build source.
		releaseVersions, err = s.freezeCurrentTargetVersions(plan)
		if err != nil {
			return nil, err
		}
	}
	plan.ReleaseVersions = releaseVersions
	if createTag {
		checkCtx, cancel := commandContext(ctx, 30*time.Second)
		defer cancel()
		for _, version := range releaseVersions {
			if _, err := s.git(checkCtx, pf.RepoRoot, "rev-parse", "--verify", "refs/tags/"+version.TagName); err == nil {
				return nil, &Error{Code: "tag_exists", Message: "本地已存在 tag：" + version.TagName}
			}
			if pushRemote && pf.remoteTags[version.TagName] != "" {
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
	profile = pf.Profile
	go s.execute(run, pf, selected, message, profile.PreReleaseCommand)
	return run, nil
}

func versionPlanChanged(pf *Preflight) *Error {
	return &Error{Code: "version_plan_changed", Message: "版本建议已更新，请确认新版本后重试", Preflight: pf}
}
