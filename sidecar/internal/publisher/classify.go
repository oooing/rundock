package publisher

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func classifyChanges(repo string, changes []FileChange, rules []releaseconfig.FileRule, decisions []ManualDecision) []FileClassification {
	decisionByPath := map[string]ManualDecision{}
	for _, decision := range decisions {
		path := filepath.ToSlash(decision.Path)
		if path != "" {
			decisionByPath[path] = decision
		}
	}
	out := make([]FileClassification, 0, len(changes))
	for _, change := range changes {
		item := classifyOne(repo, change, rules)
		if decision, ok := decisionByPath[item.Path]; ok {
			item = applyManualDecision(item, decision)
		}
		out = append(out, item)
	}
	return out
}

func classifyOne(repo string, change FileChange, rules []releaseconfig.FileRule) FileClassification {
	item := FileClassification{
		Path:               filepath.ToSlash(change.Path),
		OldPath:            filepath.ToSlash(change.OldPath),
		Status:             change.Status,
		Tracked:            change.Tracked,
		Reasons:            []string{},
		Sources:            []string{},
		RuleIDs:            []string{},
		Group:              classificationGroup(change.Path),
		ContentFingerprint: fileContentFingerprint(repo, change),
	}
	kinds := map[string][]releaseconfig.FileRule{}
	for _, rule := range rules {
		if matchFileRule(rule.Pattern, item.Path) || (item.OldPath != "" && matchFileRule(rule.Pattern, item.OldPath)) {
			kind := strings.ToLower(strings.TrimSpace(rule.Kind))
			kinds[kind] = append(kinds[kind], rule)
			item.RuleIDs = append(item.RuleIDs, rule.ID)
		}
	}
	if len(kinds[releaseconfig.RuleSensitive]) > 0 {
		item.Category = CategorySensitive
		item.Sources = append(item.Sources, "rule")
		for _, rule := range kinds[releaseconfig.RuleSensitive] {
			item.Reasons = append(item.Reasons, ruleReason(rule, "命中敏感规则"))
		}
		item.SelectedDefault = false
		if change.Tracked {
			item.BaselineKept = true
			item.Reasons = append(item.Reasons, "该文件已在当前基线提交中，取消勾选不会从发布版本移除旧内容")
		}
		return item
	}
	distinct := 0
	for _, kind := range []string{releaseconfig.RuleRecommend, releaseconfig.RuleLocal, releaseconfig.RuleReview} {
		if len(kinds[kind]) > 0 {
			distinct++
		}
	}
	if distinct > 1 {
		item.Category = CategoryReview
		item.Sources = append(item.Sources, "rule")
		item.Reasons = append(item.Reasons, "多条项目规则冲突，需要确认后再发布")
		for _, kind := range []string{releaseconfig.RuleRecommend, releaseconfig.RuleLocal, releaseconfig.RuleReview} {
			for _, rule := range kinds[kind] {
				item.Reasons = append(item.Reasons, ruleReason(rule, "命中规则 "+kind))
			}
		}
		item.SelectedDefault = false
		if change.Tracked && len(kinds[releaseconfig.RuleLocal]) > 0 {
			item.BaselineKept = true
			item.Reasons = append(item.Reasons, "已跟踪文件命中排除规则时，基线内容仍会保留在候选版本中")
		}
		return item
	}
	switch {
	case len(kinds[releaseconfig.RuleReview]) > 0:
		item.Category = CategoryReview
		item.Sources = append(item.Sources, "rule")
		for _, rule := range kinds[releaseconfig.RuleReview] {
			item.Reasons = append(item.Reasons, ruleReason(rule, "命中需确认规则"))
		}
	case len(kinds[releaseconfig.RuleLocal]) > 0:
		item.Category = CategoryLocal
		item.Sources = append(item.Sources, "rule")
		for _, rule := range kinds[releaseconfig.RuleLocal] {
			item.Reasons = append(item.Reasons, ruleReason(rule, "命中本地资料规则"))
		}
		if change.Tracked {
			item.BaselineKept = true
			item.Reasons = append(item.Reasons, "已跟踪文件命中排除规则时，基线内容仍会保留在候选版本中，不能视为已从发布中移除")
		}
	case len(kinds[releaseconfig.RuleRecommend]) > 0:
		item.Category = CategoryRecommend
		item.Sources = append(item.Sources, "rule")
		for _, rule := range kinds[releaseconfig.RuleRecommend] {
			item.Reasons = append(item.Reasons, ruleReason(rule, "命中推荐包含规则"))
		}
	default:
		applyHeuristic(&item, change)
	}
	if item.Category == CategoryRecommend && change.Tracked {
		item.SelectedDefault = true
	}
	if item.Category == CategoryRecommend && !change.Tracked {
		item.SelectedDefault = true
	}
	if item.Category == CategoryLocal || item.Category == CategoryReview || item.Category == CategorySensitive {
		item.SelectedDefault = false
	}
	if strings.ContainsAny(change.Status, "D") {
		item.Reasons = append(item.Reasons, "删除已跟踪文件是版本变更，不会删除本机原文件")
	}
	if change.OldPath != "" {
		item.Reasons = append(item.Reasons, "重命名："+change.OldPath+" → "+change.Path)
	}
	return item
}

func applyHeuristic(item *FileClassification, change FileChange) {
	item.Sources = append(item.Sources, "heuristic")
	if change.Tracked {
		item.Category = CategoryRecommend
		item.Reasons = append(item.Reasons, "已跟踪文件的本次改动默认纳入候选版本")
		return
	}
	if looksLocalOutput(item.Path) {
		item.Category = CategoryLocal
		item.Reasons = append(item.Reasons, "未跟踪的构建、缓存或临时输出，默认不提交且不会删除")
		return
	}
	if strings.HasPrefix(strings.ToLower(item.Path), "reports/") {
		item.Category = CategoryLocal
		item.Reasons = append(item.Reasons, "未跟踪的报告、快照或本地验证资料，默认留在本地；需要共享时可手动纳入")
		return
	}
	if looksReleaseSource(item.Path) {
		item.Category = CategoryRecommend
		item.Reasons = append(item.Reasons, "未跟踪的正式源码、测试、锁文件、迁移或构建脚本，不因未跟踪而漏选")
		return
	}
	item.Category = CategoryReview
	item.Reasons = append(item.Reasons, "用途未知，需要确认后再正式发布")
}

func applyManualDecision(item FileClassification, decision ManualDecision) FileClassification {
	if strings.TrimSpace(decision.ContentFingerprint) == "" {
		item.Reasons = append(item.Reasons, "人工决定未绑定内容指纹，不能作为有效确认")
		item.Sources = append(item.Sources, "manual-invalid")
		return item
	}
	if item.ContentFingerprint != "" && decision.ContentFingerprint != item.ContentFingerprint {
		item.Reasons = append(item.Reasons, "文件内容已变化，请重新确认此前的人工决定")
		item.Sources = append(item.Sources, "manual-stale")
		return item
	}
	switch strings.ToLower(strings.TrimSpace(decision.Decision)) {
	case DecisionInclude:
		if item.Category == CategorySensitive {
			item.Reasons = append(item.Reasons, "敏感阻断不能通过勾选绕过")
			return item
		}
		item.Sources = appendUniqueSource(item.Sources, "manual")
		if decision.Reason != "" {
			item.Reasons = append(item.Reasons, "人工决定纳入："+decision.Reason)
		} else {
			item.Reasons = append(item.Reasons, "已记录人工纳入决定")
		}
		item.SelectedDefault = true
		if item.Category == CategoryReview || item.Category == CategoryLocal {
			item.Category = CategoryRecommend
		}
	case DecisionExclude:
		item.Sources = appendUniqueSource(item.Sources, "manual")
		if decision.Reason != "" {
			item.Reasons = append(item.Reasons, "人工决定排除："+decision.Reason)
		} else {
			item.Reasons = append(item.Reasons, "已记录人工排除决定")
		}
		item.SelectedDefault = false
		if item.Tracked {
			item.BaselineKept = true
		}
	}
	return item
}

func ruleReason(rule releaseconfig.FileRule, fallback string) string {
	if strings.TrimSpace(rule.Reason) != "" {
		return strings.TrimSpace(rule.Reason)
	}
	return fallback + "（" + rule.Pattern + "）"
}

func classificationGroup(rel string) string {
	rel = filepath.ToSlash(rel)
	if i := strings.Index(rel, "/"); i > 0 {
		return rel[:i]
	}
	return "."
}

func fileContentFingerprint(repo string, change FileChange) string {
	if strings.ContainsAny(change.Status, "D") && change.OldPath == "" && !fileExists(filepath.Join(repo, filepath.FromSlash(change.Path))) {
		sum := sha256.Sum256([]byte("deleted:" + change.Path))
		return hex.EncodeToString(sum[:])
	}
	safe, err := secureProjectPath(repo, change.Path, false)
	if err != nil {
		return "unreadable:" + change.Path
	}
	info, err := os.Lstat(safe)
	if err != nil || isPathLink(info) || !info.Mode().IsRegular() {
		return "unsupported:" + change.Path
	}
	raw, err := os.ReadFile(safe)
	if err != nil {
		sum := sha256.Sum256([]byte("missing:" + change.Path))
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func matchFileRule(pattern, filePath string) bool {
	pattern = strings.TrimSpace(filepath.ToSlash(pattern))
	filePath = filepath.ToSlash(filePath)
	if pattern == "" || filePath == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		prefix := strings.TrimSuffix(pattern, "/")
		return filePath == prefix || strings.HasPrefix(filePath, prefix+"/")
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return filePath == prefix || strings.HasPrefix(filePath, prefix+"/")
	}
	if pattern == "**" || pattern == "**/*" {
		return true
	}
	if ok, _ := path.Match(pattern, filePath); ok {
		return true
	}
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, filePath)
	}
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(filePath))
		return ok
	}
	return false
}

func matchDoubleStar(pattern, filePath string) bool {
	pattern = filepath.ToSlash(pattern)
	filePath = filepath.ToSlash(filePath)
	parts := strings.Split(pattern, "**")
	if len(parts) == 0 {
		return false
	}
	rest := filePath
	head := strings.TrimSuffix(parts[0], "/")
	if head != "" {
		if rest != head && !strings.HasPrefix(rest, head+"/") {
			return false
		}
		if rest == head {
			rest = ""
		} else {
			rest = strings.TrimPrefix(rest, head+"/")
		}
	}
	tail := strings.TrimPrefix(parts[len(parts)-1], "/")
	if tail == "" {
		return true
	}
	if ok, _ := path.Match(tail, rest); ok {
		return true
	}
	for {
		slash := strings.Index(rest, "/")
		if slash < 0 {
			ok, _ := path.Match(tail, rest)
			return ok
		}
		rest = rest[slash+1:]
		if ok, _ := path.Match(tail, rest); ok {
			return true
		}
	}
}

func looksLocalOutput(rel string) bool {
	rel = filepath.ToSlash(strings.ToLower(rel))
	base := path.Base(rel)
	markers := []string{
		"node_modules/", ".git/", "dist/", "build/", "out/", "coverage/",
		".cache/", ".tmp/", "tmp/", "__pycache__/", ".pytest_cache/",
		".next/", ".nuxt/", ".turbo/", ".output/", "vendor/",
	}
	for _, marker := range markers {
		if rel == strings.TrimSuffix(marker, "/") || strings.HasPrefix(rel, marker) || strings.Contains(rel, "/"+marker) {
			return true
		}
	}
	switch {
	case strings.HasSuffix(base, ".log"), strings.HasSuffix(base, ".tmp"), strings.HasSuffix(base, "~"):
		return true
	case base == ".ds_store", base == "thumbs.db":
		return true
	}
	return isDiagnosticsPath(rel)
}

func looksReleaseSource(rel string) bool {
	rel = filepath.ToSlash(rel)
	lower := strings.ToLower(rel)
	base := strings.ToLower(path.Base(rel))
	// License notices belong with the source they license. Do not recommend
	// arbitrary .txt files (or private .key/.pem files) by extension alone.
	licenseName := base
	if ext := path.Ext(base); ext == ".txt" || ext == ".md" || ext == ".rst" {
		licenseName = strings.TrimSuffix(base, ext)
	}
	if path.Ext(licenseName) == "" && (licenseName == "license" || licenseName == "licence" || licenseName == "copying" ||
		strings.HasPrefix(licenseName, "license-") || strings.HasPrefix(licenseName, "license_")) {
		return true
	}
	if strings.Contains(lower, "/migrations/") || strings.HasPrefix(lower, "migrations/") || strings.HasSuffix(lower, ".sql") {
		return true
	}
	if strings.Contains(lower, "/__tests__/") || strings.Contains(lower, "/tests/") || strings.Contains(lower, "/test/") {
		return true
	}
	switch base {
	case ".gitignore", "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.mod", "go.sum",
		"cargo.toml", "cargo.lock", "gemfile", "gemfile.lock", "pipfile", "poetry.lock",
		"makefile", "dockerfile", "tsconfig.json", "vite.config.ts", "vite.config.js":
		return true
	}
	ext := strings.ToLower(path.Ext(base))
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".py", ".rs", ".java", ".kt",
		".c", ".h", ".cpp", ".cc", ".cs", ".rb", ".php", ".swift", ".css", ".scss", ".html",
		".json", ".yml", ".yaml", ".toml", ".md", ".svg", ".sh", ".ps1", ".bat", ".cmd", ".nsi", ".nsh":
		if strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.js") || strings.HasSuffix(base, "_test.go") {
			return true
		}
		return true
	}
	return false
}

func appendUniqueSource(values []string, add string) []string {
	for _, value := range values {
		if value == add {
			return values
		}
	}
	return append(values, add)
}

func defaultSelectedPaths(items []FileClassification) []string {
	out := []string{}
	for _, item := range items {
		if item.SelectedDefault && item.Category != CategorySensitive && item.Category != CategoryLocal {
			out = append(out, item.Path)
		}
	}
	return out
}

func unresolvedReview(items []FileClassification, selected map[string]bool, decisions []ManualDecision) []FileClassification {
	_ = selected
	decided := map[string]bool{}
	for _, decision := range decisions {
		if decision.Decision != DecisionInclude && decision.Decision != DecisionExclude {
			continue
		}
		if strings.TrimSpace(decision.ContentFingerprint) == "" {
			continue
		}
		for _, item := range items {
			if item.Path == filepath.ToSlash(decision.Path) && item.ContentFingerprint == decision.ContentFingerprint {
				decided[item.Path] = true
			}
		}
	}
	out := []FileClassification{}
	for _, item := range items {
		if item.Category != CategoryReview {
			continue
		}
		if decided[item.Path] {
			continue
		}
		out = append(out, item)
	}
	return out
}
