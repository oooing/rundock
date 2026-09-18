package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func normalizeCandidateRequest(req CandidateRequest, pf *Preflight) CandidateRequest {
	if req.Intent == "" {
		req.Intent = IntentFormal
	}
	if req.VersionMode == "" {
		req.VersionMode = pf.Profile.VersionMode
	}
	if req.CreateTag == nil {
		req.CreateTag = boolPtr(pf.Profile.CreateTag)
	}
	if req.PushRemote == nil {
		req.PushRemote = boolPtr(req.Intent != IntentSaveProgress)
	}
	if req.Intent == IntentSaveProgress {
		req.VersionMode = VersionModeUnchanged
		req.CreateTag = boolPtr(false)
		req.BuildMode = BuildModeNone
		req.SelectedTargets = nil
		req.TargetVersion = ""
		req.Versions = nil
	}
	if len(req.SelectedTargets) == 0 && req.BuildMode == "" {
		req.BuildMode = BuildModeNone
	}
	req.SelectedPaths = append([]string{}, req.SelectedPaths...)
	sort.Strings(req.SelectedPaths)
	return req
}

func candidateRequestFromCreate(req CreateRequest, intent string) CandidateRequest {
	return CandidateRequest{Intent: intent, SkipChecks: req.SkipChecks, StatusFingerprint: req.StatusFingerprint,
		SelectedPaths: req.SelectedPaths, ManualDecisions: req.ManualDecisions,
		SensitiveExceptions: req.SensitiveExceptions, TargetVersion: req.TargetVersion,
		Versions: req.Versions, VersionMode: req.VersionMode, CreateTag: req.CreateTag,
		PushRemote: req.PushRemote, BuildMode: req.BuildMode, SelectedTargets: req.SelectedTargets}
}

// Bind the original development state as well as the plan. A newly refreshed
// status fingerprint must never make an older checked candidate current again.
func candidateBinding(req CandidateRequest, pf *Preflight, cfg *releaseconfig.Config) string {
	req = normalizeCandidateRequest(req, pf)
	req.StatusFingerprint = pf.StatusFingerprint
	data, _ := json.Marshal(struct {
		Request                       CandidateRequest
		Branch, RemoteName, RemoteURL string
		LegacyCheck                   string
		Rules                         []releaseconfig.FileRule
		Checks                        []releaseconfig.CheckProfile
		Targets                       []releaseconfig.Target
		Groups                        []releaseconfig.VersionGroup
		Automation                    *releaseconfig.Automation
	}{req, pf.Branch, pf.RemoteName, pf.RemoteURL, pf.Profile.PreReleaseCommand, cfg.FileRules, cfg.CheckProfiles, cfg.Targets, cfg.VersionGroups, cfg.Automation})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) validateCandidateBinding(ctx context.Context, cand *releaseCandidate, req CandidateRequest) error {
	pf, err := s.PreflightLocal(ctx, cand.AppID)
	if err != nil {
		return err
	}
	cfg, err := s.releaseConfig.Get(ctx, cand.AppID)
	if err != nil {
		return err
	}
	if pf.HeadSHA != cand.HeadSHA || candidateBinding(req, pf, cfg) != cand.Binding {
		return &Error{Code: "candidate_stale", Message: "文件内容、选择、版本或发布配置已变化，请重新检查候选版本"}
	}
	return validateCandidateBytes(cand)
}

func candidateFileDigests(root string) (map[string]string, error) {
	files, err := listCandidateFiles(root)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	for _, rel := range files {
		name, err := secureProjectPath(root, rel, false)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || isPathLink(info) {
			return nil, &Error{Code: "candidate_stale", Message: "候选中出现不支持的特殊文件"}
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(append([]byte(info.Mode().String()+"\x00"), raw...))
		result[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
	}
	return result, nil
}

func validateCandidateBytes(cand *releaseCandidate) error {
	for rel, want := range cand.FileDigests {
		path, err := secureProjectPath(cand.Work, rel, false)
		if err != nil {
			return &Error{Code: "candidate_stale", Message: "候选源码已消失或路径无效"}
		}
		info, err := os.Lstat(path)
		if err != nil || isPathLink(info) || !info.Mode().IsRegular() {
			return &Error{Code: "candidate_stale", Message: "候选源码类型已变化"}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(append([]byte(info.Mode().String()+"\x00"), raw...))
		if hex.EncodeToString(sum[:]) != want {
			return &Error{Code: "candidate_stale", Message: "候选源码已变化，必须重新创建候选"}
		}
	}

	// New build/test outputs are not in the immutable index and cannot be committed.
	// Changes to existing candidate files always invalidate the check, including locks.
	tree, err := isolatedGit(context.Background(), cand, "write-tree")
	if err != nil || strings.TrimSpace(tree) != cand.TreeHash {
		return &Error{Code: "candidate_stale", Message: "候选索引已变化，请重新检查"}
	}
	return nil
}
