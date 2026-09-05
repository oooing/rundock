package releaseconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func TestScanDiscoversCommonMultiTargetLayout(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{
  "name":"demo","version":"2.0.0",
  "scripts":{"test":"vitest run","build":"vite build","tauri":"tauri"},
  "devDependencies":{"vite":"1","@tauri-apps/cli":"2"}
}`)
	writeFixture(t, root, "package-lock.json", `{"name":"demo","version":"2.0.0","packages":{"":{"version":"2.0.0"}}}`)
	writeFixture(t, root, "src-tauri/tauri.conf.json", `{"version":"1.4.0"}`)
	writeFixture(t, root, "src-tauri/Cargo.toml", "[package]\nname = \"demo\"\nversion = \"1.4.0\"\n")
	writeFixture(t, root, "src-tauri/Cargo.lock", "version = 4\n\n[[package]]\nname = \"demo\"\nversion = \"1.4.0\"\n")
	writeFixture(t, root, "android/gradlew", "")
	writeFixture(t, root, "android/app/build.gradle", `android { defaultConfig { versionName "3.2.1" } }`)
	writeFixture(t, root, "server/Dockerfile", "FROM scratch\n")

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != SourceDetected || cfg.SchemaVersion != SchemaVersion || cfg.Confidence <= 0 {
		t.Fatalf("unexpected discovery metadata: %+v", cfg)
	}
	for _, expected := range []string{"web", "windows", "macos", "android-android", "server-docker"} {
		if targetByID(cfg, expected) == nil {
			t.Fatalf("missing target %q in %+v", expected, cfg.Targets)
		}
	}
	web := targetByID(cfg, "web")
	if web.Steps.Build != "npm run build" || len(web.Artifacts) != 1 || web.Artifacts[0] != "dist/**" {
		t.Fatalf("unexpected web target: %+v", web)
	}
	windows := targetByID(cfg, "windows")
	if len(windows.Runner.OS) != 1 || windows.Runner.OS[0] != "windows" || windows.Steps.Build != "npm run tauri build" {
		t.Fatalf("unexpected windows target: %+v", windows)
	}
	if !groupHasVersionFile(cfg, windows.VersionGroup, "src-tauri/tauri.conf.json") ||
		!groupHasVersionFile(cfg, windows.VersionGroup, "src-tauri/Cargo.toml") ||
		!groupHasVersionFile(cfg, windows.VersionGroup, "src-tauri/Cargo.lock") {
		t.Fatalf("tauri version files not grouped: %+v", cfg.VersionGroups)
	}
	if !groupHasVersionFile(cfg, web.VersionGroup, "package-lock.json") {
		t.Fatalf("package-lock version file not grouped: %+v", cfg.VersionGroups)
	}
	if groupByID(cfg, web.VersionGroup).CurrentVersion != "2.0.0" || groupByID(cfg, windows.VersionGroup).CurrentVersion != "1.4.0" {
		t.Fatalf("independent version groups were not detected: %+v", cfg.VersionGroups)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ManifestPath))); !os.IsNotExist(err) {
		t.Fatalf("scan must not write manifest, stat err=%v", err)
	}
}

func TestScanUsesExpoPackageVersionInsteadOfGeneratedAndroidVersion(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "frontend/package.json", `{
  "name":"product-web","version":"1.2.3","scripts":{"build":"vite build"},
  "devDependencies":{"vite":"1"}
}`)
	writeFixture(t, root, "mobile/package.json", `{
  "name":"product-mobile","version":"1.2.3","dependencies":{"expo":"57"}
}`)
	writeFixture(t, root, "mobile/package-lock.json", `{"name":"product-mobile","version":"1.2.3","packages":{"":{"version":"1.2.3"}}}`)
	writeFixture(t, root, "mobile/app.config.ts", "export default { expo: { version: require('./package.json').version } }\n")
	writeFixture(t, root, "mobile/android/gradlew", "")
	writeFixture(t, root, "mobile/android/app/build.gradle", `android { defaultConfig { versionName "0.9.0" } }`)
	writeFixture(t, root, "mobile/.gitignore", "/android\n")

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	web := targetByID(cfg, "frontend-web")
	android := targetByID(cfg, "mobile-android-android")
	mobile := targetByID(cfg, "mobile-node")
	if web == nil || android == nil || mobile == nil {
		t.Fatalf("expected Web, Android and mobile targets: %+v", cfg.Targets)
	}
	if android.VersionGroup != mobile.VersionGroup {
		t.Fatalf("Expo Android must use its package version group: android=%s mobile=%s", android.VersionGroup, mobile.VersionGroup)
	}
	if android.VersionGroup == web.VersionGroup {
		t.Fatalf("equal version numbers must not merge unrelated Web and Android groups: %s", android.VersionGroup)
	}
	if !groupHasVersionFile(cfg, android.VersionGroup, "mobile/package.json") ||
		!groupHasVersionFile(cfg, android.VersionGroup, "mobile/package-lock.json") {
		t.Fatalf("Expo package version files missing: %+v", groupByID(cfg, android.VersionGroup))
	}
	if groupHasVersionFile(cfg, android.VersionGroup, "mobile/android/app/build.gradle") {
		t.Fatalf("generated Android version file must not be selected: %+v", groupByID(cfg, android.VersionGroup))
	}
	if !warningsContain(cfg.Warnings, "不修改自动生成的 android 目录") {
		t.Fatalf("Expo version-source decision should be explained: %+v", cfg.Warnings)
	}
}

func TestScanKeepsMatchingTauriAndPackageVersionsTogether(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{
  "name":"desktop-web","version":"2.0.0",
  "scripts":{"build":"vite build","tauri":"tauri"},
  "devDependencies":{"vite":"1","@tauri-apps/cli":"2"}
}`)
	writeFixture(t, root, "package-lock.json", `{"name":"desktop-web","version":"2.0.0","packages":{"":{"version":"2.0.0"}}}`)
	writeFixture(t, root, "src-tauri/tauri.conf.json", `{"version":"2.0.0"}`)
	writeFixture(t, root, "src-tauri/Cargo.toml", "[package]\nname = \"desktop-web\"\nversion = \"2.0.0\"\n")
	writeFixture(t, root, "src-tauri/Cargo.lock", "version = 4\n\n[[package]]\nname = \"desktop-web\"\nversion = \"2.0.0\"\n")

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	web := targetByID(cfg, "web")
	windows := targetByID(cfg, "windows")
	if web == nil || windows == nil || web.VersionGroup != windows.VersionGroup {
		t.Fatalf("matching Web and Tauri versions should share one synchronized group: web=%+v windows=%+v", web, windows)
	}
	for _, path := range []string{"package.json", "package-lock.json", "src-tauri/tauri.conf.json", "src-tauri/Cargo.toml", "src-tauri/Cargo.lock"} {
		if !groupHasVersionFile(cfg, web.VersionGroup, path) {
			t.Fatalf("missing synchronized version file %s in %+v", path, groupByID(cfg, web.VersionGroup))
		}
	}
}

func TestGetAddsCargoLockToOlderSavedTauriConfig(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "src-tauri/Cargo.toml", "[package]\nname = \"demo\"\nversion = \"1.2.3\"\n")
	writeFixture(t, root, "src-tauri/Cargo.lock", "version = 4\n\n[[package]]\nname = \"demo\"\nversion = \"1.2.3\"\n")
	writeFixture(t, root, ManifestPath, `{
  "schemaVersion": 1,
  "versionGroups": [{
    "id": "product", "name": "产品版本", "currentVersion": "1.2.3",
    "versionFiles": [{"path":"src-tauri/Cargo.toml","format":"cargo"}]
  }],
  "targets": [{
    "id":"windows", "name":"Windows", "kind":"desktop", "versionGroup":"product",
    "workingDir":".", "runner":{"type":"local","os":["windows"]},
    "enabled":true, "detected":false, "confidence":1, "steps":{}, "artifacts":[]
  }]
}`)

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Get(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if !groupHasVersionFile(cfg, "product", "src-tauri/Cargo.lock") {
		t.Fatalf("older Tauri config was not upgraded with Cargo.lock: %+v", cfg.VersionGroups)
	}
}

func TestScanFallsBackToEditableCustomTarget(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "README.md", "demo")
	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Kind != "custom" || cfg.Targets[0].Enabled {
		t.Fatalf("unexpected fallback: %+v", cfg.Targets)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("fallback discovery should explain that manual configuration is needed")
	}
}

func TestScanKeepsDockerCoveredServicesAsDisabledAdvancedTargets(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "Dockerfile", "FROM scratch\n")
	writeFixture(t, root, "backend/requirements.txt", "fastapi\n")
	writeFixture(t, root, "worker/go.mod", "module example.invalid/worker\n\ngo 1.23\n")

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	docker := targetByID(cfg, "docker")
	python := targetByID(cfg, "backend-python-server")
	goService := targetByID(cfg, "worker-go-server")
	if docker == nil || !docker.Enabled {
		t.Fatalf("Docker should remain the ordinary enabled target: %+v", cfg.Targets)
	}
	if python == nil || python.Enabled || python.Steps.Check == "" {
		t.Fatalf("covered Python service should remain visible but disabled: %+v", python)
	}
	if goService == nil || goService.Enabled || goService.Steps.Build == "" {
		t.Fatalf("covered Go service should remain visible but disabled: %+v", goService)
	}
	if !warningsContain(cfg.Warnings, "高级配置") {
		t.Fatalf("coverage decision should be explained: %+v", cfg.Warnings)
	}
}

func TestScanUsesGitPushRunnerForPushTriggeredDockerWorkflow(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "Dockerfile", "FROM scratch\n")
	writeFixture(t, root, "backend/requirements.txt", "fastapi\n")
	writeFixture(t, root, ".github/workflows/container.yml", `name: Container
on:
  push:
    branches: [main]
jobs:
  build:
    steps:
      - uses: docker/build-push-action@v6
`)

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	container := targetByID(cfg, "cloud-container")
	if container == nil || !container.Enabled || container.Runner.Type != RunnerGitPush || container.Steps.Publish == "" {
		t.Fatalf("push-triggered Docker workflow should be a cloud target: %+v", cfg.Targets)
	}
	if container.Steps.Check != "" || container.Steps.Build != "" {
		t.Fatalf("cloud target must not invoke local Docker: %+v", container)
	}
	python := targetByID(cfg, "backend-python-server")
	if python == nil || !python.Enabled {
		t.Fatalf("cloud Docker workflow must not disable local source checks: %+v", python)
	}
	if !warningsContain(cfg.Warnings, "不会在本机运行 Docker") {
		t.Fatalf("cloud execution decision should be explained: %+v", cfg.Warnings)
	}
}

func TestScanDisablesDeepNodeModulesWhenProductPlatformsExist(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "frontend/package.json", `{
  "name":"product-web","version":"1.0.0","scripts":{"build":"vite build"},
  "devDependencies":{"vite":"1"}
}`)
	writeFixture(t, root, "modules/parser/package.json", `{"name":"parser","version":"1.0.0"}`)
	writeFixture(t, root, "tools/release/package.json", `{"name":"release-helper","version":"1.0.0"}`)

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	web := targetByID(cfg, "frontend-web")
	module := targetByID(cfg, "modules-parser-node")
	tool := targetByID(cfg, "tools-release-node")
	if web == nil || !web.Enabled {
		t.Fatalf("product Web target should remain enabled: %+v", web)
	}
	if module == nil || module.Enabled || module.Steps.Package == "" {
		t.Fatalf("deep Node module should remain editable but disabled: %+v", module)
	}
	if tool == nil || tool.Enabled {
		t.Fatalf("tool package should remain editable but disabled: %+v", tool)
	}
}

func TestScanDoesNotSuppressPureNodePackages(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"root-package","version":"1.0.0"}`)
	writeFixture(t, root, "packages/helper/package.json", `{"name":"helper","version":"1.0.0"}`)

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	rootNode := targetByID(cfg, "node")
	helper := targetByID(cfg, "packages-helper-node")
	if rootNode == nil || !rootNode.Enabled || helper == nil || !helper.Enabled {
		t.Fatalf("pure Node repository targets must not be suppressed: %+v", cfg.Targets)
	}
}

func fixtureService(t *testing.T, root string) (*Service, func()) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "release-config.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := &store.App{ID: "app1", Name: "demo", EntryScript: filepath.Join(root, "start.bat"), Cwd: root,
		AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}
	if err := st.CreateApp(a); err != nil {
		st.Close()
		t.Fatal(err)
	}
	return New(st), func() { _ = st.Close() }
}

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func targetByID(cfg *Config, id string) *Target {
	for i := range cfg.Targets {
		if cfg.Targets[i].ID == id {
			return &cfg.Targets[i]
		}
	}
	return nil
}

func groupByID(cfg *Config, id string) *VersionGroup {
	for i := range cfg.VersionGroups {
		if cfg.VersionGroups[i].ID == id {
			return &cfg.VersionGroups[i]
		}
	}
	return nil
}

func groupHasVersionFile(cfg *Config, groupID, path string) bool {
	group := groupByID(cfg, groupID)
	if group == nil {
		return false
	}
	for _, file := range group.VersionFiles {
		if file.Path == path {
			return true
		}
	}
	return false
}

func warningsContain(warnings []string, wanted string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, wanted) {
			return true
		}
	}
	return false
}
