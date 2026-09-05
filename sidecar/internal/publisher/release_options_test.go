package publisher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

type versionAwareRunner struct {
	delegate execRunner
	want     string
	mu       sync.Mutex
	saw      bool
}

func (r *versionAwareRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "cmd.exe" || name == "/bin/sh" {
		raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil || !strings.Contains(string(raw), `"version": "`+r.want+`"`) {
			return "", fmt.Errorf("check saw the wrong package version: %s", raw)
		}
		r.mu.Lock()
		r.saw = true
		r.mu.Unlock()
		return "", nil
	}
	return r.delegate.Run(ctx, dir, name, args...)
}

func (r *versionAwareRunner) SawCheck() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saw
}

func TestVersioningRunsBeforePreReleaseCheck(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	if err := svc.SaveProfile(&store.ReleaseProfile{
		AppID: "app1", RemoteName: "origin", VersionStrategy: StrategyAuto,
		PreReleaseCommand: "verify-version", CreateTag: true, VersionMode: "auto",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "check version order\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	runner := &versionAwareRunner{want: pf.SuggestedVersion}
	svc.runner = runner
	createTag, pushRemote := true, false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "auto",
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || !runner.SawCheck() {
		t.Fatalf("version-aware check did not complete: run=%+v saw=%t", run, runner.SawCheck())
	}
	view, err := svc.GetRun(run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]int{}
	for index, log := range view.Logs {
		positions[log.Text] = index
	}
	versioning, hasVersioning := positions[stageText("versioning")]
	checking, hasChecking := positions[stageText("checking")]
	committing, hasCommitting := positions[stageText("committing")]
	if !hasVersioning || !hasChecking || !hasCommitting || !(versioning < checking && checking < committing) {
		t.Fatalf("unexpected release phase order: %#v", positions)
	}
}

func TestReleaseWithoutTagSkipsVersionAndTagStages(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "ordinary publish\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag := false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, VersionMode: "auto", TargetVersion: "not-a-version",
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.CreateTag || run.TargetVersion != "" || run.TagName != "" {
		t.Fatalf("tag-free run = %+v", run)
	}

	packageJSON, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packageJSON), `"version": "1.0.0"`) {
		t.Fatalf("tag-free publish changed the version file: %s", packageJSON)
	}
	if tags := strings.TrimSpace(runGit(t, repo, "tag", "--list")); tags != "" {
		t.Fatalf("tag-free publish created local tags: %s", tags)
	}
	if tags := strings.TrimSpace(runGit(t, repo, "ls-remote", "--tags", "origin")); tags != "" {
		t.Fatalf("tag-free publish pushed remote tags: %s", tags)
	}
	remoteBranch := runGit(t, repo, "ls-remote", "origin", "refs/heads/main")
	if !strings.Contains(remoteBranch, run.CommitSHA) {
		t.Fatalf("branch was not pushed: %s", remoteBranch)
	}

	view, err := svc.GetRun(run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range view.Logs {
		if log.Text == stageText("versioning") || log.Text == stageText("tagging") || log.Text == stageText("pushing_tag") {
			t.Fatalf("tag-free publish entered a skipped stage: %+v", log)
		}
	}
}

func TestReleaseCanStayLocalWithoutPushingBranchOrTag(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	remoteBefore := strings.TrimSpace(runGit(t, repo, "rev-parse", "origin/main"))
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "local release\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag, pushRemote := true, false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "auto",
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.PushRemote {
		t.Fatalf("local-only run = %+v", run)
	}
	if remoteAfter := strings.TrimSpace(runGit(t, repo, "rev-parse", "origin/main")); remoteAfter != remoteBefore {
		t.Fatalf("remote branch changed: before=%s after=%s", remoteBefore, remoteAfter)
	}
	if tag := strings.TrimSpace(runGit(t, repo, "tag", "--list", run.TagName)); tag != run.TagName {
		t.Fatalf("local tag missing: %q", tag)
	}
	if remoteTag := strings.TrimSpace(runGit(t, repo, "ls-remote", "--tags", "origin", "refs/tags/"+run.TagName)); remoteTag != "" {
		t.Fatalf("tag unexpectedly pushed: %s", remoteTag)
	}
	view, err := svc.GetRun(run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range view.Logs {
		if log.Text == stageText("pushing_branch") || log.Text == stageText("pushing_tag") {
			t.Fatalf("local-only release entered push stage: %+v", log)
		}
	}
}

func TestReleaseInheritsRememberedTagChoice(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	if err := svc.SaveProfile(&store.ReleaseProfile{
		AppID: "app1", RemoteName: "origin", VersionStrategy: StrategyAuto,
		CreateTag: false, VersionMode: "manual",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "remembered tag choice\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	if pf.Profile.CreateTag || pf.Profile.VersionMode != "manual" {
		t.Fatalf("preflight profile = %+v", pf.Profile)
	}

	// CreateTag is intentionally nil: Start must inherit the per-project profile.
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.CreateTag || run.TagName != "" {
		t.Fatalf("run did not inherit createTag=false: %+v", run)
	}
}

func TestAutoVersionUsesPreflightSuggestion(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "automatic version\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag := true
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, VersionMode: "auto",
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.TargetVersion != pf.SuggestedVersion || run.TagName != "v"+pf.SuggestedVersion {
		t.Fatalf("auto-versioned run = %+v, suggested=%s", run, pf.SuggestedVersion)
	}
}

func TestValidateTargetSelections(t *testing.T) {
	valid := []store.ReleaseTargetSelection{
		{TargetID: "web", Build: true, Publish: true},
		{TargetID: "server", Deploy: true}, // redeploy is intentionally allowed without rebuilding
	}
	got, err := validateTargetSelections(valid)
	if err != nil || len(got) != len(valid) || got[1].TargetID != "server" || !got[1].Deploy {
		t.Fatalf("valid target combination = %#v, err=%v", got, err)
	}

	tests := []struct {
		name string
		in   []store.ReleaseTargetSelection
		code string
	}{
		{name: "empty id", in: []store.ReleaseTargetSelection{{Build: true}}, code: "invalid_target"},
		{name: "path-like id", in: []store.ReleaseTargetSelection{{TargetID: "../web", Build: true}}, code: "invalid_target"},
		{name: "duplicate", in: []store.ReleaseTargetSelection{{TargetID: "web", Build: true}, {TargetID: "web", Deploy: true}}, code: "duplicate_target"},
		{name: "no action", in: []store.ReleaseTargetSelection{{TargetID: "web"}}, code: "target_action_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateTargetSelections(tt.in)
			pe, ok := err.(*Error)
			if !ok || pe.Code != tt.code {
				t.Fatalf("error = %#v, want code %s", err, tt.code)
			}
		})
	}
}

func TestRetryTagFreeBranchPushDoesNotCreateTag(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "retry commit\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "retry commit")
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	run := &store.ReleaseRun{
		ID: "retry-no-tag", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin",
		CreateTag: false, Status: "failed", Stage: "pushing_branch", CommitSHA: sha,
	}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Retry(run.ID, RetryRequest{ExternalActionsConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" || completed.Stage != "completed" {
		t.Fatalf("tag-free retry did not complete: %+v", completed)
	}
	if tags := strings.TrimSpace(runGit(t, repo, "tag", "--list")); tags != "" {
		t.Fatalf("tag-free retry created tags: %s", tags)
	}
}
