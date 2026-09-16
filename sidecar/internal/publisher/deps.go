package publisher

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
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
				findings = append(findings, DependencyFinding{Path: rel, Missing: rel, Blocked: true, Reason: "无法读取选中文件，不能验证依赖关系"})
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
				Path: rel, Reference: spec, Missing: resolved, Suggestion: resolved,
				Reason: "选中文件引用了候选版本中不存在的本地模块", Blocked: true,
			}
			if class.Category == CategoryLocal || class.Category == CategorySensitive {
				finding.Reason = "引用指向被排除的本地资料或敏感文件，不会自动加入发布"
			} else if class.Category == CategoryReview {
				finding.Reason = "引用指向需要确认的文件，不能自动加入"
			} else if sourceExists(sourceRoot, resolved) && !selectedSet[resolved] {
				finding.Reason = "确定的配套文件未被选入候选版本"
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
	specs := []string{}
	switch ext {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".vue":
		for _, match := range jsImport.FindAllStringSubmatch(content, -1) {
			if len(match) >= 2 {
				specs = append(specs, match[1])
			}
		}
	case ".py":
		for _, match := range pyImport.FindAllStringSubmatch(content, -1) {
			spec := match[1]
			if spec == "" && len(match) > 2 {
				spec = match[2]
			}
			if spec != "" {
				specs = append(specs, spec)
			}
		}
	}
	return specs
}

func resolveOneLocalModule(candidateRoot, sourceRoot, dir, spec string) (string, bool, string) {
	spec = strings.TrimSpace(spec)
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
	return joined, false, "无法唯一确定本地模块路径（未找到文件）：" + spec
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
	jsImport     = regexp.MustCompile(`(?m)(?:import\s+(?:[^'"\n]+from\s+)?|export\s+[^'"\n]*from\s+|require\s*\(\s*)['"](\.[^'"]+)['"]`)
	pyImport     = regexp.MustCompile(`(?m)(?:from\s+(\.[\w.]*)\s+import|import\s+(\.[\w.]*))`)
	jsExtensions = []string{".js", ".ts", ".tsx", ".jsx", ".mjs", ".cjs", ".vue", ".json"}
)
