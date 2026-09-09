package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureFile(t *testing.T, root, path, body string) string {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestDiscoverProjectEntriesWithoutExecuting(t *testing.T) {
	root := t.TempDir()
	start := fixtureFile(t, root, "start-brickmuse.cmd", "@echo off\r\ncd /d code\r\ncall npm run dev\r\necho never > SHOULD_NOT_EXIST")
	fixtureFile(t, root, "code/package.json", `{"name":"brickmuse","scripts":{"dev":"vite"}}`)
	for _, path := range []string{"node_modules/pkg/start.cmd", ".git/run.cmd", "dist/start.bat", ".venv/run.ps1", "release.cmd", "scripts/install.bat", "scripts/deploy.ps1", "a/b/c/d/start.cmd"} {
		fixtureFile(t, root, path, "echo excluded")
	}
	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Options) != 2 || result.Options[0].Path != start || !result.Options[0].Recommended || result.Options[1].Command != "npm run dev" {
		t.Fatalf("%+v", result)
	}
	if _, err = os.Stat(filepath.Join(root, "SHOULD_NOT_EXIST")); !os.IsNotExist(err) {
		t.Fatal("discovery executed a script")
	}
	candidate, err := Import(start, nil)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.AdapterType != "batch" || candidate.Args[len(candidate.Args)-1] != start {
		t.Fatalf("wrapper replaced: %+v", candidate)
	}
}

func TestDiscoverEmptyInvalidAndBoundedFiles(t *testing.T) {
	root := t.TempDir()
	fixtureFile(t, root, "package.json", `{"scripts":{"build":"vite build","dev":""}}`)
	fixtureFile(t, root, "sub/package.json", "invalid json")
	fixtureFile(t, root, "huge-start.cmd", strings.Repeat("x", 1024*1024+1))
	result, err := Discover(context.Background(), root)
	if err != nil || len(result.Options) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err = Discover(context.Background(), "relative"); err == nil {
		t.Fatal("accepted relative path")
	}
	if _, err = Discover(context.Background(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("accepted missing path")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Discover(ctx, root); err == nil {
		t.Fatal("ignored cancellation")
	}
	for i := 0; i < 85; i++ {
		fixtureFile(t, root, strings.Repeat("a", i+1)+".cmd", "echo candidate")
	}
	result, err = Discover(context.Background(), root)
	if err != nil || !result.Truncated || len(result.Options) > 20 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestPackageEntryKeepsManagerAndCommand(t *testing.T) {
	for _, manager := range []string{"npm", "pnpm", "yarn"} {
		t.Run(manager, func(t *testing.T) {
			root := t.TempDir()
			path := fixtureFile(t, root, "package.json", `{"name":"my-app","packageManager":"`+manager+`@1","scripts":{"dev":"","start":"node server.js"}}`)
			result, err := Discover(context.Background(), root)
			if err != nil || len(result.Options) != 1 || result.Options[0].Command != manager+" run start" {
				t.Fatalf("%+v %v", result, err)
			}
			c, err := Import(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.AdapterType != manager || c.Cmd != manager+".cmd" || strings.Join(c.Args, " ") != "run start" || c.Cwd != root {
				t.Fatalf("%+v", c)
			}
		})
	}
}

func TestDiscoveryDoesNotTraverseSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	fixtureFile(t, outside, "start.cmd", "echo outside")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	result, err := Discover(context.Background(), root)
	if err != nil || len(result.Options) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}
