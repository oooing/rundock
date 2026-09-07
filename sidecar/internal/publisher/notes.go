package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/launcher-sidecar/internal/store"
)

const maxReleaseNotesBytes = 64 * 1024

const (
	releaseNoteFeature     = "功能"
	releaseNoteFix         = "问题修复"
	releaseNotePerformance = "性能优化"
)

var (
	conventionalNotePattern = regexp.MustCompile(`(?i)^(feat(?:ure)?|add|fix|bug|bugfix|hotfix|perf(?:ormance)?)(?:\([^\r\n)]*\))?!?\s*[:：]\s*(.+)$`)
	ignoredNotePattern      = regexp.MustCompile(`(?i)^(chore|build|ci|docs?|test|style|refactor|merge|revert)(?:\([^\r\n)]*\))?!?\s*[:：]?`)
	pureNumberPattern       = regexp.MustCompile(`^[\d\s._-]+$`)
	pureVersionPattern      = regexp.MustCompile(`(?i)^v?\d+\.\d+\.\d+(?:[-+][0-9a-z.-]+)?$`)
	markdownCodePattern     = regexp.MustCompile("`[^`]*`")
	fileExtensionPattern    = regexp.MustCompile(`(?i)\.(go|py|js|jsx|ts|tsx|rs|java|kt|swift|json|ya?ml|toml|md|txt|html?|css|scss|vue|bat|cmd|ps1|sh|sql|lock)$`)
)

// DraftReleaseNotes produces a deterministic, local-only draft. It never
// fetches a remote, so opening the release panel is not held up by the network.
func (s *Service) DraftReleaseNotes(ctx context.Context, appID string, req NotesDraftRequest) (*NotesDraft, error) {
	pf, err := s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, err
	}
	if req.StatusFingerprint == "" || req.StatusFingerprint != pf.StatusFingerprint {
		return nil, &Error{Code: "status_changed", Message: "仓库内容已变化，请重新检查后再生成更新说明"}
	}
	paths, err := validateSelected(req.SelectedPaths, pf.Changes)
	if err != nil {
		return nil, err
	}
	selections, err := validateTargetSelections(req.SelectedTargets)
	if err != nil {
		return nil, err
	}
	plan, err := s.freezeExecutionPlan(ctx, appID, pf.RepoRoot, selections)
	if err != nil {
		return nil, err
	}

	baseTags := releaseNotesBaseTags(pf, plan)
	commits := []string{}
	for _, baseTag := range baseTags {
		subjects, logErr := s.releaseCommitSubjects(ctx, pf.RepoRoot, baseTag)
		if logErr != nil {
			return nil, &Error{Code: "release_notes_failed", Message: "无法读取版本提交记录"}
		}
		for _, subject := range subjects {
			commits = appendUnique(commits, subject)
		}
	}
	if len(baseTags) == 0 {
		var logErr error
		commits, logErr = s.releaseCommitSubjects(ctx, pf.RepoRoot, "")
		if logErr != nil {
			return nil, &Error{Code: "release_notes_failed", Message: "无法读取版本提交记录"}
		}
	}

	categories := map[string][]string{releaseNoteFeature: {}, releaseNoteFix: {}, releaseNotePerformance: {}}
	for _, subject := range commits {
		category, line, ok := releaseNoteFromSubject(subject)
		if ok {
			categories[category] = appendUniqueFold(categories[category], line)
		}
	}
	changeNotes, err := s.releaseChangeNotes(ctx, pf.RepoRoot, baseTags, paths)
	if err != nil {
		return nil, &Error{Code: "release_notes_failed", Message: "无法读取本次代码变更，请重新检查后再生成"}
	}
	hasCommitNotes := false
	for _, items := range categories {
		hasCommitNotes = hasCommitNotes || len(items) > 0
	}
	for _, item := range changeNotes {
		if hasCommitNotes && (item == "调整内部实现与项目维护内容" || item == "本次未检测到代码变化" || item == "调整应用内部功能实现") {
			continue
		}
		categories[releaseNoteAdjustment] = appendUniqueFold(categories[releaseNoteAdjustment], item)
	}

	targetNames := make([]string, 0, len(plan.Targets))
	for _, target := range plan.Targets {
		name := strings.TrimSpace(target.Name)
		if name == "" {
			name = target.ID
		}
		targetNames = append(targetNames, name)
	}
	sort.Strings(targetNames)
	scope := "仅提交代码"
	if len(targetNames) > 0 {
		scope = strings.Join(targetNames, "、")
	}
	text := renderReleaseNotes(scope, categories)
	baseTag := strings.Join(baseTags, "、")

	fingerprintInput := struct {
		StatusFingerprint string                         `json:"statusFingerprint"`
		BaseTag           string                         `json:"baseTag"`
		Commits           []string                       `json:"commits"`
		Paths             []string                       `json:"paths"`
		Targets           []store.ReleaseTargetSelection `json:"targets"`
	}{pf.StatusFingerprint, baseTag, commits, paths, sortedTargetSelections(selections)}
	raw, _ := json.Marshal(fingerprintInput)
	sum := sha256.Sum256(raw)
	return &NotesDraft{
		Text: text, BaseTag: baseTag, CommitCount: len(commits), ChangeCount: len(paths),
		SourceFingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

func releaseNotesBaseTags(pf *Preflight, plan *executionPlan) []string {
	if pf == nil {
		return nil
	}
	tags := []string{}
	if plan != nil && plan.usesConfiguredVersionGroups() {
		for _, group := range plan.VersionGroups {
			if tag := strings.TrimSpace(pf.LatestGroupTags[group.ID]); tag != "" {
				tags = appendUnique(tags, tag)
			}
		}
	}
	if len(tags) == 0 {
		if tag := strings.TrimSpace(pf.LatestTag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func (s *Service) releaseCommitSubjects(ctx context.Context, repo, baseTag string) ([]string, error) {
	args := []string{"log", "--max-count=100", "--format=%s%x00"}
	if strings.TrimSpace(baseTag) != "" {
		args = append(args, baseTag+"..HEAD")
	} else {
		args = append(args, "HEAD")
	}
	raw, err := s.gitRaw(ctx, repo, args...)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, subject := range strings.Split(raw, "\x00") {
		if subject = cleanReleaseNoteLine(subject); subject != "" {
			out = append(out, subject)
		}
	}
	return out, nil
}

func releaseNoteFromSubject(subject string) (category, line string, ok bool) {
	line = cleanReleaseNoteLine(subject)
	if line == "" || pureNumberPattern.MatchString(line) || pureVersionPattern.MatchString(line) || ignoredNotePattern.MatchString(line) {
		return "", "", false
	}
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "release ") || strings.HasPrefix(lower, "version ") || strings.HasPrefix(lower, "bump ") {
		return "", "", false
	}

	if match := conventionalNotePattern.FindStringSubmatch(line); len(match) == 3 {
		kind := strings.ToLower(match[1])
		line = match[2]
		switch {
		case strings.HasPrefix(kind, "feat"), kind == "add":
			category = releaseNoteFeature
		case kind == "fix", kind == "bug", kind == "bugfix", kind == "hotfix":
			category = releaseNoteFix
		default:
			category = releaseNotePerformance
		}
	} else {
		switch {
		case containsAnyFold(line, "修复", "缺陷", "故障", "解决问题", " bug ", "bugfix", "hotfix"):
			category = releaseNoteFix
		case containsAnyFold(line, "性能", "提速", "加速", "耗时", "响应速度", "加载速度", "内存占用", "卡顿", "performance"):
			category = releaseNotePerformance
		case startsWithAnyFold(line, "新增", "增加", "支持", "实现", "功能", "add ", "support ", "implement "):
			category = releaseNoteFeature
		case startsWithAnyFold(line, "优化", "调整", "改善", "改进", "improve ", "adjust "):
			category = releaseNoteAdjustment
		default:
			return "", "", false
		}
	}

	line = conciseReleaseNoteLine(line)
	if line == "" || genericReleaseNoteLine(line) {
		return "", "", false
	}
	return category, line, true
}

func conciseReleaseNoteLine(value string) string {
	value = markdownCodePattern.ReplaceAllString(value, " ")
	parts := strings.Fields(value)
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		candidate := strings.Trim(part, " ,，。.;；:：()（）[]【】<>")
		if strings.ContainsAny(candidate, `/\\`) || fileExtensionPattern.MatchString(candidate) {
			continue
		}
		kept = append(kept, part)
	}
	value = cleanReleaseNoteLine(strings.Join(kept, " "))
	value = strings.Trim(value, " -—:：,，。.;；")
	runes := []rune(value)
	if len(runes) > 80 {
		value = string(runes[:80]) + "…"
	}
	return value
}

func genericReleaseNoteLine(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch lower {
	case "update", "updates", "change", "changes", "fix", "bugfix", "feature", "performance", "更新", "修改", "调整", "修复", "优化", "新增", "改进":
		return true
	default:
		return pureNumberPattern.MatchString(lower) || pureVersionPattern.MatchString(lower)
	}
}

func containsAnyFold(value string, candidates ...string) bool {
	lower := strings.ToLower(value)
	for _, candidate := range candidates {
		if strings.Contains(lower, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func startsWithAnyFold(value string, candidates ...string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range candidates {
		if strings.HasPrefix(lower, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func renderReleaseNotes(scope string, categories map[string][]string) string {
	var out strings.Builder
	out.WriteString("**发布范围：** ")
	out.WriteString(scope)
	out.WriteString("\n")
	remaining := 5
	for _, category := range []string{releaseNoteFeature, releaseNoteFix, releaseNotePerformance, releaseNoteAdjustment} {
		items := categories[category]
		if len(items) == 0 || remaining == 0 {
			continue
		}
		out.WriteString("\n## ")
		out.WriteString(category)
		out.WriteString("\n")
		limit := len(items)
		if limit > remaining {
			limit = remaining
		}
		for _, item := range items[:limit] {
			out.WriteString("- ")
			out.WriteString(item)
			out.WriteString("\n")
		}
		remaining -= limit
	}
	return strings.TrimSpace(out.String())
}

func cleanReleaseNoteLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\x00", "")
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) > 240 {
		value = string([]rune(value)[:240]) + "…"
	}
	return value
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueFold(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func sortedTargetSelections(values []store.ReleaseTargetSelection) []store.ReleaseTargetSelection {
	out := append([]store.ReleaseTargetSelection{}, values...)
	sort.Slice(out, func(i, j int) bool { return out[i].TargetID < out[j].TargetID })
	return out
}

func normalizeReleaseNotes(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", &Error{Code: "invalid_release_notes", Message: "更新说明包含无效字符"}
	}
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	value = strings.ReplaceAll(value, "\r", "\n")
	if value == "" {
		return "", &Error{Code: "release_notes_required", Message: "创建版本 Tag 前请确认更新说明"}
	}
	if strings.Contains(strings.ToLower(value), "launcher-release-plan:") {
		return "", &Error{Code: "invalid_release_notes", Message: "更新说明包含保留的发布计划标记"}
	}
	if len([]byte(value)) > maxReleaseNotesBytes {
		return "", &Error{Code: "release_notes_too_long", Message: "更新说明不能超过 64 KB"}
	}
	for _, r := range value {
		if r < 0x20 && r != '\n' && r != '\t' {
			return "", &Error{Code: "invalid_release_notes", Message: "更新说明包含不支持的控制字符"}
		}
	}
	return value, nil
}
