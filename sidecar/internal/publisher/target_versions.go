package publisher

import (
	"context"
	"fmt"
	"strings"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

// A version is also a build input, not only a request to create a Tag. Both
// standalone builds and untagged formal builds need the actual frozen bytes.
// Config CurrentVersion is used only for groups with no declared version files.
func currentGroupVersion(root string, group planVersionGroup) (string, error) {
	values, err := group.versionCandidates(root)
	if err != nil || len(values) == 0 {
		return "", fmt.Errorf("无法读取 %s 的当前版本，请检查已配置的版本文件", group.Name)
	}
	version := strings.TrimSpace(values[0])
	if version == "" {
		return "", fmt.Errorf("%s 的当前版本为空", group.Name)
	}
	for _, value := range values {
		if strings.TrimSpace(value) != version {
			return "", fmt.Errorf("%s 的版本文件不一致，请先统一当前版本", group.Name)
		}
	}
	return version, nil
}

// Untagged formal builds consume the accepted candidate, which can differ from
// unselected live worktree files. Freeze its current versions before any commit
// or build starts; never read a newer manifest/version during target execution.
func (s *Service) freezeCurrentTargetVersions(plan *executionPlan) ([]store.ReleaseVersion, error) {
	candidate := s.lookupCandidate(plan.CandidateID)
	if candidate == nil {
		return nil, &Error{Code: "candidate_not_found", Message: "已验收候选不存在，无法确定本次构建版本，请重新检查"}
	}
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if err := validateCandidateBytes(candidate); err != nil {
		return nil, &Error{Code: "candidate_stale", Message: "候选源码已变化，不能确定本次构建版本，请重新检查"}
	}
	selected := map[string]bool{}
	for _, target := range plan.Targets {
		selected[target.VersionGroup] = true
	}
	versions := []store.ReleaseVersion{}
	for _, group := range plan.VersionGroups {
		if !selected[group.ID] {
			continue
		}
		version, err := currentGroupVersion(candidate.Work, group)
		if err != nil {
			return nil, &Error{Code: "version_file_invalid", Message: err.Error()}
		}
		versions = append(versions, store.ReleaseVersion{VersionGroupID: group.ID, VersionGroupName: group.Name, TargetVersion: version})
		delete(selected, group.ID)
	}
	if len(selected) != 0 {
		return nil, &Error{Code: "version_group_missing", Message: "构建目标缺少当前版本配置"}
	}
	return versions, nil
}

// The caller holds candidate.mu and the repository execution lock. This helper
// never looks up or relocks the candidate. Fresh configuration can supply only
// version locations after its entire binding matches the frozen candidate; it
// cannot add/change commands. The actual values always come from candidate.Work.
func (s *Service) expandCandidateCheckVersions(ctx context.Context, candidate *releaseCandidate, profiles []releaseconfig.CheckProfile) ([]releaseconfig.CheckProfile, error) {
	needed := false
	for _, profile := range profiles {
		if strings.HasPrefix(profile.ID, "target:") && strings.Contains(profile.Command, "${VERSION}") {
			needed = true
			break
		}
	}
	if !needed {
		return profiles, nil
	}
	pf, err := s.PreflightLocal(ctx, candidate.AppID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.releaseConfig.Get(ctx, candidate.AppID)
	if err != nil {
		return nil, err
	}
	if pf.HeadSHA != candidate.HeadSHA || candidateBinding(candidate.Request, pf, cfg) != candidate.Binding || !samePath(cfg.RepoRoot, candidate.RepoRoot) {
		return nil, &Error{Code: "candidate_stale", Message: "版本配置或源内容已变化，请重新创建候选"}
	}
	groups := map[string]planVersionGroup{}
	for _, group := range cfg.VersionGroups {
		groups[group.ID] = planVersionGroup{ID: group.ID, Name: group.Name, CurrentVersion: group.CurrentVersion, VersionFiles: append([]releaseconfig.VersionFile{}, group.VersionFiles...)}
	}
	targets := map[string]releaseconfig.Target{}
	selected := map[string]bool{}
	for _, target := range cfg.Targets {
		targets[target.ID] = target
	}
	for _, target := range candidate.Request.SelectedTargets {
		selected[target.TargetID] = true
	}
	versions := map[string]string{}
	for i, profile := range profiles {
		if !strings.HasPrefix(profile.ID, "target:") || !strings.Contains(profile.Command, "${VERSION}") {
			continue
		}
		id := strings.TrimPrefix(profile.ID, "target:")
		target, exists := targets[id]
		group, groupExists := groups[target.VersionGroup]
		if !exists || !selected[id] || !groupExists || profile.Command != strings.ReplaceAll(target.Steps.Check, "${TARGET_ID}", id) {
			return nil, &Error{Code: "candidate_stale", Message: "已冻结的目标检查与版本配置不一致，请重新检查"}
		}
		version, exists := versions[group.ID]
		if !exists {
			version, err = currentGroupVersion(candidate.Work, group)
			if err != nil {
				return nil, &Error{Code: "version_file_invalid", Message: err.Error()}
			}
			versions[group.ID] = version
		}
		profiles[i].Command = strings.ReplaceAll(profile.Command, "${VERSION}", version)
	}
	return profiles, nil
}
