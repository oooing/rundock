package releaseconfig

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func validate(cfg *Config) error {
	if cfg.SchemaVersion != SchemaVersion {
		return fmt.Errorf("不支持的 schemaVersion：%d", cfg.SchemaVersion)
	}
	groups := make(map[string]struct{}, len(cfg.VersionGroups))
	tagPrefixes := map[string]string{}
	versionFiles := map[string]string{}
	for i, group := range cfg.VersionGroups {
		if !idPattern.MatchString(group.ID) {
			return fmt.Errorf("versionGroups[%d].id 无效", i)
		}
		if strings.TrimSpace(group.Name) == "" {
			return fmt.Errorf("versionGroups[%d].name 不能为空", i)
		}
		if group.TagPrefix != "" && !idPattern.MatchString(group.TagPrefix) {
			return fmt.Errorf("versionGroups[%d].tagPrefix 无效", i)
		}
		if group.TagPrefix != "" {
			key := strings.ToLower(group.TagPrefix)
			if owner, exists := tagPrefixes[key]; exists {
				return fmt.Errorf("Tag 前缀重复：%s（版本组 %s 和 %s）", group.TagPrefix, owner, group.ID)
			}
			tagPrefixes[key] = group.ID
		}
		if _, exists := groups[group.ID]; exists {
			return fmt.Errorf("版本组 id 重复：%s", group.ID)
		}
		groups[group.ID] = struct{}{}
		cargoManifests := map[string]bool{}
		for _, file := range group.VersionFiles {
			if strings.EqualFold(strings.TrimSpace(file.Format), "cargo") && strings.EqualFold(filepath.Base(filepath.FromSlash(file.Path)), "Cargo.toml") {
				key := strings.ToLower(filepath.ToSlash(filepath.Clean(filepath.FromSlash(file.Path))))
				cargoManifests[key] = true
			}
		}
		for j, file := range group.VersionFiles {
			if strings.TrimSpace(file.Path) == "" {
				return fmt.Errorf("versionGroups[%d].versionFiles[%d].path 不能为空", i, j)
			}
			if err := validateRelative(file.Path); err != nil {
				return fmt.Errorf("versionGroups[%d].versionFiles[%d].path：%w", i, j, err)
			}
			key := strings.ToLower(filepath.ToSlash(filepath.Clean(file.Path)))
			if owner, exists := versionFiles[key]; exists && owner != group.ID {
				return fmt.Errorf("版本文件 %s 不能同时属于版本组 %s 和 %s", file.Path, owner, group.ID)
			}
			versionFiles[key] = group.ID
			format := strings.ToLower(strings.TrimSpace(file.Format))
			if format != "json" && format != "npm-lock" && format != "cargo" && format != "cargo-lock" && format != "toml" && format != "gradle" {
				return fmt.Errorf("versionGroups[%d].versionFiles[%d].format 仅支持 json、npm-lock、cargo、cargo-lock、toml 或 gradle", i, j)
			}
			if format == "json" && file.JSONPointer != "" && file.JSONPointer != "/version" && file.JSONPointer != "/package/version" {
				return fmt.Errorf("versionGroups[%d].versionFiles[%d].jsonPointer 仅支持 /version 或 /package/version", i, j)
			}
			if format == "cargo-lock" {
				lockPath := filepath.Clean(filepath.FromSlash(file.Path))
				manifestPath := filepath.Join(filepath.Dir(lockPath), "Cargo.toml")
				key := strings.ToLower(filepath.ToSlash(manifestPath))
				if !cargoManifests[key] {
					return fmt.Errorf("versionGroups[%d].versionFiles[%d] 的 cargo-lock 必须与同目录 Cargo.toml（cargo 格式）位于同一版本组", i, j)
				}
			}
		}
	}
	targets := make(map[string]struct{}, len(cfg.Targets))
	for i, target := range cfg.Targets {
		if err := validateDelivery(target); err != nil {
			return err
		}
		if !idPattern.MatchString(target.ID) {
			return fmt.Errorf("targets[%d].id 无效", i)
		}
		if _, exists := targets[target.ID]; exists {
			return fmt.Errorf("发布目标 id 重复：%s", target.ID)
		}
		targets[target.ID] = struct{}{}
		if strings.TrimSpace(target.Name) == "" || strings.TrimSpace(target.Kind) == "" {
			return fmt.Errorf("targets[%d] 的 name 和 kind 不能为空", i)
		}
		if _, exists := groups[target.VersionGroup]; !exists {
			return fmt.Errorf("targets[%d] 引用了不存在的版本组：%s", i, target.VersionGroup)
		}
		if err := validateRelative(target.WorkingDir); err != nil {
			return fmt.Errorf("targets[%d].workingDir：%w", i, err)
		}
		if strings.TrimSpace(target.Runner.Type) == "" {
			return fmt.Errorf("targets[%d].runner.type 不能为空", i)
		}
		if target.Confidence < 0 || target.Confidence > 1 {
			return fmt.Errorf("targets[%d].confidence 必须在 0 到 1 之间", i)
		}
		for j, pattern := range target.Artifacts {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("targets[%d].artifacts[%d] 不能为空", i, j)
			}
			if err := validateRelative(pattern); err != nil {
				return fmt.Errorf("targets[%d].artifacts[%d]：%w", i, j, err)
			}
		}
	}
	if cfg.Automation != nil {
		automation := cfg.Automation
		if strings.TrimSpace(automation.Provider) != AutomationGitHubActions {
			return fmt.Errorf("automation.provider 仅支持 %s", AutomationGitHubActions)
		}
		workflow := strings.TrimSpace(automation.Workflow)
		if workflow == "" || filepath.Base(workflow) != workflow || (filepath.Ext(workflow) != ".yml" && filepath.Ext(workflow) != ".yaml") {
			return errors.New("automation.workflow 必须是 .yml 或 .yaml 工作流文件名")
		}
		if strings.TrimSpace(automation.Trigger) != AutomationTriggerTag && strings.TrimSpace(automation.Trigger) != "dispatch" {
			return fmt.Errorf("automation.trigger 仅支持 %s", AutomationTriggerTag)
		}
		if automation.Trigger == "dispatch" && !deliveryAccountPattern.MatchString(automation.Account) { return errors.New("显式调用工作流需要配置 GitHub 登录账号") }
		if !validReleaseBranch(automation.ReleaseBranch) {
			return errors.New("automation.releaseBranch 不是有效的 Git 分支名")
		}
	}
	rules := make(map[string]struct{}, len(cfg.FileRules))
	for i, rule := range cfg.FileRules {
		if !idPattern.MatchString(rule.ID) {
			return fmt.Errorf("fileRules[%d].id 无效", i)
		}
		if _, exists := rules[rule.ID]; exists {
			return fmt.Errorf("文件规则 id 重复：%s", rule.ID)
		}
		rules[rule.ID] = struct{}{}
		if strings.TrimSpace(rule.Pattern) == "" {
			return fmt.Errorf("fileRules[%d].pattern 不能为空", i)
		}
		if err := validateRelative(strings.TrimSuffix(strings.ReplaceAll(rule.Pattern, "*", "x"), "/")); err != nil {
			return fmt.Errorf("fileRules[%d].pattern：%w", i, err)
		}
		switch strings.ToLower(strings.TrimSpace(rule.Kind)) {
		case RuleRecommend, RuleLocal, RuleReview, RuleSensitive:
		default:
			return fmt.Errorf("fileRules[%d].kind 仅支持 recommend、local、review 或 sensitive", i)
		}
	}
	profiles := make(map[string]struct{}, len(cfg.CheckProfiles))
	for i, profile := range cfg.CheckProfiles {
		if !idPattern.MatchString(profile.ID) {
			return fmt.Errorf("checkProfiles[%d].id 无效", i)
		}
		if _, exists := profiles[profile.ID]; exists {
			return fmt.Errorf("检查配置 id 重复：%s", profile.ID)
		}
		profiles[profile.ID] = struct{}{}
		if strings.TrimSpace(profile.Name) == "" {
			return fmt.Errorf("checkProfiles[%d].name 不能为空", i)
		}
		if strings.TrimSpace(profile.Command) == "" {
			return fmt.Errorf("checkProfiles[%d].command 不能为空", i)
		}
		if strings.TrimSpace(profile.WorkingDir) != "" {
			if err := validateRelative(profile.WorkingDir); err != nil {
				return fmt.Errorf("checkProfiles[%d].workingDir：%w", i, err)
			}
		}
		if profile.TimeoutSeconds < 0 {
			return fmt.Errorf("checkProfiles[%d].timeoutSeconds 不能为负数", i)
		}
	}
	return nil
}

func validReleaseBranch(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".lock") {
		return false
	}
	if strings.Contains(value, "..") || strings.Contains(value, "//") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validateRelative(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return nil
	}
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return errors.New("必须是项目内的相对路径")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("不能跳出项目目录")
	}
	return nil
}
