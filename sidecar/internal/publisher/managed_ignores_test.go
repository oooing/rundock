package publisher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedIgnoresPreserveFilesIndexAndSourceChecks(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	writeTestFile(t, filepath.Join(repo, "demos/tracked.ts"), "tracked demo")
	runGit(t, repo, "add", "demos/tracked.ts")
	runGit(t, repo, "commit", "-m", "tracked demo")
	writeTestFile(t, filepath.Join(repo, "demos/tracked.ts"), "changed demo")
	original := []byte("# user rules\r\n/private/\r\n!/reports/\r\n")
	writeTestFile(t, filepath.Join(repo, ".gitignore"), string(original))
	ignored := []string{"demo/a.ts", "demos/new.ts", "DEMO/copy.ts", "artifacts/check/result.txt", "outputs/run/file.txt", "code/frontend/node_modules/p/index.js", "code/backend/__pycache__/file.pyc", "code/frontend/.vite/cache", "code/frontend/src-tauri/target/debug/app.exe"}
	sources := []string{"src/demo/page.ts", "tests/test_main.py", "src/target/main.go", "build/recipe.go", "reports/shared.txt"}
	for _, name := range append(ignored, sources...) {
		writeTestFile(t, filepath.Join(repo, name), "keep "+name)
	}
	head, index := runGit(t, repo, "rev-parse", "HEAD"), runGit(t, repo, "ls-files", "--stage")
	policy, err := svc.EnsureManagedIgnores(context.Background(), "app1")
	if err != nil || !policy.Managed {
		t.Fatalf("ensure: %+v %v", policy, err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if !bytes.HasSuffix(raw, original) {
		t.Fatal("original rules/line endings were changed")
	}
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, file := range pf.Changes {
		listed[file.Path] = true
	}
	for _, name := range ignored {
		if listed[name] {
			t.Errorf("still listed: %s", name)
		}
		if data, err := os.ReadFile(filepath.Join(repo, name)); err != nil || string(data) != "keep "+name {
			t.Errorf("file changed: %s", name)
		}
	}
	for _, name := range append(sources, "demos/tracked.ts") {
		if !listed[name] {
			t.Errorf("source/tracked file hidden: %s", name)
		}
	}
	if head != runGit(t, repo, "rev-parse", "HEAD") || index != runGit(t, repo, "ls-files", "--stage") {
		t.Fatal("HEAD/index changed")
	}
	path := filepath.Join(repo, "src/demo/page.ts")
	info, _ := os.Stat(path)
	writeTestFile(t, path, strings.Repeat("x", int(info.Size())))
	os.Chtimes(path, info.ModTime(), info.ModTime())
	fresh, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil || fresh.StatusFingerprint == pf.StatusFingerprint {
		t.Fatal("real same-size/source-content change was missed", err)
	}
	// Ignored changes do not affect the selected source snapshot.
	writeTestFile(t, filepath.Join(repo, "artifacts/check/result.txt"), "new local output")
	writeTestFile(t, filepath.Join(repo, "artifacts/new-verification/copy.ts"), "new verification copy")
	writeTestFile(t, filepath.Join(repo, "demos/new-demo/index.ts"), "new demo")
	writeTestFile(t, filepath.Join(repo, "code/new-tool/.cache/new.bin"), "new cache")
	again, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil || again.StatusFingerprint != fresh.StatusFingerprint {
		t.Fatal("ignored output still changes source snapshot", err)
	}
}

func TestManagedIgnoresRestorePersistsAndKeepsUserRules(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "/demos/private.txt\n")
	writeTestFile(t, filepath.Join(repo, "demos/shared.ts"), "demo")
	writeTestFile(t, filepath.Join(repo, "demos/private.txt"), "private")
	policy, err := svc.EnsureManagedIgnores(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.UpdateManagedIgnores(context.Background(), "app1", ManagedIgnoreUpdate{ID: "demos", Enabled: false, Revision: policy.Revision})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, f := range result.Preflight.Changes {
		listed[f.Path] = true
	}
	if !listed["demos/shared.ts"] || listed["demos/private.txt"] {
		t.Fatal("restore did not respect existing user rules")
	}
	for _, f := range result.Preflight.Classifications {
		if f.Path == "demos/shared.ts" && f.SelectedDefault {
			t.Fatal("restoring unexpectedly selected upload")
		}
	}
	before, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	for i := 0; i < 2; i++ {
		policy, err = svc.EnsureManagedIgnores(context.Background(), "app1")
		if err != nil || policy.Groups[0].Enabled {
			t.Fatal("restore was re-enabled", err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if !bytes.Equal(before, after) {
		t.Fatal("repeated opening rewrites rules")
	}
	if _, err = svc.UpdateManagedIgnores(context.Background(), "app1", ManagedIgnoreUpdate{ID: "demos", Enabled: true, Revision: policy.Revision}); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "check-ignore", "demos/shared.ts")
}

func TestManagedIgnoresRejectStaleAndBusyEdits(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	if !svc.reserve(repo) {
		t.Fatal("reserve")
	}
	_, err := svc.EnsureManagedIgnores(context.Background(), "app1")
	svc.release(repo)
	if err == nil {
		t.Fatal("installed ignores during active release")
	}
	if _, err = os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("wrote while busy")
	}
	p, err := svc.EnsureManagedIgnores(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	writeTestFile(t, filepath.Join(repo, ".gitignore"), string(raw)+"/other/\n")
	_, err = svc.UpdateManagedIgnores(context.Background(), "app1", ManagedIgnoreUpdate{ID: "demos", Revision: p.Revision})
	if err == nil {
		t.Fatal("stale revision accepted")
	}
	changed, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if string(changed) != string(raw)+"/other/\n" {
		t.Fatal("concurrent edit overwritten")
	}
	writeTestFile(t, filepath.Join(repo, ".gitignore"), strings.Replace(string(raw), "/outputs/", "/my-files/", 1))
	if _, err = svc.EnsureManagedIgnores(context.Background(), "app1"); err == nil {
		t.Fatal("manually changed block overwritten")
	}
}

func TestManagedIgnoreParserAndBOM(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		g := defaultIgnoreGroups()
		g[1].Enabled = false
		raw := []byte("\xef\xbb\xbf" + renderManagedIgnores(g, newline) + "# user" + newline)
		p, start, end, err := parseManagedIgnores(raw)
		if err != nil || start != 3 || end != len(raw)-len("# user"+newline) || p.Groups[1].Enabled {
			t.Fatalf("parse: %+v %d %d %v", p, start, end, err)
		}
		for _, bad := range []string{managedIgnoreStart, managedIgnoreEnd, string(raw) + renderManagedIgnores(g, newline), strings.Replace(string(raw), "# ignored: demos", "# unknown", 1)} {
			if _, _, _, err := parseManagedIgnores([]byte(bad)); err == nil {
				t.Fatal("malformed block accepted")
			}
		}
	}
}

func TestManagedIgnoresDoNotFollowIgnoreSymlink(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	outside := filepath.Join(t.TempDir(), "outside")
	writeTestFile(t, outside, "unchanged")
	if err := os.Symlink(outside, filepath.Join(repo, ".gitignore")); err != nil {
		t.Skip("symlinks not available:", err)
	}
	if _, err := svc.EnsureManagedIgnores(context.Background(), "app1"); err == nil {
		t.Fatal("followed .gitignore link")
	}
	raw, _ := os.ReadFile(outside)
	if string(raw) != "unchanged" {
		t.Fatal("outside file changed")
	}
}
