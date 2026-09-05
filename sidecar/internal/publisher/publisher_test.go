package publisher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

const testReleaseNotes = "## 优化\n- 完善发布流程"

func frozenTagExecutionPlan(pushRemote bool, versions ...store.ReleaseVersion) *executionPlan {
	return &executionPlan{
		SchemaVersion: executionPlanSchemaVersion, ConfigPath: releaseconfig.ManifestPath,
		PushRemote: &pushRemote, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
		VersionGroups: []planVersionGroup{}, ReleaseVersions: append([]store.ReleaseVersion{}, versions...), Targets: []planTarget{},
	}
}

func TestSemverAndVersionReplacement(t *testing.T) {
	for _, invalid := range []string{"1.2", "01.2.3", "1.2.3-beta", "1.2.3.4"} {
		if _, ok := parseSemver(invalid); ok {
			t.Fatalf("expected invalid semver: %s", invalid)
		}
	}
	if got := nextPatch("v1.2.3", "1.3.0"); got != "1.3.1" {
		t.Fatalf("nextPatch = %s", got)
	}
	jsonDoc := []byte("{\n  \"name\": \"demo\",\n  \"version\": \"1.2.3\",\n  \"keep\": true\n}\n")
	updated, ok := replaceVersionForTest(jsonDoc, "2.0.0", false)
	if !ok || !strings.Contains(string(updated), `"version": "2.0.0"`) || !strings.Contains(string(updated), `"keep": true`) {
		t.Fatalf("unexpected JSON update: %s", updated)
	}
	cargo := []byte("[package]\nname = \"demo\"\nversion = \"1.2.3\"\n\n[dependencies]\nfoo = \"4.5.6\"\n")
	updated, ok = replaceVersionForTest(cargo, "2.0.0", true)
	if !ok || !strings.Contains(string(updated), `version = "2.0.0"`) || !strings.Contains(string(updated), `foo = "4.5.6"`) {
		t.Fatalf("unexpected Cargo update: %s", updated)
	}
}

func TestParseChangesNULTerminatesUnicodeAndRenamePaths(t *testing.T) {
	changes := parseChanges(" M docs/更新优化.md\x00?? 新文件.txt\x00R  new name.txt\x00old name.txt\x00")
	if len(changes) != 3 {
		t.Fatalf("changes = %+v", changes)
	}
	byPath := map[string]FileChange{}
	for _, change := range changes {
		byPath[change.Path] = change
	}
	if _, ok := byPath["docs/更新优化.md"]; !ok {
		t.Fatalf("unicode path missing: %+v", changes)
	}
	if change, ok := byPath["新文件.txt"]; !ok || change.Tracked {
		t.Fatalf("untracked path mismatch: %+v", changes)
	}
	if _, ok := byPath["new name.txt"]; !ok {
		t.Fatalf("rename target missing: %+v", changes)
	}
	if _, ok := byPath["old name.txt"]; ok {
		t.Fatalf("rename source must not be selectable: %+v", changes)
	}
}

func TestParseCommittedChanges(t *testing.T) {
	changes := parseCommittedChanges("A\x00new.txt\x00M\x00docs/更新.md\x00R100\x00old.txt\x00renamed.txt\x00")
	if len(changes) != 3 {
		t.Fatalf("changes = %+v", changes)
	}
	byPath := map[string]string{}
	for _, change := range changes {
		byPath[change.Path] = change.Status
	}
	if byPath["new.txt"] != "A" || byPath["docs/更新.md"] != "M" || byPath["renamed.txt"] != "R100" {
		t.Fatalf("unexpected committed changes: %+v", changes)
	}
}

func TestPreflightBlocksStagedAndChangedFingerprint(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	runGit(t, repo, "add", "tracked.txt")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if pf.CanRelease || !hasIssue(pf, "staged_changes") {
		t.Fatalf("expected staged_changes issue: %+v", pf.BlockingIssues)
	}
	runGit(t, repo, "reset", "HEAD", "--", "tracked.txt")
	pf, err = svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	if pf.BlockingIssues == nil {
		t.Fatal("successful preflight must serialize blockingIssues as an empty array")
	}
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed again\n")
	_, err = svc.Start(context.Background(), "app1", CreateRequest{
		TargetVersion: "1.0.1", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
	})
	pe, ok := err.(*Error)
	if !ok || pe.Code != "status_changed" {
		t.Fatalf("expected status_changed, got %#v", err)
	}
}

func TestPreflightLocalSkipsRemoteFetch(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runGit(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))

	local, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if local.RemoteChecked || !local.CanRelease || hasIssue(local, "fetch_failed") {
		t.Fatalf("local preflight should skip remote fetch: %+v", local)
	}

	full, err := svc.Preflight(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if !full.RemoteChecked || full.CanRelease || !hasIssue(full, "fetch_failed") {
		t.Fatalf("full preflight should report remote fetch failure: %+v", full)
	}
}

func TestReleaseCommitsSelectedFilesAndPushesTag(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "release change\n")
	writeTestFile(t, filepath.Join(repo, "selected-new.txt"), "include me\n")
	writeTestFile(t, filepath.Join(repo, "not-selected.txt"), "keep local\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	if pf.VersionStrategy != StrategyNode || pf.SuggestedVersion != "1.0.1" {
		t.Fatalf("unexpected strategy/version: %s %s", pf.VersionStrategy, pf.SuggestedVersion)
	}
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		TargetVersion: "1.0.1", SelectedPaths: []string{"tracked.txt", "selected-new.txt"},
		CommitMessage: "chore(release): v1.0.1", StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := svc.store.GetReleaseRun(run.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.Status == "succeeded" {
			run = current
			break
		}
		if current.Status == "failed" {
			t.Fatalf("release failed at %s: %s", current.Stage, current.ErrorMessage)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if run.Status != "succeeded" {
		t.Fatalf("release did not finish: %+v", run)
	}
	if got := runGit(t, repo, "show", "HEAD:selected-new.txt"); strings.TrimSpace(got) != "include me" {
		t.Fatalf("selected untracked file was not committed: %q", got)
	}
	packageJSON, _ := os.ReadFile(filepath.Join(repo, "package.json"))
	if !strings.Contains(string(packageJSON), `"version": "1.0.1"`) {
		t.Fatalf("version not updated: %s", packageJSON)
	}
	status := runGit(t, repo, "status", "--porcelain=v1")
	if !strings.Contains(status, "?? not-selected.txt") {
		t.Fatalf("unselected file should remain untracked: %s", status)
	}
	remoteBranch := runGit(t, repo, "ls-remote", "origin", "refs/heads/main")
	remoteTag := runGit(t, repo, "ls-remote", "origin", "refs/tags/v1.0.1")
	if !strings.Contains(remoteBranch, run.CommitSHA) || strings.TrimSpace(remoteTag) == "" {
		t.Fatalf("remote refs missing: branch=%s tag=%s", remoteBranch, remoteTag)
	}
}

func TestPreflightBlocksRepositoryOperationAndRemoteBehind(t *testing.T) {
	t.Run("merge in progress", func(t *testing.T) {
		svc, repo, cleanup := newReleaseFixture(t)
		defer cleanup()
		runGit(t, repo, "checkout", "-b", "feature")
		writeTestFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
		runGit(t, repo, "add", "feature.txt")
		runGit(t, repo, "commit", "-m", "feature")
		runGit(t, repo, "checkout", "main")
		writeTestFile(t, filepath.Join(repo, "main.txt"), "main\n")
		runGit(t, repo, "add", "main.txt")
		runGit(t, repo, "commit", "-m", "main")
		runGit(t, repo, "merge", "--no-commit", "--no-ff", "feature")
		pf, err := svc.Preflight(context.Background(), "app1")
		if err != nil || !hasIssue(pf, "repository_operation") {
			t.Fatalf("expected repository_operation: %v %+v", err, pf.BlockingIssues)
		}
	})

	t.Run("remote ahead", func(t *testing.T) {
		svc, repo, cleanup := newReleaseFixture(t)
		defer cleanup()
		other := filepath.Join(t.TempDir(), "other")
		remote := strings.TrimSpace(runGit(t, repo, "remote", "get-url", "origin"))
		runGit(t, t.TempDir(), "clone", "--branch", "main", remote, other)
		runGit(t, other, "config", "user.name", "Remote Test")
		runGit(t, other, "config", "user.email", "remote-test@example.invalid")
		writeTestFile(t, filepath.Join(other, "remote.txt"), "remote\n")
		runGit(t, other, "add", "remote.txt")
		runGit(t, other, "commit", "-m", "remote")
		runGit(t, other, "push", "origin", "main")
		pf, err := svc.Preflight(context.Background(), "app1")
		if err != nil || !hasIssue(pf, "branch_behind") {
			t.Fatalf("expected branch_behind: %v %+v", err, pf.BlockingIssues)
		}
	})

	t.Run("local commits waiting to push", func(t *testing.T) {
		svc, repo, cleanup := newReleaseFixture(t)
		defer cleanup()
		writeTestFile(t, filepath.Join(repo, "local-new.txt"), "local\n")
		runGit(t, repo, "add", "local-new.txt")
		runGit(t, repo, "commit", "-m", "local only")
		pf, err := svc.Preflight(context.Background(), "app1")
		if err != nil || !pf.CanRelease {
			t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
		}
		if pf.AheadCount != 1 || len(pf.UnpushedChanges) != 1 || pf.UnpushedChanges[0].Path != "local-new.txt" || pf.UnpushedChanges[0].Status != "A" {
			t.Fatalf("unexpected unpushed state: ahead=%d changes=%+v", pf.AheadCount, pf.UnpushedChanges)
		}
	})
}

func TestStartBlocksDuplicateTagAndConcurrentRelease(t *testing.T) {
	t.Run("duplicate local tag", func(t *testing.T) {
		svc, repo, cleanup := newReleaseFixture(t)
		defer cleanup()
		runGit(t, repo, "checkout", "-b", "tag-holder")
		runGit(t, repo, "commit", "--allow-empty", "-m", "tag holder")
		runGit(t, repo, "tag", "-a", "v1.0.1", "-m", "existing")
		runGit(t, repo, "checkout", "main")
		writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
		pf, err := svc.Preflight(context.Background(), "app1")
		if err != nil || !pf.CanRelease {
			t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
		}
		_, err = svc.Start(context.Background(), "app1", CreateRequest{TargetVersion: "1.0.1", VersionMode: "manual", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true})
		pe, ok := err.(*Error)
		if !ok || pe.Code != "version_not_newer" {
			t.Fatalf("expected version_not_newer, got %#v", err)
		}
	})

	t.Run("repository lock", func(t *testing.T) {
		svc, repo, cleanup := newReleaseFixture(t)
		defer cleanup()
		writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
		pf, err := svc.Preflight(context.Background(), "app1")
		if err != nil || !pf.CanRelease {
			t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
		}
		if !svc.reserve(repo) {
			t.Fatal("failed to reserve test repository")
		}
		defer svc.release(repo)
		_, err = svc.Start(context.Background(), "app1", CreateRequest{TargetVersion: "1.0.1", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true})
		pe, ok := err.(*Error)
		if !ok || pe.Code != "release_in_progress" {
			t.Fatalf("expected release_in_progress, got %#v", err)
		}
	})
}

func TestRetryContinuesFromTagging(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "release commit\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "release commit")
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	version := store.ReleaseVersion{VersionGroupID: "repository", VersionGroupName: "项目版本", TargetVersion: "1.0.1", TagName: "v1.0.1"}
	planJSON, err := frozenTagExecutionPlan(true, version).marshal()
	if err != nil {
		t.Fatal(err)
	}
	run := &store.ReleaseRun{ID: "retry1", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin",
		TargetVersion: "1.0.1", TagName: "v1.0.1", CreateTag: true, PushRemote: true, Versions: []store.ReleaseVersion{version}, ExecutionPlan: planJSON,
		Status: "failed", Stage: "tagging", CommitSHA: sha}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Retry(run.ID, RetryRequest{ExternalActionsConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" || completed.Stage != "completed" {
		t.Fatalf("retry did not complete: %+v", completed)
	}
	remoteBranch := runGit(t, repo, "ls-remote", "origin", "refs/heads/main")
	remoteTag := runGit(t, repo, "ls-remote", "origin", "refs/tags/v1.0.1")
	if !strings.Contains(remoteBranch, sha) || strings.TrimSpace(remoteTag) == "" {
		t.Fatalf("retry refs missing: branch=%s tag=%s", remoteBranch, remoteTag)
	}
}

func waitRelease(t *testing.T, svc *Service, runID string) *store.ReleaseRun {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		run, err := svc.store.GetReleaseRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == "succeeded" || run.Status == "failed" {
			return run
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("release %s did not finish", runID)
	return nil
}

func newReleaseFixture(t *testing.T) (*Service, string, func()) {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	remote := filepath.Join(base, "remote.git")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, base, "init", "--bare", remote)
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Launcher Test")
	runGit(t, repo, "config", "user.email", "launcher-test@example.invalid")
	writeTestFile(t, filepath.Join(repo, "package.json"), "{\n  \"name\": \"demo\",\n  \"version\": \"1.0.0\"\n}\n")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "initial\n")
	writeTestFile(t, filepath.Join(repo, "start.bat"), "@echo off\r\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "main")

	db, err := store.Open(filepath.Join(base, "launcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := &store.App{ID: "app1", Name: "demo", EntryScript: filepath.Join(repo, "start.bat"), Cwd: repo,
		AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{},
		LastStatus: "stopped"}
	if err := db.CreateApp(a); err != nil {
		db.Close()
		t.Fatal(err)
	}
	svc := New(db)
	return svc, repo, func() { _ = db.Close() }
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasIssue(pf *Preflight, code string) bool {
	for _, issue := range pf.BlockingIssues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
