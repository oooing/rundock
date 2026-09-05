package releaseconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type nodeProject struct {
	Dir          string
	Name         string
	Version      string
	Scripts      map[string]string
	Dependencies map[string]string
	Manager      string
}

type discoveryBuilder struct {
	root          string
	config        *Config
	groupByKey    map[string]string
	expoGroups    map[string]string
	groupIDs      map[string]bool
	targetIDs     map[string]bool
	dockerDirs    []string
	gitPushDocker bool
	warnings      []string
}

func (s *Service) scanRoot(root string, repoFound bool) *Config {
	b := &discoveryBuilder{
		root: root,
		config: &Config{
			SchemaVersion: SchemaVersion,
			VersionGroups: []VersionGroup{},
			Targets:       []Target{},
		},
		groupByKey: map[string]string{}, expoGroups: map[string]string{}, groupIDs: map[string]bool{}, targetIDs: map[string]bool{},
	}
	if !repoFound {
		b.warnings = append(b.warnings, "未检测到 Git 仓库，发布配置将以当前项目目录为根目录")
	}

	var packageFiles, gradleWrappers, dockerfiles, goMods, pythonFiles []string
	b.warnings = append(b.warnings, safeWalk(root, func(path string, entry os.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		switch strings.ToLower(entry.Name()) {
		case "package.json":
			packageFiles = append(packageFiles, path)
		case "gradlew", "gradlew.bat":
			gradleWrappers = append(gradleWrappers, path)
		case "dockerfile":
			dockerfiles = append(dockerfiles, path)
		case "go.mod":
			goMods = append(goMods, path)
		case "pyproject.toml", "requirements.txt":
			pythonFiles = append(pythonFiles, path)
		}
		return nil
	})...)

	packageFiles = sortedUnique(packageFiles)
	for _, packagePath := range packageFiles {
		project, err := readNodeProject(packagePath)
		if err != nil {
			b.warnings = append(b.warnings, "无法解析 "+relative(root, packagePath)+"："+err.Error())
			continue
		}
		b.discoverNode(project)
	}
	for _, wrapper := range uniqueDirs(gradleWrappers) {
		b.discoverAndroid(wrapper)
	}
	b.dockerDirs = uniqueDirs(dockerfiles)
	b.gitPushDocker = hasPushTriggeredDockerWorkflow(root)
	for _, dockerDir := range b.dockerDirs {
		b.discoverDocker(dockerDir)
	}
	for _, goMod := range goMods {
		b.discoverGo(filepath.Dir(goMod))
	}
	for _, pyDir := range uniqueDirs(pythonFiles) {
		b.discoverPython(pyDir)
	}
	b.applyTargetPriorities()
	// Tag prefixes are normally added while decorating the response.  Resolve
	// them one step earlier as well so a repository's existing tag-triggered
	// workflow can be matched to the exact version group it will receive.
	ensureTagPrefixes(b.config)
	b.applyTagTriggeredWorkflowRunners()

	if len(b.config.VersionGroups) == 0 {
		b.ensureGroup("default", "", nil)
	}
	if len(b.config.Targets) == 0 {
		b.config.Targets = append(b.config.Targets, Target{
			ID: "custom", Name: "自定义发布目标", Kind: "custom", VersionGroup: b.config.VersionGroups[0].ID,
			WorkingDir: ".", Runner: Runner{Type: "local", OS: []string{"windows", "linux", "darwin"}},
			Enabled: false, Detected: true, Confidence: 0.2, Steps: Steps{}, Artifacts: []string{},
		})
		b.warnings = append(b.warnings, "未识别出常见构建系统，请在配置界面中补充构建命令和产物路径")
	}
	sort.SliceStable(b.config.VersionGroups, func(i, j int) bool { return b.config.VersionGroups[i].ID < b.config.VersionGroups[j].ID })
	sort.SliceStable(b.config.Targets, func(i, j int) bool { return b.config.Targets[i].ID < b.config.Targets[j].ID })
	confidence := 0.0
	for _, target := range b.config.Targets {
		confidence += target.Confidence
	}
	confidence /= float64(len(b.config.Targets))
	b.warnings = append(b.warnings, "自动识别结果不会直接执行；首次发布前请确认目标、命令和产物路径")
	decorate(b.config, SourceDetected, root, confidence, sortedUnique(b.warnings))
	return b.config
}

func readNodeProject(packagePath string) (nodeProject, error) {
	raw, err := os.ReadFile(packagePath)
	if err != nil {
		return nodeProject{}, err
	}
	var manifest struct {
		Name            string            `json:"name"`
		Version         string            `json:"version"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nodeProject{}, err
	}
	if manifest.Scripts == nil {
		manifest.Scripts = map[string]string{}
	}
	deps := map[string]string{}
	for key, value := range manifest.Dependencies {
		deps[strings.ToLower(key)] = value
	}
	for key, value := range manifest.DevDependencies {
		deps[strings.ToLower(key)] = value
	}
	dir := filepath.Dir(packagePath)
	manager := "npm"
	if fileExists(filepath.Join(dir, "pnpm-lock.yaml")) {
		manager = "pnpm"
	} else if fileExists(filepath.Join(dir, "yarn.lock")) {
		manager = "yarn"
	} else if fileExists(filepath.Join(dir, "bun.lockb")) || fileExists(filepath.Join(dir, "bun.lock")) {
		manager = "bun"
	}
	return nodeProject{Dir: dir, Name: manifest.Name, Version: manifest.Version, Scripts: manifest.Scripts, Dependencies: deps, Manager: manager}, nil
}

func (b *discoveryBuilder) discoverNode(project nodeProject) {
	rel := relative(b.root, project.Dir)
	name := strings.TrimSpace(project.Name)
	if name == "" {
		name = filepath.Base(project.Dir)
	}
	packageFiles := []VersionFile{{Path: joinRelative(rel, "package.json"), Format: "json", JSONPointer: "/version"}}
	if fileExists(filepath.Join(project.Dir, "package-lock.json")) {
		packageFiles = append(packageFiles, VersionFile{Path: joinRelative(rel, "package-lock.json"), Format: "npm-lock"})
	}
	nodeGroup := b.ensureGroup("node:"+rel, project.Version, packageFiles)
	if hasAnyDependency(project.Dependencies, "expo") {
		b.expoGroups[filepath.Clean(project.Dir)] = nodeGroup
	}
	check := scriptCommand(project, "test")
	build := scriptCommand(project, "build")

	isTauri := hasAnyDependency(project.Dependencies, "@tauri-apps/api", "@tauri-apps/cli") || fileExists(filepath.Join(project.Dir, "src-tauri", "tauri.conf.json")) || fileExists(filepath.Join(project.Dir, "src-tauri", "tauri.conf.json5"))
	isWeb := hasAnyDependency(project.Dependencies, "vite", "next", "react-scripts", "@angular/core", "nuxt") || hasAnyFile(project.Dir, "vite.config.ts", "vite.config.js", "vite.config.mts", "next.config.js", "next.config.mjs")
	isServer := looksLikeServer(project)

	if isWeb && build != "" {
		artifact := "dist/**"
		if _, ok := project.Dependencies["next"]; ok {
			artifact = ".next/**"
		} else if _, ok := project.Dependencies["react-scripts"]; ok {
			artifact = "build/**"
		}
		b.addTarget(Target{ID: b.targetID(rel, "web"), Name: name + " Web", Kind: "web", VersionGroup: nodeGroup,
			WorkingDir: rel, Runner: anyLocalRunner(), Enabled: true, Detected: true, Confidence: 0.94,
			Steps: Steps{Check: check, Build: build}, Artifacts: []string{artifact}})
	}

	if isTauri {
		version, files := detectTauriVersion(b.root, project)
		group := ""
		if version != "" && version == project.Version {
			group = nodeGroup
			b.mergeGroupFiles(group, files)
		} else {
			group = b.ensureGroup("tauri:"+rel, version, files)
		}
		tauriBuild := detectTauriBuild(project)
		b.addTarget(Target{ID: b.targetID(rel, "windows"), Name: name + " Windows", Kind: "desktop", VersionGroup: group,
			WorkingDir: rel, Runner: Runner{Type: "local", OS: []string{"windows"}}, Enabled: true, Detected: true, Confidence: 0.96,
			Steps: Steps{Check: check, Build: tauriBuild}, Artifacts: []string{"src-tauri/target/release/bundle/**/*.exe", "src-tauri/target/release/bundle/**/*.msi"}})
		b.addTarget(Target{ID: b.targetID(rel, "macos"), Name: name + " macOS", Kind: "desktop", VersionGroup: group,
			WorkingDir: rel, Runner: Runner{Type: "local", OS: []string{"darwin"}}, Enabled: false, Detected: true, Confidence: 0.92,
			Steps: Steps{Check: check, Build: tauriBuild}, Artifacts: []string{"src-tauri/target/release/bundle/**/*.dmg", "src-tauri/target/release/bundle/**/*.app/**"}})
	}

	if isServer {
		serverBuild := build
		b.addTarget(Target{ID: b.targetID(rel, "server"), Name: name + " 服务端", Kind: "server", VersionGroup: nodeGroup,
			WorkingDir: rel, Runner: anyLocalRunner(), Enabled: true, Detected: true, Confidence: 0.82,
			Steps: Steps{Check: check, Build: serverBuild}, Artifacts: nodeServerArtifacts(project)})
	}

	if !isWeb && !isTauri && !isServer {
		pack := packageCommand(project.Manager)
		b.addTarget(Target{ID: b.targetID(rel, "node"), Name: name + " Node", Kind: "node", VersionGroup: nodeGroup,
			WorkingDir: rel, Runner: anyLocalRunner(), Enabled: true, Detected: true, Confidence: 0.72,
			Steps: Steps{Check: check, Build: build, Package: pack}, Artifacts: []string{"*.tgz"}})
	}
}

func (b *discoveryBuilder) discoverAndroid(dir string) {
	rel := relative(b.root, dir)
	group, usesExpoSource := b.nearestExpoGroup(dir)
	if !usesExpoSource {
		version, file := detectGradleVersion(b.root, dir)
		files := []VersionFile{}
		if file.Path != "" {
			files = append(files, file)
		}
		group = b.ensureGroup("android:"+rel, version, files)
	} else {
		b.warnings = append(b.warnings, displayName(dir)+" Android 使用上级 Expo 项目的 package.json 作为版本来源，不修改自动生成的 android 目录")
	}
	b.addTarget(Target{ID: b.targetID(rel, "android"), Name: displayName(dir) + " Android", Kind: "android", VersionGroup: group,
		WorkingDir: rel, Runner: anyLocalRunner(), Enabled: true, Detected: true, Confidence: 0.9,
		Steps:     Steps{Check: "gradlew tasks", Build: "gradlew bundleRelease"},
		Artifacts: []string{"**/build/outputs/**/*.apk", "**/build/outputs/**/*.aab"}})
}

func (b *discoveryBuilder) discoverDocker(dir string) {
	rel := relative(b.root, dir)
	group := b.nearestGroup(rel)
	if b.gitPushDocker {
		b.addTarget(Target{ID: b.targetID(rel, "cloud-container"), Name: displayName(dir) + " 云端容器", Kind: "server", VersionGroup: group,
			WorkingDir: rel, Runner: Runner{Type: RunnerGitPush, OS: []string{}}, Enabled: true, Detected: true, Confidence: 0.94,
			Steps: Steps{Publish: "branch-push"}, Artifacts: []string{}})
		b.warnings = append(b.warnings, "检测到 GitHub Actions 容器构建；不会在本机运行 Docker")
		return
	}
	b.addTarget(Target{ID: b.targetID(rel, "docker"), Name: displayName(dir) + " Docker", Kind: "server", VersionGroup: group,
		WorkingDir: rel, Runner: anyLocalRunner(), Enabled: true, Detected: true, Confidence: 0.86,
		Steps: Steps{Check: "docker version", Build: "docker build ."}, Artifacts: []string{}})
}

var workflowPushRE = regexp.MustCompile(`(?m)^\s*push\s*:`)

func hasPushTriggeredDockerWorkflow(root string) bool {
	workflowDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		return false
	}
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
		content := strings.ToLower(string(raw))
		pushTriggered := workflowPushRE.MatchString(content) || strings.Contains(content, "on: [push")
		if pushTriggered && strings.Contains(content, "docker/build-push-action") {
			return true
		}
	}
	return false
}

func (b *discoveryBuilder) discoverGo(dir string) {
	rel := relative(b.root, dir)
	group := b.nearestGroup(rel)
	enabled, confidence := true, 0.76
	if dockerDir, covered := b.coveringDocker(dir); covered {
		enabled, confidence = false, 0.58
		b.warnings = append(b.warnings, displayName(dir)+" Go 服务已由 "+b.dockerLocation(dockerDir)+" 的 Docker 目标覆盖；原生构建目标已保留在高级配置中")
	}
	b.addTarget(Target{ID: b.targetID(rel, "go-server"), Name: displayName(dir) + " Go 服务", Kind: "server", VersionGroup: group,
		WorkingDir: rel, Runner: anyLocalRunner(), Enabled: enabled, Detected: true, Confidence: confidence,
		Steps: Steps{Check: "go test ./...", Build: "go build ./..."}, Artifacts: []string{}})
}

func (b *discoveryBuilder) discoverPython(dir string) {
	rel := relative(b.root, dir)
	group := b.nearestGroup(rel)
	enabled, confidence := true, 0.65
	if dockerDir, covered := b.coveringDocker(dir); covered {
		enabled, confidence = false, 0.48
		b.warnings = append(b.warnings, displayName(dir)+" Python 服务已由 "+b.dockerLocation(dockerDir)+" 的 Docker 目标覆盖；仅检查目标已保留在高级配置中")
	}
	b.addTarget(Target{ID: b.targetID(rel, "python-server"), Name: displayName(dir) + " Python 服务", Kind: "server", VersionGroup: group,
		WorkingDir: rel, Runner: anyLocalRunner(), Enabled: enabled, Detected: true, Confidence: confidence,
		Steps: Steps{Check: "python -m pytest"}, Artifacts: []string{}})
}

// applyTargetPriorities keeps the full discovery result for advanced editing,
// while ensuring auxiliary packages do not look like ordinary product release
// targets. Pure Node repositories are intentionally left unchanged.
func (b *discoveryBuilder) applyTargetPriorities() {
	hasProductPlatform := false
	for _, target := range b.config.Targets {
		if target.Kind == "web" || target.Kind == "desktop" || target.Kind == "android" {
			hasProductPlatform = true
			break
		}
	}
	if !hasProductPlatform {
		return
	}
	for i := range b.config.Targets {
		target := &b.config.Targets[i]
		if target.Kind != "node" || !looksAuxiliaryNodeDir(target.WorkingDir) {
			continue
		}
		target.Enabled = false
		if target.Confidence > 0.5 {
			target.Confidence = 0.5
		}
		b.warnings = append(b.warnings, target.Name+" 看起来是产品内部的 Node 模块；已保留在高级配置中但默认不发布")
	}
}

func (b *discoveryBuilder) coveringDocker(serviceDir string) (string, bool) {
	if b.gitPushDocker {
		return "", false
	}
	serviceDir = filepath.Clean(serviceDir)
	for _, dockerDir := range b.dockerDirs {
		rel, err := filepath.Rel(filepath.Clean(dockerDir), serviceDir)
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return dockerDir, true
		}
	}
	return "", false
}

func (b *discoveryBuilder) dockerLocation(dir string) string {
	location := relative(b.root, dir)
	if location == "." {
		return "仓库根目录"
	}
	return location
}

func looksAuxiliaryNodeDir(workingDir string) bool {
	workingDir = strings.Trim(filepath.ToSlash(workingDir), "/")
	if workingDir == "" || workingDir == "." {
		return false
	}
	parts := strings.Split(strings.ToLower(workingDir), "/")
	if len(parts) >= 2 {
		return true
	}
	switch parts[0] {
	case "modules", "module", "tools", "tooling", "scripts", "packages", "libs", "plugins", "examples":
		return true
	default:
		return false
	}
}

func (b *discoveryBuilder) ensureGroup(key, version string, files []VersionFile) string {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "_default"
	}
	if id, ok := b.groupByKey[key]; ok {
		b.mergeGroupFiles(id, files)
		return id
	}
	id := "product"
	if len(b.config.VersionGroups) > 0 {
		id = slug("version-" + strings.ReplaceAll(key, ".", "-"))
		if id == "version-_default" || id == "" {
			id = "product-" + fmt.Sprint(len(b.config.VersionGroups)+1)
		}
	}
	base := id
	for suffix := 2; b.groupIDs[id]; suffix++ {
		id = fmt.Sprintf("%s-%d", base, suffix)
	}
	b.groupIDs[id] = true
	b.groupByKey[key] = id
	name := "产品版本"
	if version != "" {
		name = "版本 " + version
	}
	b.config.VersionGroups = append(b.config.VersionGroups, VersionGroup{ID: id, Name: name, CurrentVersion: version, VersionFiles: mergeVersionFiles(nil, files)})
	return id
}

func (b *discoveryBuilder) mergeGroupFiles(id string, files []VersionFile) {
	for i := range b.config.VersionGroups {
		if b.config.VersionGroups[i].ID == id {
			b.config.VersionGroups[i].VersionFiles = mergeVersionFiles(b.config.VersionGroups[i].VersionFiles, files)
			return
		}
	}
}

func (b *discoveryBuilder) nearestExpoGroup(dir string) (string, bool) {
	root := filepath.Clean(b.root)
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		if group, ok := b.expoGroups[current]; ok {
			return group, true
		}
		if current == root {
			break
		}
		parent := filepath.Dir(current)
		if parent == current || !strings.HasPrefix(strings.ToLower(parent), strings.ToLower(root)) {
			break
		}
	}
	return "", false
}

func (b *discoveryBuilder) nearestGroup(_ string) string {
	if len(b.config.VersionGroups) == 0 {
		return b.ensureGroup("default", "", nil)
	}
	return b.config.VersionGroups[0].ID
}

func (b *discoveryBuilder) targetID(rel, suffix string) string {
	prefix := ""
	if rel != "." {
		prefix = slug(strings.ReplaceAll(rel, "/", "-")) + "-"
	}
	id := slug(prefix + suffix)
	base := id
	for n := 2; b.targetIDs[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

func (b *discoveryBuilder) addTarget(target Target) {
	if b.targetIDs[target.ID] {
		return
	}
	b.targetIDs[target.ID] = true
	if target.Artifacts == nil {
		target.Artifacts = []string{}
	}
	b.config.Targets = append(b.config.Targets, target)
}

func detectTauriVersion(root string, project nodeProject) (string, []VersionFile) {
	dir := filepath.Join(project.Dir, "src-tauri")
	files := []VersionFile{}
	version := ""
	for _, name := range []string{"tauri.conf.json", "tauri.conf.json5"} {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil || name == "tauri.conf.json5" {
			continue
		}
		var cfg struct {
			Version string `json:"version"`
			Package struct {
				Version string `json:"version"`
			} `json:"package"`
		}
		if json.Unmarshal(raw, &cfg) == nil {
			pointer := "/version"
			value := cfg.Version
			if value == "" {
				value, pointer = cfg.Package.Version, "/package/version"
			}
			if value != "" {
				version = value
				files = append(files, VersionFile{Path: relative(root, path), Format: "json", JSONPointer: pointer})
			}
		}
	}
	cargoPath := filepath.Join(dir, "Cargo.toml")
	if raw, err := os.ReadFile(cargoPath); err == nil {
		if match := cargoVersionPattern.FindSubmatch(raw); len(match) == 2 {
			cargoVersion := string(match[1])
			if version == "" {
				version = cargoVersion
			}
			if cargoVersion == version {
				files = append(files, VersionFile{Path: relative(root, cargoPath), Format: "cargo"})
				cargoLockPath := filepath.Join(dir, "Cargo.lock")
				if fileExists(cargoLockPath) {
					files = append(files, VersionFile{Path: relative(root, cargoLockPath), Format: "cargo-lock"})
				}
			}
		}
	}
	if version == "" {
		version = project.Version
	}
	if project.Version == version && version != "" {
		files = append(files, VersionFile{Path: relative(root, filepath.Join(project.Dir, "package.json")), Format: "json", JSONPointer: "/version"})
		if fileExists(filepath.Join(project.Dir, "package-lock.json")) {
			files = append(files, VersionFile{Path: relative(root, filepath.Join(project.Dir, "package-lock.json")), Format: "npm-lock"})
		}
	}
	return version, mergeVersionFiles(nil, files)
}

var (
	cargoVersionPattern  = regexp.MustCompile(`(?m)^version\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+(?:[-+][^"]+)?)"`)
	gradleVersionPattern = regexp.MustCompile(`(?m)versionName\s*[= ]\s*["']([^"']+)["']`)
)

func detectGradleVersion(root, dir string) (string, VersionFile) {
	candidates := []string{
		filepath.Join(dir, "app", "build.gradle"), filepath.Join(dir, "app", "build.gradle.kts"),
		filepath.Join(dir, "build.gradle"), filepath.Join(dir, "build.gradle.kts"),
	}
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if match := gradleVersionPattern.FindSubmatch(raw); len(match) == 2 {
			return string(match[1]), VersionFile{Path: relative(root, path), Format: "gradle"}
		}
	}
	return "", VersionFile{}
}

func detectTauriBuild(project nodeProject) string {
	for _, script := range []string{"tauri:build", "build:tauri", "desktop:build"} {
		if _, ok := project.Scripts[script]; ok {
			return scriptCommand(project, script)
		}
	}
	if _, ok := project.Scripts["tauri"]; ok {
		return scriptCommand(project, "tauri") + " build"
	}
	return "npx tauri build"
}

func scriptCommand(project nodeProject, script string) string {
	if _, ok := project.Scripts[script]; !ok {
		return ""
	}
	if project.Manager == "yarn" {
		return "yarn " + script
	}
	return project.Manager + " run " + script
}

func packageCommand(manager string) string {
	if manager == "yarn" {
		return "yarn pack"
	}
	return manager + " pack"
}

func looksLikeServer(project nodeProject) bool {
	base := strings.ToLower(filepath.Base(project.Dir))
	if base == "server" || base == "backend" || base == "api" {
		return true
	}
	return hasAnyDependency(project.Dependencies, "express", "fastify", "@nestjs/core", "koa", "hapi", "hono")
}

func nodeServerArtifacts(project nodeProject) []string {
	if _, ok := project.Scripts["build"]; ok {
		return []string{"dist/**"}
	}
	return []string{}
}

func anyLocalRunner() Runner {
	return Runner{Type: "local", OS: []string{"windows", "linux", "darwin"}}
}

func mergeVersionFiles(current, incoming []VersionFile) []VersionFile {
	seen := map[string]bool{}
	out := make([]VersionFile, 0, len(current)+len(incoming))
	for _, file := range append(append([]VersionFile{}, current...), incoming...) {
		key := file.Path + "\x00" + file.Format + "\x00" + file.JSONPointer
		if file.Path == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func uniqueDirs(paths []string) []string {
	dirs := make([]string, 0, len(paths))
	for _, path := range paths {
		dirs = append(dirs, filepath.Dir(path))
	}
	return sortedUnique(dirs)
}

func joinRelative(base, name string) string {
	if base == "." || base == "" {
		return name
	}
	return strings.TrimSuffix(base, "/") + "/" + name
}

func displayName(dir string) string {
	name := filepath.Base(dir)
	if name == "." || name == "" {
		return "项目"
	}
	return name
}

func hasAnyDependency(deps map[string]string, names ...string) bool {
	for _, name := range names {
		if _, ok := deps[strings.ToLower(name)]; ok {
			return true
		}
	}
	return false
}

func hasAnyFile(dir string, names ...string) bool {
	for _, name := range names {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_'
		if valid {
			out.WriteRune(r)
			lastDash = false
		} else if !lastDash && out.Len() > 0 {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-.")
}
