package releaseconfig

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// tagWorkflow contains only the small, verified part of a GitHub Actions
// workflow that discovery needs.  We deliberately do not try to interpret
// arbitrary YAML or expressions: an uncertain workflow must leave the target
// on its existing local runner.
type tagWorkflow struct {
	name        string
	tagPatterns []string
	signals     workflowSignals
}

type workflowSignals struct {
	container bool
	web       bool
	server    bool
	windows   bool
	macOS     bool
	android   bool
}

var (
	windowsRunnerRE = regexp.MustCompile(`(?m)runs-on\s*:\s*[^\r\n]*windows(?:-|\b)`)
	macRunnerRE     = regexp.MustCompile(`(?m)runs-on\s*:\s*[^\r\n]*macos(?:-|\b)`)
	webBuildRE      = regexp.MustCompile(`(?m)(?:npm\s+run\s+build|pnpm\s+(?:run\s+)?build|yarn\s+build|vite\s+build|next\s+build)`)
	serverBuildRE   = regexp.MustCompile(`(?m)(?:go\s+build\b|dotnet\s+publish\b|mvn\s+(?:[^\r\n]*\s)?package\b|gradle\w*\s+[^\r\n]*bootjar\b)`)
	androidBuildRE  = regexp.MustCompile(`(?m)(?:gradlew(?:\.bat)?[^\r\n]*(?:assemblerelease|bundlerelease)|expo\s+prebuild[^\r\n]*--platform\s+android|eas\s+build[^\r\n]*--platform\s+android)`)
	desktopBuildRE  = regexp.MustCompile(`(?m)(?:tauri(?:-action@|\s+build\b)|electron-builder\b|electron-forge[^\r\n]*make\b)`)
)

func (b *discoveryBuilder) applyTagTriggeredWorkflowRunners() {
	workflows := discoverTagWorkflows(b.root)
	if len(workflows) == 0 {
		return
	}
	groupPrefixes := make(map[string]string, len(b.config.VersionGroups))
	for _, group := range b.config.VersionGroups {
		prefix := strings.TrimSpace(group.TagPrefix)
		if prefix == "" {
			prefix = group.ID
		}
		groupPrefixes[group.ID] = prefix
	}

	convertedByWorkflow := map[string][]string{}
	for i := range b.config.Targets {
		target := &b.config.Targets[i]
		if !targetHasReleaseWork(*target) {
			// Check-only targets are local validation helpers, not independently
			// published products.  Claiming them would duplicate the real cloud
			// target in the UI and would silently remove their useful local check.
			continue
		}
		prefix := groupPrefixes[target.VersionGroup]
		if prefix == "" {
			continue
		}
		for _, workflow := range workflows {
			if !workflow.matchesPrefix(prefix) || !workflow.supports(*target, prefix) {
				continue
			}
			target.Runner = Runner{Type: RunnerGitPush, OS: []string{}}
			// A git-push target is a declaration that the matching workflow owns
			// the build. Keeping local commands here would let the UI offer work
			// which the executor must never run for this runner.
			target.Steps = Steps{Publish: "tag-push"}
			if target.Confidence < 0.97 {
				target.Confidence = 0.97
			}
			convertedByWorkflow[workflow.name] = append(convertedByWorkflow[workflow.name], target.Name)
			break
		}
	}
	for _, workflow := range workflows {
		names := sortedUnique(convertedByWorkflow[workflow.name])
		if len(names) == 0 {
			continue
		}
		b.warnings = append(b.warnings, "检测到 "+workflow.name+" 由版本 Tag 触发并负责 "+strings.Join(names, "、")+"；不会在本机重复构建")
	}
}

func targetHasReleaseWork(target Target) bool {
	return strings.TrimSpace(target.Steps.Build) != "" ||
		strings.TrimSpace(target.Steps.Package) != "" ||
		strings.TrimSpace(target.Steps.Publish) != "" ||
		strings.TrimSpace(target.Steps.Deploy) != ""
}

func discoverTagWorkflows(root string) []tagWorkflow {
	workflowDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		return nil
	}
	out := []tagWorkflow{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yml" && ext != ".yaml" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(workflowDir, entry.Name()))
		if readErr != nil {
			continue
		}
		patterns := parsePushTagPatterns(string(raw))
		if len(patterns) == 0 {
			continue
		}
		content := strings.ToLower(string(raw))
		container := strings.Contains(content, "docker/build-push-action@") || strings.Contains(content, "docker build ")
		uploadsArtifact := strings.Contains(content, "actions/upload-artifact@") || strings.Contains(content, "actions/upload-pages-artifact@")
		publishesWeb := strings.Contains(content, "actions/deploy-pages@") || strings.Contains(content, "vercel") || strings.Contains(content, "netlify") || container
		publishesServer := container || strings.Contains(content, "kubectl ") || strings.Contains(content, "helm ") || strings.Contains(content, "serverless deploy")
		desktop := desktopBuildRE.MatchString(content)
		out = append(out, tagWorkflow{
			name: entry.Name(), tagPatterns: patterns,
			signals: workflowSignals{
				container: container,
				web:       webBuildRE.MatchString(content) && (uploadsArtifact || publishesWeb),
				server:    serverBuildRE.MatchString(content) && (uploadsArtifact || publishesServer),
				windows:   desktop && windowsRunnerRE.MatchString(content),
				macOS:     desktop && macRunnerRE.MatchString(content),
				android:   androidBuildRE.MatchString(content),
			},
		})
	}
	return out
}

func (w tagWorkflow) matchesPrefix(prefix string) bool {
	candidate := strings.TrimSpace(prefix) + "/v0.0.0"
	matched := false
	for _, raw := range w.tagPatterns {
		pattern := strings.TrimSpace(raw)
		negative := strings.HasPrefix(pattern, "!")
		if negative {
			pattern = strings.TrimPrefix(pattern, "!")
		}
		ok, err := path.Match(pattern, candidate)
		if err != nil || !ok {
			continue
		}
		matched = !negative
	}
	return matched
}

func (w tagWorkflow) supports(target Target, prefix string) bool {
	kind := strings.ToLower(strings.TrimSpace(target.Kind))
	switch kind {
	case "web":
		// A container workflow can own both halves of a bundled Web/server
		// release, but only when the tag namespace explicitly says Web.
		return w.signals.web || (w.signals.container && prefixHasToken(prefix, "web"))
	case "server", "service", "backend", "docker":
		return w.signals.container || w.signals.server
	case "android":
		return w.signals.android
	case "windows", "pc":
		return w.signals.windows
	case "mac", "macos", "darwin":
		return w.signals.macOS
	case "desktop":
		platform := desktopTargetPlatform(target)
		if platform == "windows" {
			return w.signals.windows
		}
		if platform == "macos" {
			return w.signals.macOS
		}
	}
	return false
}

func desktopTargetPlatform(target Target) string {
	for _, value := range target.Runner.OS {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "windows":
			return "windows"
		case "darwin", "mac", "macos":
			return "macos"
		}
	}
	clue := strings.ToLower(target.ID + " " + target.Name)
	if strings.Contains(clue, "windows") || strings.Contains(clue, " win") {
		return "windows"
	}
	if strings.Contains(clue, "macos") || strings.Contains(clue, " mac") {
		return "macos"
	}
	return ""
}

func prefixHasToken(prefix, wanted string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(prefix), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/'
	}) {
		if token == wanted {
			return true
		}
	}
	return false
}

// parsePushTagPatterns intentionally accepts only the ordinary block form
// used by GitHub's generated examples. Expressions, anchors and arbitrary
// inline maps remain unrecognized so they cannot silently disable local work.
func parsePushTagPatterns(document string) []string {
	lines := strings.Split(strings.ReplaceAll(document, "\r\n", "\n"), "\n")
	onIndent, pushIndent, tagsIndent := -1, -1, -1
	patterns := []string{}
	for _, original := range lines {
		line := stripYAMLComment(original)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := leadingSpaces(line)
		if onIndent < 0 {
			key, value, ok := yamlKeyValue(trimmed)
			if ok && key == "on" && value == "" {
				onIndent = indent
			}
			continue
		}
		if indent <= onIndent {
			break
		}
		if pushIndent < 0 {
			key, value, ok := yamlKeyValue(trimmed)
			if ok && key == "push" && value == "" {
				pushIndent = indent
			}
			continue
		}
		if indent <= pushIndent {
			break
		}
		if tagsIndent < 0 {
			key, value, ok := yamlKeyValue(trimmed)
			if !ok || key != "tags" {
				continue
			}
			tagsIndent = indent
			if value != "" {
				patterns = append(patterns, parseYAMLInlineList(value)...)
			}
			continue
		}
		if indent <= tagsIndent {
			break
		}
		if strings.HasPrefix(trimmed, "-") {
			if value := unquoteYAMLScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))); value != "" {
				patterns = append(patterns, value)
			}
		}
	}
	return patterns
}

func yamlKeyValue(line string) (string, string, bool) {
	index := strings.IndexByte(line, ':')
	if index < 0 {
		return "", "", false
	}
	key := unquoteYAMLScalar(strings.TrimSpace(line[:index]))
	return strings.ToLower(key), strings.TrimSpace(line[index+1:]), true
}

func parseYAMLInlineList(value string) []string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil
	}
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if value == "" {
		return nil
	}
	out := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = unquoteYAMLScalar(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func unquoteYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if decoded, err := strconv.Unquote(value); err == nil {
			return decoded
		}
	}
	return value
}

func stripYAMLComment(line string) string {
	single, double, escaped := false, false, false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if double && r == '\\' {
			escaped = true
			continue
		}
		if !double && r == '\'' {
			single = !single
			continue
		}
		if !single && r == '"' {
			double = !double
			continue
		}
		if r == '#' && !single && !double {
			return line[:i]
		}
	}
	return line
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}
