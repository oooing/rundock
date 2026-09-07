package publisher

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const releaseNoteAdjustment = "功能调整"

var noteWordBoundary = regexp.MustCompile(`([a-z])([A-Z])`)

// This is deliberately a coarse, local summary, not a claim to understand
// arbitrary code. Only fixed product descriptions leave this analysis; source
// strings, paths and credentials never become release-note bullets.
func (s *Service) releaseChangeNotes(ctx context.Context, repo string, bases, selected []string) ([]string, error) {
	files := map[string]bool{}
	patches := map[string]string{}
	collect := func(revisions, paths []string) error {
		args := append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--numstat", "-z"}, revisions...)
		args = append(args, "--")
		args = append(args, paths...)
		raw, err := s.gitRaw(ctx, repo, args...)
		if err != nil {
			return err
		}
		inspect := []string{}
		lines := 0
		for _, record := range strings.Split(raw, "\x00") {
			parts := strings.SplitN(record, "\t", 3)
			if len(parts) != 3 {
				continue
			}
			name := parts[2]
			files[name] = true
			added, e1 := strconv.Atoi(parts[0])
			removed, e2 := strconv.Atoi(parts[1])
			// Bound patch inspection, and disable user-configured diff helpers.
			if noteSourceFile(name) && e1 == nil && e2 == nil && added+removed <= 500 && lines+added+removed <= 2000 && len(inspect) < 16 {
				inspect = append(inspect, ":(literal)"+name)
				lines += added + removed
			}
		}
		if len(inspect) == 0 {
			return nil
		}
		args = append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--unified=0"}, revisions...)
		args = append(args, "--")
		args = append(args, inspect...)
		patch, err := s.gitRaw(ctx, repo, args...)
		if err != nil {
			return err
		}
		// Only recognizable changed code identifiers are used as evidence.
		for _, name := range inspect {
			name = strings.TrimPrefix(name, ":(literal)")
			marker := "diff --git a/" + name + " b/" + name + "\n"
			start := strings.Index(patch, marker)
			if start < 0 {
				continue
			}
			section := patch[start+len(marker):]
			if end := strings.Index(section, "\ndiff --git "); end >= 0 {
				section = section[:end]
			}
			for _, line := range strings.Split(section, "\n") {
				if (strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")) || (strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")) {
					patches[name] += line + "\n"
				}
			}
		}
		return nil
	}
	for _, base := range bases {
		if err := collect([]string{base, "HEAD"}, nil); err != nil {
			return nil, err
		}
	}
	if len(bases) == 0 {
		// A first release has no prior snapshot; summarize tracked product areas.
		raw, err := s.gitRaw(ctx, repo, "ls-tree", "-r", "--name-only", "-z", "HEAD")
		if err != nil {
			return nil, err
		}
		for _, name := range strings.Split(raw, "\x00") {
			if name != "" {
				files[name] = true
			}
		}
	}
	if len(selected) > 0 {
		literal := []string{}
		for _, name := range selected {
			files[name] = true
			literal = append(literal, ":(literal)"+name)
		}
		if err := collect([]string{"HEAD"}, literal); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	items := []string{}
	for _, name := range names {
		for _, item := range noteAreas(name, patches[name]) {
			items = appendUnique(items, item)
		}
	}
	if len(items) == 0 {
		if len(files) > 0 {
			return []string{"调整内部实现与项目维护内容"}, nil
		}
		return []string{"本次未检测到代码变化"}, nil
	}
	// Specific areas are more useful than repeating an overall UI/code summary.
	if len(items) > 1 {
		filtered := []string{}
		specificRelease := contains(items, "调整更新说明的生成与编辑") || contains(items, "调整发布失败后的重试流程与提示")
		for _, item := range items {
			if item == "调整界面布局与交互" || item == "调整应用内部功能实现" || (specificRelease && item == "调整版本发布与上传流程") {
				continue
			}
			filtered = append(filtered, item)
		}
		if len(filtered) > 0 {
			items = filtered
		}
	}
	return items, nil
}

func noteSourceFile(name string) bool {
	lower := strings.ToLower(name)
	for _, part := range strings.Split(lower, "/") {
		if contains([]string{"tests", "test", "__tests__", "docs", "scripts", "vendor", "node_modules", "dist", "generated", "i18n", "locales"}, part) {
			return false
		}
	}
	if strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") || strings.HasSuffix(lower, ".d.ts") {
		return false
	}
	return contains([]string{".go", ".rs", ".py", ".js", ".jsx", ".ts", ".tsx", ".vue", ".html", ".css", ".scss", ".swift", ".kt", ".java", ".dart"}, path.Ext(lower))
}

func noteAreas(name, patch string) []string {
	if !noteSourceFile(name) {
		return nil
	}
	words := strings.ToLower(noteWordBoundary.ReplaceAllString(name, "$1 $2"))
	words = " " + strings.Join(strings.FieldsFunc(words, func(r rune) bool { return r < 'a' || r > 'z' }), " ") + " "
	has := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(words, " "+value+" ") {
				return true
			}
		}
		return false
	}
	if has("publisher", "release", "releases") {
		items := []string{}
		if has("notes") || containsAnyFold(patch, "releaseNotes", "release_notes", "notesDraft") {
			items = append(items, "调整更新说明的生成与编辑")
		}
		if has("retry") || containsAnyFold(patch, "retryRelease", "retrying", "retryRemote", "retryConfirmation") {
			items = append(items, "调整发布失败后的重试流程与提示")
		}
		if len(items) > 0 {
			return items
		}
		return []string{"调整版本发布与上传流程"}
	}
	for _, area := range []struct {
		words []string
		text  string
	}{
		{[]string{"login", "auth", "signin", "signup"}, "调整登录与账号验证流程"},
		{[]string{"subtitle", "subtitles"}, "调整字幕显示与设置"},
		{[]string{"player", "playback", "video"}, "调整视频播放相关功能"},
		{[]string{"download", "downloads"}, "调整下载任务管理"},
		{[]string{"search", "filter"}, "调整搜索与筛选功能"},
		{[]string{"settings", "preferences"}, "调整设置选项与操作"},
		{[]string{"appcard", "card", "dashboard", "sidebar"}, "调整项目列表与管理界面"},
	} {
		if has(area.words...) {
			return []string{area.text}
		}
	}
	if has("components", "pages", "views") || contains([]string{".vue", ".html", ".css", ".scss"}, path.Ext(name)) {
		return []string{"调整界面布局与交互"}
	}
	return []string{"调整应用内部功能实现"}
}
