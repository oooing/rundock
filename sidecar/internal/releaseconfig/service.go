package releaseconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

type Service struct {
	store *store.Store
	mu    sync.Mutex
}

func New(st *store.Store) *Service { return &Service{store: st} }

// Get returns a saved manifest, or a non-persisted detected proposal when the
// project has not been configured yet.
func (s *Service) Get(ctx context.Context, appID string) (*Config, error) {
	_, root, repoFound, err := s.project(ctx, appID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, filepath.FromSlash(ManifestPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.scanRoot(root, repoFound), nil
		}
		return nil, &Error{Code: "config_read_failed", Message: "无法读取发布配置：" + err.Error()}
	}
	cfg, err := decode(raw)
	if err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	if err := validate(cfg); err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	addSiblingCargoLocks(cfg, root)
	if err := validate(cfg); err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	decorate(cfg, SourceFile, root, 1, nil)
	return cfg, nil
}

// Scan always performs fresh discovery and never writes into the project.
func (s *Service) Scan(ctx context.Context, appID string) (*Config, error) {
	_, root, repoFound, err := s.project(ctx, appID)
	if err != nil {
		return nil, err
	}
	return s.scanRoot(root, repoFound), nil
}

// Put validates and atomically stores a project-local manifest. The on-disk
// document is JSON, which is a portable YAML 1.2 subset.
func (s *Service) Put(ctx context.Context, appID string, cfg *Config) (*Config, error) {
	_, root, _, err := s.project(ctx, appID)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, &Error{Code: "config_invalid", Message: "发布配置不能为空"}
	}
	manifest := cloneForManifest(cfg)
	if manifest.Automation != nil {
		manifest.Automation.Provider = strings.TrimSpace(manifest.Automation.Provider)
		manifest.Automation.Workflow = strings.TrimSpace(manifest.Automation.Workflow)
		manifest.Automation.Trigger = strings.TrimSpace(manifest.Automation.Trigger)
		manifest.Automation.ReleaseBranch = strings.TrimSpace(manifest.Automation.ReleaseBranch)
	}
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = SchemaVersion
	}
	if err := validate(manifest); err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	addSiblingCargoLocks(manifest, root)
	if err := validate(manifest); err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	// Detection metadata belongs to the local UI response, not to the portable
	// repository manifest.
	document := struct {
		SchemaVersion int            `json:"schemaVersion"`
		VersionGroups []VersionGroup `json:"versionGroups"`
		Targets       []Target       `json:"targets"`
		Automation    *Automation    `json:"automation,omitempty"`
	}{manifest.SchemaVersion, manifest.VersionGroups, manifest.Targets, manifest.Automation}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(root, ".launcher")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &Error{Code: "config_write_failed", Message: "无法创建发布配置目录：" + err.Error()}
	}
	tmp, err := os.CreateTemp(dir, "release-*.tmp")
	if err != nil {
		return nil, &Error{Code: "config_write_failed", Message: "无法创建临时配置文件：" + err.Error()}
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return nil, &Error{Code: "config_write_failed", Message: "无法设置配置文件权限：" + err.Error()}
	}
	if _, err := tmp.Write(raw); err != nil {
		return nil, &Error{Code: "config_write_failed", Message: "无法写入发布配置：" + err.Error()}
	}
	if err := tmp.Sync(); err != nil {
		return nil, &Error{Code: "config_write_failed", Message: "无法同步发布配置：" + err.Error()}
	}
	if err := tmp.Close(); err != nil {
		return nil, &Error{Code: "config_write_failed", Message: "无法关闭发布配置：" + err.Error()}
	}
	path := filepath.Join(root, filepath.FromSlash(ManifestPath))
	if err := os.Rename(tmpName, path); err != nil {
		return nil, &Error{Code: "config_write_failed", Message: "无法替换发布配置：" + err.Error()}
	}
	removeTemp = false
	decorate(manifest, SourceFile, root, 1, nil)
	return manifest, nil
}

func (s *Service) project(ctx context.Context, appID string) (*store.App, string, bool, error) {
	a, err := s.store.GetApp(appID)
	if err != nil {
		return nil, "", false, err
	}
	if a == nil {
		return nil, "", false, &Error{Code: "app_not_found", Message: "项目不存在"}
	}
	cwd := strings.TrimSpace(a.Cwd)
	if cwd == "" {
		cwd = filepath.Dir(a.EntryScript)
	}
	root, repoFound, err := resolveRoot(ctx, cwd)
	if err != nil {
		return nil, "", false, &Error{Code: "project_path_invalid", Message: err.Error()}
	}
	return a, root, repoFound, nil
}

func resolveRoot(ctx context.Context, cwd string) (string, bool, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", false, fmt.Errorf("项目目录无效：%w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", false, fmt.Errorf("项目目录不存在：%s", abs)
	}
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "-C", abs, "rev-parse", "--show-toplevel")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, cmdErr := cmd.Output(); cmdErr == nil {
		root := filepath.Clean(strings.TrimSpace(string(out)))
		if stat, statErr := os.Stat(root); statErr == nil && stat.IsDir() {
			return root, true, nil
		}
	}
	for dir := filepath.Clean(abs); ; dir = filepath.Dir(dir) {
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			return dir, true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return filepath.Clean(abs), false, nil
}

func decode(raw []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	cfg := &Config{}
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("release.yaml 必须使用 JSON 兼容的 YAML 格式：%w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("release.yaml 只能包含一个配置对象")
		}
		return nil, fmt.Errorf("release.yaml 末尾包含无效内容：%w", err)
	}
	return cfg, nil
}

func decorate(cfg *Config, source, root string, confidence float64, warnings []string) {
	cfg.Source = source
	cfg.RepoRoot = root
	cfg.ConfigPath = ManifestPath
	cfg.Confidence = roundConfidence(confidence)
	if warnings == nil {
		warnings = []string{}
	}
	cfg.Warnings = warnings
	if cfg.VersionGroups == nil {
		cfg.VersionGroups = []VersionGroup{}
	}
	if cfg.Targets == nil {
		cfg.Targets = []Target{}
	}
	ensureTagPrefixes(cfg)
}

func ensureTagPrefixes(cfg *Config) {
	used := map[string]bool{}
	for _, group := range cfg.VersionGroups {
		if prefix := strings.TrimSpace(group.TagPrefix); prefix != "" {
			used[strings.ToLower(prefix)] = true
		}
	}
	for i := range cfg.VersionGroups {
		if strings.TrimSpace(cfg.VersionGroups[i].TagPrefix) != "" {
			continue
		}
		categories := map[string]bool{}
		for _, target := range cfg.Targets {
			if !target.Enabled || target.VersionGroup != cfg.VersionGroups[i].ID {
				continue
			}
			kind := strings.ToLower(strings.TrimSpace(target.Kind))
			switch kind {
			case "web":
				categories["web"] = true
			case "desktop", "windows", "mac", "macos", "darwin":
				categories["desktop"] = true
			case "android":
				categories["android"] = true
			case "server", "service", "backend", "docker":
				categories["server"] = true
			}
		}
		ordered := []string{}
		for _, category := range []string{"web", "desktop", "android", "server"} {
			if categories[category] {
				ordered = append(ordered, category)
			}
		}
		prefix := strings.Join(ordered, "-")
		if prefix == "" {
			prefix = slug(cfg.VersionGroups[i].ID)
		}
		if prefix == "" {
			prefix = fmt.Sprintf("version-%d", i+1)
		}
		base := prefix
		for suffix := 2; used[strings.ToLower(prefix)]; suffix++ {
			prefix = fmt.Sprintf("%s-%d", base, suffix)
		}
		used[strings.ToLower(prefix)] = true
		cfg.VersionGroups[i].TagPrefix = prefix
	}
}

func cloneForManifest(cfg *Config) *Config {
	raw, _ := json.Marshal(cfg)
	out := &Config{}
	_ = json.Unmarshal(raw, out)
	out.Source = ""
	out.RepoRoot = ""
	out.ConfigPath = ""
	out.Confidence = 0
	out.Warnings = []string{}
	if out.VersionGroups == nil {
		out.VersionGroups = []VersionGroup{}
	}
	if out.Targets == nil {
		out.Targets = []Target{}
	}
	for i := range out.VersionGroups {
		if out.VersionGroups[i].VersionFiles == nil {
			out.VersionGroups[i].VersionFiles = []VersionFile{}
		}
	}
	for i := range out.Targets {
		if out.Targets[i].Runner.OS == nil {
			out.Targets[i].Runner.OS = []string{}
		}
		if out.Targets[i].Artifacts == nil {
			out.Targets[i].Artifacts = []string{}
		}
	}
	return out
}

// addSiblingCargoLocks upgrades older saved Tauri configurations at runtime.
// Cargo.lock remains optional for non-locked projects, but when it exists next
// to a configured Cargo.toml it must advance in the same version group.
func addSiblingCargoLocks(cfg *Config, root string) {
	owners := map[string]string{}
	for _, group := range cfg.VersionGroups {
		for _, file := range group.VersionFiles {
			key := strings.ToLower(filepath.ToSlash(filepath.Clean(filepath.FromSlash(file.Path))))
			owners[key] = group.ID
		}
	}
	for i := range cfg.VersionGroups {
		group := &cfg.VersionGroups[i]
		additions := []VersionFile{}
		for _, file := range group.VersionFiles {
			if !strings.EqualFold(strings.TrimSpace(file.Format), "cargo") || !strings.EqualFold(filepath.Base(file.Path), "Cargo.toml") {
				continue
			}
			manifestPath := filepath.Clean(filepath.FromSlash(file.Path))
			lockPath := filepath.Join(filepath.Dir(manifestPath), "Cargo.lock")
			lockRelative := filepath.ToSlash(lockPath)
			key := strings.ToLower(lockRelative)
			if _, exists := owners[key]; exists || !fileExists(filepath.Join(root, lockPath)) {
				continue
			}
			owners[key] = group.ID
			additions = append(additions, VersionFile{Path: lockRelative, Format: "cargo-lock"})
		}
		group.VersionFiles = mergeVersionFiles(group.VersionFiles, additions)
	}
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
		if strings.TrimSpace(automation.Trigger) != AutomationTriggerTag {
			return fmt.Errorf("automation.trigger 仅支持 %s", AutomationTriggerTag)
		}
		if !validReleaseBranch(automation.ReleaseBranch) {
			return errors.New("automation.releaseBranch 不是有效的 Git 分支名")
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

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func roundConfidence(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func safeWalk(root string, visit func(path string, entry fs.DirEntry) error) []string {
	warnings := []string{}
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
	visited := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			warnings = append(warnings, "无法扫描 "+relative(root, path)+"："+err.Error())
			return nil
		}
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if path != root && (name == ".git" || name == ".launcher" || name == "node_modules" || name == "target" || name == "dist" || name == "build" || name == "vendor" || name == ".next") {
				return filepath.SkipDir
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > 4 {
				return filepath.SkipDir
			}
		}
		visited++
		if visited > 10000 {
			return errors.New("scan limit reached")
		}
		return visit(path, entry)
	})
	if err != nil {
		warnings = append(warnings, "项目较大，自动扫描已提前停止，请检查识别结果")
	}
	return sortedUnique(warnings)
}
