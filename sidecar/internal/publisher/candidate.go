package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

type releaseCandidate struct {
	FileModes        map[string]string
	ID               string
	AppID            string
	RepoRoot         string
	GitDir           string
	Dir              string
	Index            string
	Work             string
	Fingerprint      string
	Binding          string
	Request          CandidateRequest
	FileDigests      map[string]string
	SafetyReady      bool
	TreeHash         string
	HeadSHA          string
	Intent           string
	Selected         []string
	VersionFiles     map[string][]byte
	VersionOriginals map[string][]byte
	FrozenRules      []releaseconfig.FileRule
	FrozenProfiles   []releaseconfig.CheckProfile
	AutomaticChecks  []CheckResult
	TargetKinds      []string
	View             CandidateView
	cancel           context.CancelFunc
	mu               sync.Mutex
}

func (s *Service) PrepareCandidate(ctx context.Context, appID string, req CandidateRequest) (*CandidateView, error) {
	pf, err := s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, err
	}
	if !s.reserve(pf.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库已有发布或检查任务正在执行"}
	}
	defer s.release(pf.RepoRoot)
	if err := validateCandidateIndex(ctx, pf.RepoRoot); err != nil {
		return nil, err
	}
	if req.StatusFingerprint == "" || req.StatusFingerprint != pf.StatusFingerprint {
		return nil, &Error{Code: "status_changed", Message: "仓库内容已变化，请重新检查后再构建候选版本", Preflight: pf}
	}
	selected, err := validateSelected(req.SelectedPaths, pf.Changes)
	if err != nil {
		return nil, err
	}
	intent := strings.TrimSpace(req.Intent)
	if intent != IntentFormal && intent != IntentSaveProgress {
		return nil, &Error{Code: "invalid_intent", Message: "发布意图必须是保存进度或正式发布"}
	}
	req = normalizeCandidateRequest(req, pf)
	cfg, err := s.releaseConfig.Get(ctx, appID)
	if err != nil {
		return nil, &Error{Code: "release_config_invalid", Message: "无法读取发布配置：" + err.Error()}
	}
	rules := append([]releaseconfig.FileRule{}, cfg.FileRules...)
	profiles := candidateCheckProfiles(req, pf, cfg)
	kinds := frozenTargetKinds(cfg, req.SelectedTargets)
	classifications := classifyChanges(pf.RepoRoot, pf.Changes, rules, req.ManualDecisions)
	selectedSet := map[string]bool{}
	for _, path := range selected {
		selectedSet[path] = true
	}
	cand, err := s.buildIsolatedCandidate(ctx, appID, pf, selected, req, cfg)
	if err != nil {
		return nil, err
	}
	cand.AppID = appID
	cand.Intent = intent
	cand.FrozenRules = rules
	cand.FrozenProfiles = profiles
	cand.TargetKinds = kinds
	cand.Request = req
	cand.Binding = candidateBinding(req, pf, cfg)
	cand.FileDigests, err = candidateFileDigests(cand.Work)
	if err != nil {
		cand.cleanup()
		return nil, err
	}
	if err = s.validateCandidateBinding(ctx, cand, req); err != nil {
		cand.cleanup()
		return nil, err
	}

	scanPaths, err := listCandidateFiles(cand.Work)
	if err != nil {
		cand.cleanup()
		return nil, err
	}
	targets, err := readScanTargets(cand.Work, scanPaths)
	if err != nil {
		cand.cleanup()
		return nil, err
	}
	findings, err := scanSensitiveContent(targets)
	if err != nil {
		cand.cleanup()
		return nil, err
	}
	for _, path := range scanPaths {
		for _, rule := range rules {
			if rule.Kind == releaseconfig.RuleSensitive && matchFileRule(rule.Pattern, path) {
				findings = append(findings, SensitiveFinding{Path: path, Kind: "sensitive-rule", Reason: "命中项目敏感规则", Redacted: "[blocked]"})
				break
			}
		}
	}
	contentFP := map[string]string{}
	for _, item := range classifications {
		contentFP[item.Path] = item.ContentFingerprint
	}
	for _, file := range targets {
		sum := sha256.Sum256(file.Content)
		contentFP[file.Path] = hex.EncodeToString(sum[:])
	}
	findings = filterSensitiveExceptions(findings, req.SensitiveExceptions, contentFP)
	warnings := []string{}
	blockingFindings := make([]SensitiveFinding, 0, len(findings))
	for _, finding := range findings {
		if finding.Kind == "credential-literal-review" {
			warnings = append(warnings, fmt.Sprintf("%s:%d · %s", finding.Path, finding.Line, finding.Reason))
		} else {
			blockingFindings = append(blockingFindings, finding)
		}
	}
	findings = blockingFindings
	for i := range classifications {
		for _, finding := range findings {
			if finding.Path == classifications[i].Path {
				classifications[i].Category = CategorySensitive
				classifications[i].SelectedDefault = false
				classifications[i].SensitiveKind = finding.Kind
				classifications[i].Sources = appendUniqueSource(classifications[i].Sources, "scan")
				classifications[i].Reasons = append(classifications[i].Reasons, finding.Reason+"（已脱敏，不显示密钥原文）")
			}
		}
	}
	deps := findMissingLocalDependencies(cand.Work, pf.RepoRoot, selected, classifications)
	for _, item := range classifications {
		if item.BaselineKept && !selectedSet[item.Path] {
			warnings = append(warnings, item.Path+" 的本次改动未选入，但基线提交中的内容仍在候选版本中")
		}
	}
	reviewOpen := unresolvedReview(classifications, selectedSet, req.ManualDecisions)
	cand.AutomaticChecks = automaticCandidateChecks(findings, deps, len(reviewOpen))
	checkResults := append(append([]CheckResult{}, cand.AutomaticChecks...), plannedCheckResults(profiles, kinds)...)
	status := CheckPending
	if len(findings) > 0 || len(reviewOpen) > 0 || hasBlockingDependency(deps) {
		status = "blocked"
	}
	canSave := len(findings) == 0
	canFormal := canSave && len(reviewOpen) == 0 && !hasBlockingDependency(deps) && applicableChecksReady(checkResults)
	cand.SafetyReady = canSave && len(reviewOpen) == 0 && !hasBlockingDependency(deps)
	if len(checkResults) == 0 || !applicableChecksReady(checkResults) {
		canFormal = false
		if status != "blocked" {
			status = CheckUnverified
		}
	}
	view := CandidateView{
		ID: cand.ID, Fingerprint: cand.Fingerprint, Status: status, Intent: intent,
		Classifications: classifications, SelectedPaths: selected,
		SensitiveFindings: findings, DependencyFindings: deps, CheckResults: checkResults,
		Warnings: warnings, CanFormal: canFormal, CanSaveProgress: canSave, Accepted: false,
		TreeHash: cand.TreeHash,
	}
	if intent == IntentSaveProgress && canSave {
		view.Status = "ready"
	}
	cand.View = view
	if err := ctx.Err(); err != nil {
		cand.cleanup()
		return nil, err
	}
	s.storeCandidate(cand)
	return cloneView(view), nil
}

func (s *Service) GetCandidate(appID, id string) (*CandidateView, error) {
	cand := s.lookupCandidate(id)
	if cand == nil || cand.AppID != appID {
		return nil, &Error{Code: "candidate_not_found", Message: "候选版本不存在或已失效"}
	}
	cand.mu.Lock()
	defer cand.mu.Unlock()
	return cloneView(cand.View), nil
}

func (s *Service) CancelCandidate(appID, id string) (*CandidateView, error) {
	cand := s.lookupCandidate(id)
	if cand == nil || cand.AppID != appID {
		return nil, &Error{Code: "candidate_not_found", Message: "候选版本不存在或已失效"}
	}
	cand.mu.Lock()
	if cand.cancel != nil {
		cand.cancel()
	}
	cand.View.Status = CheckCancelled
	cand.View.Accepted = false
	cand.View.CanFormal = false
	cand.View.CanSaveProgress = false
	for i := range cand.View.CheckResults {
		if cand.View.CheckResults[i].Status == CheckRunning || cand.View.CheckResults[i].Status == CheckPending {
			cand.View.CheckResults[i].Status = CheckCancelled
			cand.View.CheckResults[i].Reason = "已取消"
		}
	}
	view := cloneView(cand.View)
	cand.mu.Unlock()
	return view, nil
}

func (s *Service) buildIsolatedCandidate(ctx context.Context, appID string, pf *Preflight, selected []string, req CandidateRequest, cfg *releaseconfig.Config) (*releaseCandidate, error) {
	dir, err := os.MkdirTemp("", "rundock-candidate-*")
	if err != nil {
		return nil, &Error{Code: "candidate_failed", Message: "无法创建隔离候选目录"}
	}
	work := filepath.Join(dir, "tree")
	index := filepath.Join(dir, "index")
	if err := os.MkdirAll(work, 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return nil, &Error{Code: "candidate_failed", Message: "无法创建隔离工作目录"}
	}
	gitDir, err := resolveGitDir(ctx, pf.RepoRoot)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	head, err := gitOutput(ctx, pf.RepoRoot, "rev-parse", "HEAD")
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, &Error{Code: "candidate_failed", Message: "无法读取当前 HEAD"}
	}
	head = strings.TrimSpace(head)
	if pf.HeadSHA != "" && head != pf.HeadSHA {
		_ = os.RemoveAll(dir)
		return nil, &Error{Code: "status_changed", Message: "HEAD 在预检后已变化，请重新检查"}
	}
	cand := &releaseCandidate{ID: newCandidateID(), AppID: appID, RepoRoot: pf.RepoRoot, GitDir: gitDir, Dir: dir, Index: index, Work: work, HeadSHA: head, Selected: append([]string{}, selected...), VersionFiles: map[string][]byte{}}
	if err := inspectUnsupportedTree(ctx, pf.RepoRoot); err != nil {
		cand.cleanup()
		return nil, err
	}
	if _, err := isolatedGit(ctx, cand, "read-tree", cand.HeadSHA); err != nil {
		cand.cleanup()
		return nil, &Error{Code: "candidate_failed", Message: "无法从当前 HEAD 读取隔离索引"}
	}
	if err := materializeCandidate(ctx, cand); err != nil {
		cand.cleanup()
		return nil, err
	}

	if err := failUnsupportedWorktree(cand.Work); err != nil {
		cand.cleanup()
		return nil, err
	}
	changeByPath := map[string]FileChange{}
	for _, change := range pf.Changes {
		changeByPath[filepath.ToSlash(change.Path)] = change
	}
	selectedSet := map[string]bool{}
	for _, path := range selected {
		selectedSet[path] = true
	}
	for _, path := range selected {
		change := changeByPath[path]
		if err := applySelectedChange(cand, pf.RepoRoot, change); err != nil {
			cand.cleanup()
			return nil, err
		}
	}
	if shouldWriteVersions(req) {
		if err := s.applyPlannedVersions(ctx, cand, pf, req, cfg); err != nil {
			cand.cleanup()
			return nil, err
		}
	}
	if err := failUnsupportedWorktree(cand.Work); err != nil {
		cand.cleanup()
		return nil, err
	}
	tree, err := isolatedGit(ctx, cand, "write-tree")
	if err != nil {
		cand.cleanup()
		return nil, &Error{Code: "candidate_failed", Message: "无法写入候选树"}
	}
	cand.TreeHash = strings.TrimSpace(tree)
	fp, err := candidateFingerprint(cand.HeadSHA, selected, req, cfg, cand)
	if err != nil {
		cand.cleanup()
		return nil, err
	}
	cand.Fingerprint = fp
	return cand, nil
}

func applySelectedChange(cand *releaseCandidate, repo string, change FileChange) error {
	path := filepath.ToSlash(change.Path)
	if path == "" {
		return &Error{Code: "invalid_path", Message: "文件路径无效"}
	}
	if _, err := secureCandidateRel(path); err != nil {
		return err
	}
	if strings.ContainsAny(change.Status, "D") && change.OldPath == "" {
		_ = os.Remove(filepath.Join(cand.Work, filepath.FromSlash(path)))
		if _, err := isolatedGit(context.Background(), cand, "update-index", "--force-remove", "--", path); err != nil {
			return &Error{Code: "candidate_failed", Message: "无法在候选版本中删除 " + path}
		}
		return nil
	}
	if change.OldPath != "" {
		if _, err := secureCandidateRel(change.OldPath); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(cand.Work, filepath.FromSlash(change.OldPath)))
		if _, err := isolatedGit(context.Background(), cand, "update-index", "--force-remove", "--", change.OldPath); err != nil {
			return &Error{Code: "candidate_failed", Message: "无法在候选版本中移除旧路径 " + change.OldPath}
		}
	}
	if err := copyWorktreeFile(repo, cand.Work, path); err != nil {
		return err
	}
	if err := stageCandidateBlob(cand, path); err != nil {
		return &Error{Code: "candidate_failed", Message: "无法将 " + path + " 加入候选索引"}
	}
	return nil
}

func copyWorktreeFile(repo, work, rel string) error {
	src, err := secureProjectPath(repo, rel, false)
	if err != nil {
		return &Error{Code: "invalid_path", Message: "无法安全读取选中文件：" + rel + "：" + err.Error()}
	}
	info, statErr := os.Lstat(src)
	if statErr != nil {
		return &Error{Code: "candidate_failed", Message: "无法读取选中文件：" + rel}
	}
	if isPathLink(info) {
		return &Error{Code: "symlink_unsupported", Message: "候选版本不支持符号链接：" + rel}
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return &Error{Code: "candidate_failed", Message: "无法读取选中文件：" + rel}
	}
	if isLFSPointer(raw) {
		return &Error{Code: "lfs_unsupported", Message: "候选版本暂不支持 Git LFS 文件：" + rel}
	}
	dest := filepath.Join(work, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return &Error{Code: "candidate_unsupported", Message: "候选不支持特殊文件"}
	}
	return os.WriteFile(dest, raw, info.Mode().Perm())
}

func (s *Service) applyPlannedVersions(ctx context.Context, cand *releaseCandidate, pf *Preflight, req CandidateRequest, cfg *releaseconfig.Config) error {
	groups := []planVersionGroup{}
	if cfg != nil && len(cfg.VersionGroups) > 0 {
		plan, err := s.freezeExecutionPlan(ctx, cand.AppID, pf.RepoRoot, req.SelectedTargets)
		if err != nil {
			return err
		}
		if plan.usesConfiguredVersionGroups() {
			groups = plan.VersionGroups
		}
	}
	if len(groups) == 0 {
		version := plannedVersion(req, pf, "", 1)
		files := legacyVersionFiles(pf.VersionFiles)
		if version == "" || len(files) == 0 {
			return nil
		}
		return writeCandidateVersionFiles(cand, files, version)
	}
	requested := map[string]string{}
	for _, item := range req.Versions {
		requested[item.VersionGroupID] = item.TargetVersion
	}
	if len(groups) == 1 && len(req.SelectedTargets) == 0 && requested[groups[0].ID] == "" {
		requested[groups[0].ID] = requested["repository"]
	}
	for _, group := range groups {
		version := strings.TrimSpace(requested[group.ID])
		if version == "" {
			version = plannedVersion(req, pf, group.ID, len(groups))
		}
		if version == "" {
			continue
		}
		if err := writeCandidateVersionFiles(cand, group.VersionFiles, version); err != nil {
			return err
		}
	}
	return nil
}

func plannedVersion(req CandidateRequest, pf *Preflight, groupID string, groupCount int) string {
	if groupID == "" {
		groupID = "repository"
	}
	if groupID != "" {
		for _, item := range req.Versions {
			if item.VersionGroupID == groupID && strings.TrimSpace(item.TargetVersion) != "" {
				return strings.TrimSpace(item.TargetVersion)
			}
		}
	}
	if v := strings.TrimSpace(req.TargetVersion); v != "" && (groupID == "" || groupCount == 1) {
		return v
	}
	if pf != nil && pf.SuggestedVersions[groupID] != "" {
		return pf.SuggestedVersions[groupID]
	}
	if pf != nil && strings.TrimSpace(pf.SuggestedVersion) != "" && (groupID == "" || groupCount == 1) {
		return strings.TrimSpace(pf.SuggestedVersion)
	}
	return ""
}

func writeCandidateVersionFiles(cand *releaseCandidate, files []releaseconfig.VersionFile, version string) error {
	if cand.VersionOriginals == nil {
		cand.VersionOriginals = map[string][]byte{}
	}
	originals := map[string][]byte{}
	for _, file := range files {
		abs, err := secureProjectPath(cand.Work, file.Path, false)
		if err != nil {
			return &Error{Code: "version_file_invalid", Message: "候选版本文件路径无效：" + file.Path}
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return &Error{Code: "version_file_invalid", Message: "候选版本中缺少版本文件：" + file.Path}
		}
		originals[abs] = raw
		cand.VersionOriginals[filepath.ToSlash(file.Path)] = append([]byte(nil), raw...)
	}
	for _, file := range files {
		abs := filepath.Join(cand.Work, filepath.FromSlash(file.Path))
		after, err := configuredVersionBytes(cand.Work, file, originals[abs], originals, version)
		if err != nil {
			return err
		}
		if err := os.WriteFile(abs, after, 0o644); err != nil {
			return err
		}
		if err := stageCandidateBlob(cand, file.Path); err != nil {
			return &Error{Code: "candidate_failed", Message: "无法将计划版本文件加入候选：" + file.Path}
		}
		cand.VersionFiles[filepath.ToSlash(file.Path)] = after
	}
	return nil
}

func shouldWriteVersions(req CandidateRequest) bool {
	if strings.EqualFold(strings.TrimSpace(req.Intent), IntentSaveProgress) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(req.VersionMode), VersionModeUnchanged) {
		return false
	}
	if req.CreateTag != nil && !*req.CreateTag {
		return false
	}
	return true
}

func inspectUnsupportedTree(ctx context.Context, repo string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "ls-tree", "-r", "-z", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	raw, err := cmd.Output()
	if err != nil {
		return &Error{Code: "candidate_failed", Message: "无法检查 HEAD 树中的子模块或符号链接"}
	}
	for _, rec := range bytes.Split(raw, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		line := string(rec)
		if strings.HasPrefix(line, "160000 ") {
			return &Error{Code: "submodule_unsupported", Message: "候选版本暂不支持 Git 子模块，已停止以免生成残缺副本"}
		}
		if strings.HasPrefix(line, "120000 ") {
			return &Error{Code: "symlink_unsupported", Message: "候选版本暂不支持符号链接，已停止以免生成残缺副本"}
		}
	}
	return nil
}

func failUnsupportedWorktree(work string) error {
	return filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(work, path)
		rel = filepath.ToSlash(rel)
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			return fs.SkipDir
		}
		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if isPathLink(info) {
			return &Error{Code: "symlink_unsupported", Message: "候选版本暂不支持符号链接：" + rel}
		}
		if d.IsDir() {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr == nil && isLFSPointer(raw) {
			return &Error{Code: "lfs_unsupported", Message: "候选版本暂不支持 Git LFS 文件：" + rel}
		}
		return nil
	})
}

func isLFSPointer(raw []byte) bool {
	return bytes.HasPrefix(raw, []byte("version https://git-lfs.github.com/spec/v1\n"))
}

func secureCandidateRel(rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, ":") {
		return "", &Error{Code: "invalid_path", Message: "路径无效"}
	}
	clean := filepath.ToSlash(pathClean(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", &Error{Code: "invalid_path", Message: "路径不能跳出候选目录：" + rel}
	}
	return clean, nil
}

func pathClean(rel string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
}

func resolveGitDir(ctx context.Context, repo string) (string, error) {
	out, err := gitOutput(ctx, repo, "rev-parse", "--absolute-git-dir")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", &Error{Code: "not_repository", Message: "无法解析 Git 目录；独立 worktree 的 .git 文件不受支持时请改用仓库根目录"}
	}
	return strings.TrimSpace(out), nil
}

func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	raw, err := cmd.Output()
	return strings.TrimSpace(string(raw)), err
}

func isolatedGit(ctx context.Context, cand *releaseCandidate, args ...string) (string, error) {
	gitDir := cand.GitDir
	if gitDir == "" {
		gitDir = filepath.Join(cand.RepoRoot, ".git")
	}
	all := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + filepath.Join(cand.Dir, "no-hooks"), "-c", "core.splitIndex=false", "--git-dir", gitDir, "--work-tree", cand.Work}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	cmd.Dir = cand.Work
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_INDEX_FILE="+cand.Index,
		"GIT_DIR="+gitDir,
		"GIT_WORK_TREE="+cand.Work,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimRight(stdout.String(), "\r\n")
	if err != nil {
		out += stderr.String()
	}
	return out, err
}

func listCandidateFiles(work string) ([]string, error) {
	out := []string{}
	err := filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(work, path)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if rel == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

func candidateFingerprint(head string, selected []string, req CandidateRequest, cfg *releaseconfig.Config, cand *releaseCandidate) (string, error) {
	parts := []string{head, cand.TreeHash, strings.Join(append([]string{}, selected...), "\x00")}
	if cfg != nil {
		raw, err := json.Marshal(struct {
			Rules    []releaseconfig.FileRule     `json:"rules"`
			Profiles []releaseconfig.CheckProfile `json:"profiles"`
			Targets  []releaseconfig.Target       `json:"targets"`
			Groups   []releaseconfig.VersionGroup `json:"groups"`
		}{cfg.FileRules, cfg.CheckProfiles, cfg.Targets, cfg.VersionGroups})
		if err != nil {
			return "", err
		}
		parts = append(parts, string(raw))
	}
	plan, err := json.Marshal(struct {
		Intent        string
		VersionMode   string
		BuildMode     string
		TargetVersion string
		CreateTag     *bool
		PushRemote    *bool
		Versions      []ReleaseVersionInput
		Targets       []storeReleaseTarget
	}{req.Intent, req.VersionMode, req.BuildMode, req.TargetVersion, req.CreateTag, req.PushRemote, req.Versions, toStoreTargets(req.SelectedTargets)})
	if err != nil {
		return "", err
	}
	parts = append(parts, string(plan))
	files, err := listCandidateFiles(cand.Work)
	if err != nil {
		return "", err
	}
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(cand.Work, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		parts = append(parts, rel, hex.EncodeToString(sum[:]))
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:]), nil
}

type storeReleaseTarget struct {
	TargetID string `json:"targetId"`
	Build    bool   `json:"build"`
	Package  bool   `json:"package"`
	Publish  bool   `json:"publish"`
	Deploy   bool   `json:"deploy"`
}

func toStoreTargets(items []store.ReleaseTargetSelection) []storeReleaseTarget {
	out := make([]storeReleaseTarget, 0, len(items))
	for _, item := range items {
		out = append(out, storeReleaseTarget{TargetID: item.TargetID, Build: item.Build, Package: item.Package, Publish: item.Publish, Deploy: item.Deploy})
	}
	return out
}

func newCandidateID() string {
	return fmt.Sprintf("cand-%d", time.Now().UnixNano())
}

func (c *releaseCandidate) cleanup() {
	if c == nil || c.Dir == "" {
		return
	}
	base := filepath.Base(c.Dir)
	if !strings.HasPrefix(base, "rundock-candidate-") {
		return
	}
	abs, err := filepath.Abs(c.Dir)
	temp, errTemp := filepath.Abs(os.TempDir())
	if err != nil || errTemp != nil || !strings.EqualFold(filepath.Dir(abs), temp) {
		return
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return
	}
	if isPathLink(info) {
		_ = os.Remove(abs)
		return
	}
	_ = os.RemoveAll(c.Dir)
}

func (s *Service) storeCandidate(cand *releaseCandidate) {
	s.candidatesMu.Lock()
	defer s.candidatesMu.Unlock()
	if s.candidates == nil {
		s.candidates = map[string]*releaseCandidate{}
	}
	replaced := []string{}
	for id, existing := range s.candidates {
		if existing.AppID != cand.AppID || existing.ID == cand.ID {
			continue
		}
		existing.mu.Lock()
		running := existing.View.Status == CheckRunning
		existing.mu.Unlock()
		if running {
			continue
		}
		existing.cleanup()
		replaced = append(replaced, id)
	}
	for _, id := range replaced {
		delete(s.candidates, id)
	}
	s.candidates[cand.ID] = cand
}

func (s *Service) lookupCandidate(id string) *releaseCandidate {
	s.candidatesMu.Lock()
	defer s.candidatesMu.Unlock()
	if s.candidates == nil {
		return nil
	}
	return s.candidates[id]
}

func hasBlockingDependency(findings []DependencyFinding) bool {
	for _, finding := range findings {
		if finding.Blocked {
			return true
		}
	}
	return false
}

func applicableChecksReady(results []CheckResult) bool {
	applicable := 0
	for _, result := range results {
		if result.Status == CheckSkipped {
			continue
		}
		applicable++
		if result.Required && result.Status != CheckPassed && result.Status != CheckPending {
			return false
		}
	}
	return applicable > 0
}

func frozenTargetKinds(cfg *releaseconfig.Config, selections []store.ReleaseTargetSelection) []string {
	if cfg == nil {
		return nil
	}
	byID := map[string]releaseconfig.Target{}
	for _, target := range cfg.Targets {
		byID[target.ID] = target
	}
	out := []string{}
	seen := map[string]bool{}
	for _, selection := range selections {
		target, ok := byID[selection.TargetID]
		if !ok {
			continue
		}
		kind := strings.TrimSpace(target.Kind)
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}

func plannedCheckResults(profiles []releaseconfig.CheckProfile, targetKinds []string) []CheckResult {
	kindSet := map[string]bool{}
	for _, kind := range targetKinds {
		kindSet[strings.ToLower(strings.TrimSpace(kind))] = true
	}
	out := []CheckResult{}
	for _, profile := range profiles {
		if len(profile.TargetKinds) > 0 {
			ok := false
			for _, kind := range profile.TargetKinds {
				if kindSet[strings.ToLower(strings.TrimSpace(kind))] {
					ok = true
					break
				}
			}
			if !ok {
				out = append(out, CheckResult{ID: profile.ID, Name: profile.Name, Status: CheckSkipped, Reason: "目标限定检查不适用于本次范围", Required: profile.Required})
				continue
			}
		}
		if len(profile.OS) > 0 && !osAllowed(profile.OS) {
			status := CheckSkipped
			reason := "当前系统不是该检查声明的运行环境"
			if profile.Required {
				status = CheckUnverified
				reason = "当前系统无法运行必需检查，正式发布已阻断"
			}
			out = append(out, CheckResult{ID: profile.ID, Name: profile.Name, Status: status, Reason: reason, Required: profile.Required})
			continue
		}
		out = append(out, CheckResult{ID: profile.ID, Name: profile.Name, Status: CheckPending, Required: profile.Required})
	}
	return out
}

func (s *Service) ensureReleaseCandidate(ctx context.Context, appID string, req CreateRequest, selected []string, intent string) (*CandidateView, error) {
	if id := strings.TrimSpace(req.CandidateID); id != "" {
		cand := s.lookupCandidate(id)
		if cand == nil || cand.AppID != appID {
			return nil, &Error{Code: "candidate_not_found", Message: "候选版本不存在或已失效"}
		}
		if cand.Intent != intent {
			return nil, &Error{Code: "candidate_stale", Message: "本次目的已改变，请重新创建候选版本"}
		}
		if err := s.validateCandidateBinding(ctx, cand, candidateRequestFromCreate(req, intent)); err != nil {
			return nil, err
		}
	}
	if intent == IntentFormal {
		id := strings.TrimSpace(req.CandidateID)
		if id == "" {
			return nil, &Error{Code: "check_required", Message: "正式发布必须先构建并验收候选版本，不能省略候选标识或在发布时自动执行检查"}
		}
		cand := s.lookupCandidate(id)
		if cand == nil || cand.AppID != appID {
			return nil, &Error{Code: "candidate_not_found", Message: "正式发布必须使用已验收的候选版本"}
		}
		cand.mu.Lock()
		view := cloneView(cand.View)
		tree := cand.TreeHash
		fp := cand.Fingerprint
		head := cand.HeadSHA
		cand.mu.Unlock()
		if !view.Accepted || view.Status != CheckPassed {
			return nil, &Error{Code: "candidate_not_accepted", Message: "正式发布必须使用已检查通过的验收候选"}
		}
		if fp == "" || tree == "" {
			return nil, &Error{Code: "candidate_not_accepted", Message: "候选版本指纹缺失"}
		}
		currentHead, err := gitOutput(ctx, cand.RepoRoot, "rev-parse", "HEAD")
		if err != nil || strings.TrimSpace(currentHead) != head {
			return nil, &Error{Code: "candidate_stale", Message: "基线 HEAD 已变化，请重新构建候选版本"}
		}
		liveTree, err := isolatedGit(ctx, cand, "write-tree")
		if err != nil || strings.TrimSpace(liveTree) != tree {
			return nil, &Error{Code: "candidate_stale", Message: "候选树与验收内容不一致，请重新检查"}
		}
		return view, nil
	}
	if id := strings.TrimSpace(req.CandidateID); id != "" {
		if cand := s.lookupCandidate(id); cand != nil && cand.AppID == appID {
			cand.mu.Lock()
			defer cand.mu.Unlock()
			if cand.View.Status != "ready" || !cand.View.CanSaveProgress {
				return nil, &Error{Code: "candidate_not_accepted", Message: "候选尚未就绪或已取消，请重新检查"}
			}
			return cloneView(cand.View), nil
		}
	}
	return s.PrepareCandidate(ctx, appID, CandidateRequest{
		StatusFingerprint: req.StatusFingerprint, SelectedPaths: selected, ManualDecisions: req.ManualDecisions,
		Intent: intent, TargetVersion: req.TargetVersion, Versions: req.Versions, VersionMode: req.VersionMode,
		CreateTag: req.CreateTag, PushRemote: req.PushRemote, BuildMode: req.BuildMode, SelectedTargets: req.SelectedTargets,
		SensitiveExceptions: req.SensitiveExceptions,
	})
}

func (s *Service) verifyAcceptedCandidate(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, pf *Preflight, selected []string) error {
	if plan == nil || strings.TrimSpace(plan.CandidateID) == "" {
		return &Error{Code: "candidate_not_accepted", Message: "缺少已验收候选，不能提交"}
	}
	cand := s.lookupCandidate(plan.CandidateID)
	if cand == nil {
		return &Error{Code: "candidate_not_found", Message: "验收候选已失效，请重新检查后再发布"}
	}
	cand.mu.Lock()
	defer cand.mu.Unlock()
	if cand.Intent != plan.Intent {
		return &Error{Code: "candidate_stale", Message: "发布意图已变化"}
	}
	if plan.Intent == IntentSaveProgress && (cand.View.Status != "ready" || !cand.View.CanSaveProgress) {
		return &Error{Code: "candidate_not_accepted", Message: "进度候选已取消或不可保存"}
	}
	if err := s.validateCandidateBinding(ctx, cand, cand.Request); err != nil {
		return err
	}
	if plan.Intent == IntentFormal && (!cand.View.Accepted || cand.View.Status != CheckPassed) {
		return &Error{Code: "candidate_not_accepted", Message: "正式发布必须使用已检查通过的验收候选"}
	}
	if plan.CandidateFingerprint != "" && cand.Fingerprint != plan.CandidateFingerprint {
		return &Error{Code: "candidate_stale", Message: "候选版本与当前内容不一致，请重新检查"}
	}
	tree, err := isolatedGit(ctx, cand, "write-tree")
	if err != nil {
		return &Error{Code: "candidate_stale", Message: "无法读取验收候选树"}
	}
	if plan.CandidateTreeHash != "" && strings.TrimSpace(tree) != plan.CandidateTreeHash {
		return &Error{Code: "candidate_stale", Message: "候选树已变化，不能提交未再验证的内容"}
	}
	_ = selected
	_ = pf
	_ = run
	return nil
}

func changeStatus(pf *Preflight, path string) string {
	if pf == nil {
		return ""
	}
	for _, change := range pf.Changes {
		if change.Path == path {
			return change.Status
		}
	}
	return ""
}

func selectedSet(paths []string) map[string]bool {
	out := map[string]bool{}
	for _, path := range paths {
		out[filepath.ToSlash(path)] = true
	}
	return out
}

func boolPtr(v bool) *bool { return &v }

func osAllowed(values []string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "any" || value == runtime.GOOS {
			return true
		}
	}
	return false
}
