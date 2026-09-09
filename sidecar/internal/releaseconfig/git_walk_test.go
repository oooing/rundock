package releaseconfig

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitFixture(t *testing.T, root string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git: %s %v", out, err)
	}
}
func TestGitDiscoveryHonorsIgnoreRulesAndTrackedFiles(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init")
	writeFixture(t, root, ".gitignore", "/work/\n/kept/*\n!/kept/real/\n")
	writeFixture(t, root, "nested/.gitignore", "cache/\n")
	writeFixture(t, root, ".git/info/exclude", "local-only/\n")
	for _, dir := range []string{"code", "work/cache", "work/old-checkout/code", "kept/real", "kept/ignored", "nested/cache", "local-only", "中文 空格", "tracked", "deleted"} {
		writeFixture(t, root, dir+"/package.json", `{"name":"test","version":"1.0.0","scripts":{"build":"vite build"},"devDependencies":{"vite":"1"}}`)
	}
	gitFixture(t, root, "add", "tracked/package.json", "deleted/package.json")
	writeFixture(t, root, "tracked/.gitignore", "package.json\n") // Already tracked remains eligible.
	if err := os.Remove(filepath.Join(root, "deleted/package.json")); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, filepath.Join(root, "work/old-checkout"), "init")
	service, cleanup := fixtureService(t, root)
	defer cleanup()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, target := range cfg.Targets {
		got[target.WorkingDir] = true
	}
	for _, dir := range []string{"code", "kept/real", "中文 空格", "tracked"} {
		if !got[dir] {
			t.Fatalf("missing %s: %+v", dir, got)
		}
		delete(got, dir)
	}
	if len(got) != 0 {
		t.Fatalf("ignored or deleted projects leaked: %+v", got)
	}
}
func TestGitFailureDoesNotFallBackToIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".git", "gitdir: missing-git-dir\n")
	writeFixture(t, root, "work/package.json", `{"version":"bad"}`)
	service, cleanup := fixtureService(t, root)
	defer cleanup()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if !warningsContain(cfg.Warnings, "无法读取 Git 文件范围") {
		t.Fatal(cfg.Warnings)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].ID != "custom" {
		t.Fatal(cfg.Targets)
	}
}
func TestExplicitManifestStillOverridesDiscovery(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init")
	writeFixture(t, root, ".gitignore", "generated/\n")
	writeFixture(t, root, "generated/package.json", `{"version":"1.0.0"}`)
	service, cleanup := fixtureService(t, root)
	defer cleanup()
	cfg := &Config{SchemaVersion: 1, VersionGroups: []VersionGroup{{ID: "app", Name: "App", VersionFiles: []VersionFile{{Path: "generated/package.json", Format: "json"}}}}, Targets: []Target{{ID: "web", Name: "Web", Kind: "web", VersionGroup: "app", WorkingDir: "generated", Enabled: true, Runner: Runner{Type: "local"}}}}
	if _, err := service.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceFile || got.Targets[0].WorkingDir != "generated" {
		t.Fatal(got)
	}
	// Explicit version files still receive publisher validation before release.
}
