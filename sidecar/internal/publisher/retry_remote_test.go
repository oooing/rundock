package publisher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

type retryNetworkRecorder struct {
	mu        sync.Mutex
	pushes    []string
	reads     []string
	deadlines []time.Time
	failReads bool
}

func (r *retryNetworkRecorder) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" && len(args) > 2 {
		action := args[2]
		if action == "push" || action == "ls-remote" || action == "fetch" {
			r.mu.Lock()
			if action == "push" {
				r.pushes = append(r.pushes, strings.Join(args[3:], " "))
			} else {
				r.reads = append(r.reads, action)
				deadline, _ := ctx.Deadline()
				r.deadlines = append(r.deadlines, deadline)
			}
			r.mu.Unlock()
			if r.failReads && action != "push" {
				return "fatal: Authentication failed for https://user:secret@host/repo", errors.New("exit status 128")
			}
		}
	}
	return execRunner{}.Run(ctx, dir, name, args...)
}

func newFailedGitRetry(t *testing.T, count int) (*Service, string, string, *store.ReleaseRun, *executionPlan, func()) {
	t.Helper()
	svc, repo, cleanup := newReleaseFixture(t)
	remote := strings.TrimSpace(runGit(t, repo, "remote", "get-url", "origin"))
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "frozen retry commit\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "frozen release")
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	versions := []store.ReleaseVersion{}
	for _, prefix := range []string{"web", "server"}[:count] {
		versions = append(versions, store.ReleaseVersion{VersionGroupID: prefix, TargetVersion: "1.0.1", TagName: prefix + "/v1.0.1"})
	}
	plan := frozenTagExecutionPlan(true, versions...)
	plan.RemoteURL = redact(remote)
	planJSON, _ := plan.marshal()
	run := &store.ReleaseRun{ID: "retry-fixture", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin", CommitSHA: sha,
		CreateTag: count > 0, PushRemote: true, Versions: versions, ExecutionPlan: planJSON, Status: "failed", Stage: "pushing_branch"}
	if len(versions) > 0 {
		run.TagName = versions[0].TagName
		run.TargetVersion = versions[0].TargetVersion
	}
	for _, version := range versions {
		if err := svc.ensureFrozenTag(context.Background(), run, plan, version, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	return svc, repo, remote, run, plan, cleanup
}

func TestRetryGitUploadsOnlyMissingFrozenRefs(t *testing.T) {
	for _, scenario := range []string{"nothing uploaded", "everything uploaded", "one tag uploaded"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, _, run, _, cleanup := newFailedGitRetry(t, 2)
			defer cleanup()
			wantPushes := 3
			if scenario != "nothing uploaded" {
				runGit(t, repo, "push", "origin", "main", "refs/tags/"+run.Versions[0].TagName)
				wantPushes = 1
				if scenario == "everything uploaded" {
					runGit(t, repo, "push", "origin", "refs/tags/"+run.Versions[1].TagName)
					wantPushes = 0
				}
				_ = svc.store.UpdateReleaseRun(run.ID, "failed", "pushing_tag", run.CommitSHA, "push_tag_failed", "connection lost", true)
			}
			beforeRefs := runGit(t, repo, "show-ref")
			recorder := &retryNetworkRecorder{}
			svc.runner = recorder
			view, err := svc.GetRun(run.ID, 0)
			if err != nil || view.RetryConfirmationRequired {
				t.Fatalf("Git retry should not ask for confirmation: %+v %v", view, err)
			}
			if _, err := svc.Retry(run.ID); err != nil {
				t.Fatal(err)
			}
			completed := waitRelease(t, svc, run.ID)
			if completed.Status != "succeeded" {
				t.Fatalf("retry failed: %+v", completed)
			}
			if len(recorder.pushes) != wantPushes || len(recorder.reads) != 1 || recorder.reads[0] != "ls-remote" {
				t.Fatalf("unexpected retry operations: pushes=%+v reads=%+v", recorder.pushes, recorder.reads)
			}
			for _, push := range recorder.pushes {
				if strings.Contains(push, "--force") || strings.Contains(push, "+refs/") {
					t.Fatalf("force push forbidden: %s", push)
				}
			}
			if scenario != "nothing uploaded" && runGit(t, repo, "show-ref") != beforeRefs {
				t.Fatal("verification changed local refs")
			}
			remoteRefs := runGit(t, repo, "ls-remote", "origin")
			if !strings.Contains(remoteRefs, run.CommitSHA+"\trefs/heads/main") {
				t.Fatalf("branch missing: %s", remoteRefs)
			}
			for _, version := range run.Versions {
				if !strings.Contains(remoteRefs, "refs/tags/"+version.TagName+"^{}") {
					t.Fatalf("tag missing: %s", remoteRefs)
				}
			}
		})
	}
}

func TestRetryConflictingRemoteTagStopsAllUploads(t *testing.T) {
	for _, kind := range []string{"different commit", "different notes", "lightweight"} {
		t.Run(kind, func(t *testing.T) {
			svc, repo, remote, run, _, cleanup := newFailedGitRetry(t, 2)
			defer cleanup()
			runGit(t, remote, "config", "user.name", "Remote Test")
			runGit(t, remote, "config", "user.email", "remote@example.invalid")
			// Transfer the frozen commit without changing the destination branch.
			runGit(t, repo, "push", "origin", run.CommitSHA+":refs/heads/object-transfer")
			tag := run.Versions[1].TagName
			if kind == "lightweight" {
				runGit(t, remote, "tag", tag, run.CommitSHA)
			} else {
				ref := run.CommitSHA
				if kind == "different commit" {
					ref = "main"
				}
				runGit(t, remote, "tag", "-a", tag, ref, "-m", "different release notes")
			}
			beforeRemote := runGit(t, remote, "show-ref")
			beforeLocal := runGit(t, repo, "show-ref")
			recorder := &retryNetworkRecorder{}
			svc.runner = recorder
			if _, err := svc.Retry(run.ID); err != nil {
				t.Fatal(err)
			}
			failed := waitRelease(t, svc, run.ID)
			if failed.Status != "failed" || failed.Stage != "pushing_branch" || failed.ErrorCode != "remote_tag_conflict" || len(recorder.pushes) != 0 {
				t.Fatalf("conflicting tag retry: %+v pushes=%+v", failed, recorder.pushes)
			}
			if runGit(t, remote, "show-ref") != beforeRemote || runGit(t, repo, "show-ref") != beforeLocal {
				t.Fatal("conflict verification changed refs")
			}
		})
	}
}

func TestRetryCannotReadRemoteDoesNotBlindlyPush(t *testing.T) {
	svc, repo, remote, run, _, cleanup := newFailedGitRetry(t, 1)
	defer cleanup()
	beforeRemote, beforeLocal := runGit(t, remote, "show-ref"), runGit(t, repo, "show-ref")
	recorder := &retryNetworkRecorder{failReads: true}
	svc.runner = recorder
	if _, err := svc.Retry(run.ID); err != nil {
		t.Fatal(err)
	}
	failed := waitRelease(t, svc, run.ID)
	if failed.Status != "failed" || failed.Stage != run.Stage || failed.ErrorCode != "remote_auth_failed" || len(recorder.pushes) != 0 || strings.Contains(failed.ErrorMessage, "secret") {
		t.Fatalf("failed remote check: %+v pushes=%+v", failed, recorder.pushes)
	}
	if runGit(t, remote, "show-ref") != beforeRemote || runGit(t, repo, "show-ref") != beforeLocal {
		t.Fatal("network failure changed refs")
	}
}

func TestRetrySkipsRemoteBranchThatAlreadyContainsRelease(t *testing.T) {
	svc, repo, remote, run, _, cleanup := newFailedGitRetry(t, 1)
	defer cleanup()
	runGit(t, repo, "push", "origin", "main")
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", "--branch", "main", remote, other)
	runGit(t, other, "config", "user.name", "Other Developer")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	writeTestFile(t, filepath.Join(other, "newer.txt"), "newer commit\n")
	runGit(t, other, "add", "newer.txt")
	runGit(t, other, "commit", "-m", "newer work")
	runGit(t, other, "push", "origin", "main")
	newer := strings.TrimSpace(runGit(t, remote, "rev-parse", "main"))
	beforeLocal := runGit(t, repo, "show-ref")
	fetchHeadPath := filepath.Join(repo, ".git", "FETCH_HEAD")
	writeTestFile(t, fetchHeadPath, "keep existing FETCH_HEAD\n")
	recorder := &retryNetworkRecorder{}
	svc.runner = recorder
	if _, err := svc.Retry(run.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" || len(recorder.pushes) != 1 || strings.Contains(recorder.pushes[0], "refs/heads") {
		t.Fatalf("newer remote retry: %+v pushes=%+v", completed, recorder.pushes)
	}
	if strings.TrimSpace(runGit(t, remote, "rev-parse", "main")) != newer || runGit(t, repo, "show-ref") != beforeLocal {
		t.Fatal("remote verification moved a branch")
	}
	if data, _ := os.ReadFile(fetchHeadPath); string(data) != "keep existing FETCH_HEAD\n" {
		t.Fatalf("FETCH_HEAD changed: %s", data)
	}
	if len(recorder.deadlines) != 2 || !recorder.deadlines[0].Equal(recorder.deadlines[1]) || time.Until(recorder.deadlines[0]) > remotePreflightTimeout {
		t.Fatalf("inspection does not share a bounded budget: %+v", recorder.deadlines)
	}
}

func TestRetryRejectsChangedRemoteDestination(t *testing.T) {
	svc, repo, _, run, _, cleanup := newFailedGitRetry(t, 1)
	defer cleanup()
	other := filepath.Join(t.TempDir(), "other.git")
	runGit(t, t.TempDir(), "init", "--bare", other)
	runGit(t, repo, "remote", "set-url", "origin", other)
	recorder := &retryNetworkRecorder{}
	svc.runner = recorder
	if _, err := svc.Retry(run.ID); err != nil {
		t.Fatal(err)
	}
	failed := waitRelease(t, svc, run.ID)
	if failed.Status != "failed" || failed.ErrorCode != "remote_destination_changed" || len(recorder.reads)+len(recorder.pushes) != 0 {
		t.Fatalf("changed remote was used: %+v reads=%+v pushes=%+v", failed, recorder.reads, recorder.pushes)
	}
}

func TestRetryConfirmationOnlyCoversPreviouslyAttemptedCustomSteps(t *testing.T) {
	run := &store.ReleaseRun{Status: "failed", Stage: "pushing_branch", CommitSHA: "frozen", PushRemote: true}
	local := planTarget{ID: "web", Name: "Web", Runner: releaseconfig.Runner{Type: releaseconfig.RunnerLocal}, Selection: store.ReleaseTargetSelection{Publish: true, Deploy: true}, Steps: releaseconfig.Steps{Publish: "upload-custom", Deploy: "deploy-custom"}}
	cloud := local
	cloud.ID = "cloud"
	cloud.Name = "Cloud"
	cloud.Runner.Type = releaseconfig.RunnerGitPush
	plan := &executionPlan{Targets: []planTarget{local, cloud}}
	for _, test := range []struct {
		name, status, stage, code string
		publishDone               bool
		want                      string
	}{
		{"not started", "waiting", "waiting_publish", "", false, ""},
		{"pre-execution check failed", "failed", "publish", "artifact_changed", false, ""},
		{"publish attempted", "failed", "publish", "target_step_failed", false, "Web：发布"},
		{"deploy attempted", "failed", "deploy", "target_step_failed", true, "Web：部署"},
		{"interrupted deploy", "running", "deploy", "", true, "Web：部署"},
		{"already completed", "succeeded", "completed", "", true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			states := []*store.ReleaseTargetRun{{TargetID: "web", Status: test.status, Stage: test.stage, ErrorCode: test.code, PublishDone: test.publishDone}, {TargetID: "cloud", Status: "failed", Stage: "publish", ErrorCode: "target_step_failed"}}
			if got := strings.Join(retryCustomExternalTargets(run, plan, states), ","); got != test.want {
				t.Fatalf("confirmation targets=%q want=%q", got, test.want)
			}
		})
	}
}

func TestRetryGitPushHandoffChecksUploadWithoutUserConfirmation(t *testing.T) {
	svc, _, _, run, plan, cleanup := newFailedGitRetry(t, 0)
	defer cleanup()
	selection := store.ReleaseTargetSelection{TargetID: "cloud", Publish: true}
	plan.Targets = []planTarget{{ID: "cloud", Name: "Cloud", WorkingDir: ".", Runner: releaseconfig.Runner{Type: releaseconfig.RunnerGitPush},
		Steps: releaseconfig.Steps{Publish: "branch-push"}, Selection: selection}}
	run.ID = "retry-cloud-handoff"
	run.Stage = "target_publish"
	run.SelectedTargets = []store.ReleaseTargetSelection{selection}
	run.ExecutionPlan, _ = plan.marshal()
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.CreateReleaseTargetRuns(run.ID, run.SelectedTargets); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.UpdateReleaseTargetRun(run.ID, "cloud", "failed", "publish", "connection_lost", "lost response", true, true); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetRun(run.ID, 0)
	if err != nil || view.RetryConfirmationRequired {
		t.Fatalf("cloud retry confirmation: %+v %v", view, err)
	}
	recorder := &retryNetworkRecorder{}
	svc.runner = recorder
	if _, err := svc.Retry(run.ID); err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" || len(recorder.reads) != 1 || len(recorder.pushes) != 1 {
		t.Fatalf("cloud upload was not reconciled: %+v reads=%+v pushes=%+v", completed, recorder.reads, recorder.pushes)
	}
	states, err := svc.store.ReleaseTargetRuns(run.ID)
	if err != nil || len(states) != 1 || !states[0].PublishDone || states[0].Status != "handed_off" {
		t.Fatalf("cloud state: %+v %v", states, err)
	}
}
