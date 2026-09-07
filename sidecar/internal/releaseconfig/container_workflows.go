package releaseconfig

import (
	"path"
	"regexp"
	"strings"
)

var nodeVersionSourceRE = regexp.MustCompile(`require\(\s*['"]([^'"\r\n]+package\.json)['"]\s*\)\s*\.version\b`)

// Only literal, repository-relative version reads are evidence of ownership.
// Equal version numbers or package discovery order are not such evidence.
func workflowNodeVersionSources(document string) []string {
	lines := strings.Split(document, "\n")
	for i := range lines {
		lines[i] = stripYAMLComment(lines[i])
	}
	document = strings.Join(lines, "\n")
	result := []string{}
	for _, match := range nodeVersionSourceRE.FindAllStringSubmatch(document, -1) {
		if value := literalWorkflowPath(match[1]); value != "" {
			result = append(result, value)
		}
	}
	return sortedUnique(result)
}

func literalWorkflowPath(value string) string {
	value = strings.TrimSpace(unquoteYAMLScalar(stripYAMLComment(value)))
	if value == "" || strings.ContainsAny(value, "$*{}\\:") || strings.HasPrefix(value, "/") {
		return ""
	}
	value = path.Clean(value)
	if value == ".." || strings.HasPrefix(value, "../") {
		return ""
	}
	return value
}

func workflowDockerfiles(document string) []string {
	files, contexts := []string{}, []string{}
	uncertain := false
	for _, line := range strings.Split(document, "\n") {
		key, value, ok := yamlKeyValue(strings.TrimSpace(stripYAMLComment(line)))
		if !ok || (key != "file" && key != "context") {
			continue
		}
		resolved := literalWorkflowPath(value)
		if resolved == "" {
			uncertain = true
			continue
		}
		if key == "file" && path.Base(resolved) == "Dockerfile" {
			files = append(files, resolved)
		} else if key == "context" {
			contexts = append(contexts, resolved)
		}
	}
	if len(files) > 0 {
		return sortedUnique(files)
	}
	if uncertain {
		return nil
	}
	if len(contexts) == 0 {
		return []string{"Dockerfile"}
	}
	for _, context := range contexts {
		files = append(files, path.Join(context, "Dockerfile"))
	}
	return sortedUnique(files)
}

func (w tagWorkflow) literalPrefix() string {
	prefix := ""
	for _, pattern := range w.tagPatterns {
		if !strings.HasSuffix(pattern, "/v*") {
			return ""
		}
		candidate := strings.TrimSuffix(pattern, "/v*")
		if !idPattern.MatchString(candidate) || (prefix != "" && prefix != candidate) {
			return ""
		}
		prefix = candidate
	}
	return prefix
}

func (b *discoveryBuilder) bindContainerWorkflowVersions(workflows []tagWorkflow) {
	for i := range workflows {
		workflow := &workflows[i]
		if !workflow.signals.container {
			continue
		}
		containerIndexes := []int{}
		for j, target := range b.config.Targets {
			if target.Runner.Type != RunnerGitPush || !strings.HasSuffix(target.ID, "cloud-container") {
				continue
			}
			for _, file := range workflow.dockerfiles {
				if path.Clean(target.WorkingDir) == path.Dir(file) {
					containerIndexes = append(containerIndexes, j)
					break
				}
			}
		}
		prefix := workflow.literalPrefix()
		groupID := ""
		for _, source := range workflow.versionSources {
			owner := ""
			for _, group := range b.config.VersionGroups {
				for _, file := range group.VersionFiles {
					if file.Path == source && file.Format == "json" && file.JSONPointer == "/version" {
						if owner != "" && owner != group.ID {
							owner = "?"
							break
						}
						owner = group.ID
					}
				}
			}
			if owner == "" || owner == "?" || (groupID != "" && owner != groupID) {
				groupID = "?"
				break
			}
			groupID = owner
		}
		if groupID != "" && groupID != "?" && prefix != "" {
			for _, group := range b.config.VersionGroups {
				if group.ID != groupID && group.TagPrefix == prefix {
					groupID = "?"
					break
				}
			}
			for _, other := range workflows {
				if other.name == workflow.name || !other.signals.container || other.literalPrefix() == prefix {
					continue
				}
				for _, otherSource := range other.versionSources {
					for _, source := range workflow.versionSources {
						if otherSource == source {
							groupID = "?"
						}
					}
				}
			}
		}
		if prefix == "" || groupID == "" || groupID == "?" || len(containerIndexes) == 0 {
			for _, index := range containerIndexes {
				target := &b.config.Targets[index]
				target.Enabled = false
				target.Steps = Steps{Publish: "tag-push"}
			}
			b.warnings = append(b.warnings, workflow.name+" 由版本 Tag 触发，但无法唯一确认 Tag 前缀、版本文件与 Dockerfile 的对应关系；云端容器目标不会自动启用，请在高级配置中确认")
			continue
		}
		workflow.versionGroup = groupID
		for j := range b.config.VersionGroups {
			if b.config.VersionGroups[j].ID == groupID {
				b.config.VersionGroups[j].TagPrefix = prefix
			}
		}
		for _, index := range containerIndexes {
			container := &b.config.Targets[index]
			container.VersionGroup = groupID
			container.Enabled = true
			container.Steps = Steps{Publish: "tag-push"}
			// Check-only backend helpers covered by this container describe the
			// same delivered image; they must not borrow an unrelated extension's
			// package version merely because it was discovered first.
			for j := range b.config.Targets {
				target := &b.config.Targets[j]
				rel := path.Clean(target.WorkingDir)
				base := path.Clean(container.WorkingDir)
				covered := base == "." || rel == base || strings.HasPrefix(rel, base+"/")
				if target.Kind == "server" && !targetHasReleaseWork(*target) && covered {
					target.VersionGroup = groupID
				}
			}
		}
		b.warnings = append(b.warnings, workflow.name+" 使用 "+strings.Join(workflow.versionSources, "、")+" 的版本并监听 "+prefix+"/v*；Web 与容器共用该版本组")
	}
}

// A push event filtered only by tags does not run for branch pushes. Accept
// ordinary literal YAML event forms and leave expressions/inline maps unknown.
func hasBranchPushEvent(document string) bool {
	onIndent, pushIndent := -1, -1
	sawBranches, sawTags := false, false
	for _, original := range strings.Split(document, "\n") {
		line := stripYAMLComment(original)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := leadingSpaces(line)
		key, value, ok := yamlKeyValue(trimmed)
		if onIndent < 0 {
			if ok && key == "on" {
				if unquoteYAMLScalar(value) == "push" {
					return true
				}
				for _, event := range parseYAMLInlineList(value) {
					if event == "push" {
						return true
					}
				}
				if value == "" {
					onIndent = indent
				}
			}
			continue
		}
		if indent <= onIndent {
			break
		}
		if pushIndent < 0 {
			if ok && key == "push" && value == "" {
				pushIndent = indent
			}
			continue
		}
		if indent <= pushIndent {
			break
		}
		if ok && (key == "branches" || key == "branches-ignore") {
			sawBranches = true
		}
		if ok && (key == "tags" || key == "tags-ignore") {
			sawTags = true
		}
	}
	return pushIndent >= 0 && (sawBranches || !sawTags)
}
