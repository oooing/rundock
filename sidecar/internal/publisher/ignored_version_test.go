package publisher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestPreflightBlocksIgnoredUntrackedConfiguredVersionFile(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()

	writeTestFile(t, filepath.Join(repo, ".gitignore"), "generated/\n")
	if err := os.MkdirAll(filepath.Join(repo, ".launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, ".launcher", "release.yaml"), `{
  "schemaVersion": 1,
  "versionGroups": [{
    "id": "android",
    "name": "Android",
    "tagPrefix": "android",
    "currentVersion": "1.0.0",
    "versionFiles": [{"path":"generated/version.json","format":"json","jsonPointer":"/version"}]
  }],
  "targets": [{
    "id": "android",
    "name": "Android",
    "kind": "android",
    "versionGroup": "android",
    "workingDir": ".",
    "runner": {"type":"local","os":["windows","linux","darwin"]},
    "enabled": true,
    "detected": false,
    "confidence": 1,
    "steps": {},
    "artifacts": []
  }]
}
`)
	runGit(t, repo, "add", ".gitignore", ".launcher/release.yaml")
	runGit(t, repo, "commit", "-m", "add release config")
	runGit(t, repo, "push", "origin", "main")
	if err := os.MkdirAll(filepath.Join(repo, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "generated", "version.json"), "{\"version\":\"1.0.0\"}\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if pf.CanRelease || !hasIssue(pf, "version_file_ignored") {
		t.Fatalf("ignored untracked version file must block release: %+v", pf.BlockingIssues)
	}
	if !strings.Contains(pf.BlockingIssues[len(pf.BlockingIssues)-1].Message, "generated/version.json") {
		t.Fatalf("blocking issue must name the ignored version file: %+v", pf.BlockingIssues)
	}
}

func TestGitAddFailureRollsBackPartialIndex(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()

	writeTestFile(t, filepath.Join(repo, ".gitignore"), "generated/\n")
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-m", "ignore generated versions")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "release change\n")
	writeTestFile(t, filepath.Join(repo, "selected-new.txt"), "include me\n")
	if err := os.MkdirAll(filepath.Join(repo, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	versionPath := filepath.Join(repo, "generated", "version.json")
	writeTestFile(t, versionPath, "{\"version\":\"1.0.0\"}\n")

	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	statusRaw, err := svc.gitRaw(context.Background(), repo, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		t.Fatal(err)
	}
	version := store.ReleaseVersion{VersionGroupID: "android", VersionGroupName: "Android", TargetVersion: "1.0.1", TagName: "android/v1.0.1"}
	plan := &executionPlan{
		SchemaVersion: executionPlanSchemaVersion,
		ConfigPath:    releaseconfig.ManifestPath,
		VersionGroups: []planVersionGroup{{
			ID: "android", Name: "Android", TagPrefix: "android", CurrentVersion: "1.0.0",
			VersionFiles: []releaseconfig.VersionFile{{Path: "generated/version.json", Format: "json", JSONPointer: "/version"}},
		}},
		ReleaseVersions: []store.ReleaseVersion{version},
		Targets:         []planTarget{},
	}
	planJSON, err := plan.marshal()
	if err != nil {
		t.Fatal(err)
	}
	run := &store.ReleaseRun{
		ID: "partial-add", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin",
		TargetVersion: "1.0.1", TagName: version.TagName, CreateTag: true, Versions: []store.ReleaseVersion{version},
		ExecutionPlan: planJSON, Status: "queued", Stage: "preparing",
		StatusFingerprint: statusFingerprint(repo, head, statusRaw, parseChanges(statusRaw)),
	}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	pf := &Preflight{RepoRoot: repo, Branch: "main", HeadSHA: head, StatusFingerprint: run.StatusFingerprint}
	svc.execute(run, pf, []string{"tracked.txt", "selected-new.txt"}, "release", "")

	completed, err := svc.store.GetReleaseRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "failed" || completed.ErrorCode != "git_add_failed" {
		t.Fatalf("expected git_add_failed, got %+v", completed)
	}
	diff := exec.Command("git", "-C", repo, "diff", "--cached", "--quiet")
	diff.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, diffErr := diff.CombinedOutput(); diffErr != nil {
		t.Fatalf("partial git add left paths staged: %v\n%s", diffErr, out)
	}
	status := runGit(t, repo, "status", "--porcelain=v1", "--untracked-files=all")
	if !strings.Contains(status, " M tracked.txt") || !strings.Contains(status, "?? selected-new.txt") {
		t.Fatalf("cleanup must preserve worktree changes: %s", status)
	}
	raw, err := os.ReadFile(versionPath)
	if err != nil || !strings.Contains(string(raw), `"1.0.0"`) {
		t.Fatalf("version file was not restored: %q, %v", raw, err)
	}
}
