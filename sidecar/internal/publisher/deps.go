package publisher

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func findMissingLocalDependencies(candidateRoot, sourceRoot string, selected []string, classifications []FileClassification) []DependencyFinding {
	classByPath := map[string]FileClassification{}
	for _, item := range classifications {
		classByPath[item.Path] = item
	}
	selectedSet := map[string]bool{}
	for _, rel := range selected {
		selectedSet[filepath.ToSlash(rel)] = true
	}
	findings := []DependencyFinding{}
	seen := map[string]bool{}
	for _, rel := range selected {
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(filepath.Join(candidateRoot, filepath.FromSlash(rel)))
		if err != nil {
			if !strings.Contains(classByPath[rel].Status, "D") {
				findings = append(findings, DependencyFinding{Kind: "unreadable", Path: rel, Missing: rel, Blocked: true, Reason: "无法读取选中文件，不能验证依赖关系"})
			}
			continue
		}
		if looksBinary(raw) {
			continue
		}
		for _, spec := range localImportSpecs(rel, string(raw)) {
			resolved, ok, warning := resolveOneLocalModule(candidateRoot, sourceRoot, path.Dir(rel), spec)
			key := rel + "->" + spec
			if seen[key] {
				continue
			}
			seen[key] = true
			if !ok {
				if warning != "" {
					findings = append(findings, DependencyFinding{
						Path: rel, Reference: spec, Missing: spec, Reason: warning, Blocked: false,
					})
				}
				continue
			}
			if candidateHasPath(candidateRoot, resolved) {
				continue
			}
			class := classByPath[resolved]
			finding := DependencyFinding{
				Kind: "missing",
				Path: rel, Reference: spec, Missing: resolved, Suggestion: resolved,
				Reason: "未在保存副本中找到此引用；可能由构建生成或使用其他解析规则，由项目检查和构建确认", Blocked: false,
			}
			if sourceExists(sourceRoot, resolved) {
				finding.Kind = "not_selected"
			} else if path.Ext(resolved) == "" {
				finding.Kind = "unresolved"
				finding.Reason = "未识别此引用对应的文件；由项目检查和构建确认，不要求补齐推测的文件"
			}
			if class.Category == CategoryLocal || class.Category == CategorySensitive {
				finding.Reason = "引用指向被排除的本地资料或敏感文件，不会自动加入发布"
			} else if class.Category == CategoryReview {
				finding.Reason = "引用指向需要确认的文件，不能自动加入"
			} else if sourceExists(sourceRoot, resolved) && !selectedSet[resolved] {
				finding.Reason = "本机有同名文件未选入；是否需要它，由项目检查和构建确认"
			}
			findings = append(findings, finding)
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			return findings[i].Missing < findings[j].Missing
		}
		return findings[i].Path < findings[j].Path
	})
	return findings
}

func candidateHasPath(root, rel string) bool {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if fileExists(abs) {
		return true
	}
	info, err := os.Stat(abs)
	return err == nil && info.IsDir()
}

func sourceExists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func localImportSpecs(from, content string) []string {
	ext := strings.ToLower(path.Ext(from))
	switch ext {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		return localJSImportSpecs(content)
	case ".vue":
		return localVueImportSpecs(content)
	}
	// Python's module search and imports in strings/comments cannot be determined
	// by a regular expression. Let the project's own tools validate that language.
	return nil
}

func resolveOneLocalModule(candidateRoot, sourceRoot, dir, spec string) (string, bool, string) {
	spec = strings.TrimSpace(spec)
	// Bundler resource modifiers do not belong to the filename (e.g. ?raw, ?url).
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		if index := strings.IndexAny(spec, "?#"); index >= 0 {
			spec = spec[:index]
		}
	}
	if spec == "" {
		return "", false, ""
	}
	joined := ""
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		joined = path.Clean(path.Join(dir, spec))
	} else if strings.HasPrefix(spec, ".") {
		joined = path.Clean(path.Join(dir, pythonRelative(spec)))
	} else {
		return "", false, ""
	}
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false, "相对导入超出项目目录，已忽略"
	}
	joined = filepath.ToSlash(joined)
	candidates := moduleCandidates(joined)
	for _, item := range candidates {
		if candidateHasPath(candidateRoot, item) {
			return item, true, ""
		}
	}
	for _, item := range candidates {
		if sourceExists(sourceRoot, item) {
			return item, true, ""
		}
	}
	if path.Ext(joined) != "" {
		return joined, true, ""
	}
	// This is advisory only. Keep the requested path, never invent a missing file.
	return joined, true, ""
}

func pythonRelative(spec string) string {
	n := 0
	for strings.HasPrefix(spec, ".") {
		n++
		spec = spec[1:]
	}
	prefix := "."
	for i := 1; i < n; i++ {
		prefix += "/.."
	}
	if spec == "" {
		return prefix
	}
	return prefix + "/" + strings.ReplaceAll(spec, ".", "/")
}

func moduleCandidates(joined string) []string {
	joined = filepath.ToSlash(joined)
	if path.Ext(joined) != "" {
		return []string{joined}
	}
	out := []string{}
	for _, ext := range jsExtensions {
		out = append(out, joined+ext)
	}
	out = append(out,
		path.Join(joined, "index.js"), path.Join(joined, "index.ts"),
		path.Join(joined, "index.vue"), path.Join(joined, "__init__.py"),
		joined+".py",
	)
	seen := map[string]bool{}
	uniq := []string{}
	for _, item := range out {
		item = filepath.ToSlash(item)
		if seen[item] {
			continue
		}
		seen[item] = true
		uniq = append(uniq, item)
	}
	return uniq
}

var (
	jsExtensions = []string{".js", ".ts", ".tsx", ".jsx", ".mjs", ".cjs", ".vue", ".json"}
)
