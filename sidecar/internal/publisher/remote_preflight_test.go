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

type networkTraceRunner struct {
	mu            sync.Mutex
	calls         []string
	deadlines     []time.Time
	forbidNetwork bool
	forbidTags    bool
}

func (r *networkTraceRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" && len(args) > 2 && contains([]string{"fetch", "ls-remote", "push"}, args[2]) {
		r.mu.Lock()
		r.calls = append(r.calls, args[2])
		if args[2] != "push" {
			deadline, _ := ctx.Deadline()
			r.deadlines = append(r.deadlines, deadline)
		}
		r.mu.Unlock()
		if r.forbidNetwork || (r.forbidTags && args[2] == "ls-remote") {
			return "unexpected remote command", errors.New("network forbidden by test")
		}
	}
	return execRunner{}.Run(ctx, dir, name, args...)
}

func (r *networkTraceRunner) count(action string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, value := range r.calls {
		if value == action {
			n++
		}
	}
	return n
}

func TestLocalReleaseWorksWithoutUsableRemote(t *testing.T) {
	for _, remote := range []string{"missing", "unreachable"} {
		for _, tag := range []bool{false, true} {
			t.Run(remote+map[bool]string{false: "/commit", true: "/tag"}[tag], func(t *testing.T) {
				svc, repo, cleanup := newReleaseFixture(t)
				defer cleanup()
				if remote == "missing" {
					runGit(t, repo, "remote", "remove", "origin")
				} else {
					runGit(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
				}
				runner := &networkTraceRunner{forbidNetwork: true}
				svc.runner = runner
				writeTestFile(t, filepath.Join(repo, "tracked.txt"), "local work\n")
				pf, err := svc.PreflightLocal(context.Background(), "app1")
				if err != nil || !pf.CanRelease || pf.RemoteChecked {
					t.Fatalf("local preflight: %+v %v", pf, err)
				}
				push := false
				run, err := svc.Start(context.Background(), "app1", CreateRequest{
					CreateTag: &tag, PushRemote: &push, VersionMode: "auto", TargetVersion: pf.SuggestedVersion,
					SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
					ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				if completed := waitRelease(t, svc, run.ID); completed.Status != "succeeded" {
					t.Fatalf("local release failed: %+v", completed)
				}
				if runner.count("fetch")+runner.count("ls-remote")+runner.count("push") != 0 {
					t.Fatalf("local release contacted remote: %+v", runner.calls)
				}
				if tags := strings.TrimSpace(runGit(t, repo, "tag", "--list")); (tags != "") != tag {
					t.Fatalf("wrong tag result: %q", tags)
				}
			})
		}
	}
}

func TestPlainCommitIgnoresBrokenReleaseConfiguration(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runGit(t, repo, "remote", "remove", "origin")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, releaseconfig.ManifestPath)), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, releaseconfig.ManifestPath), "not valid JSON")
	writeTestFile(t, filepath.Join(repo, "package.json"), "broken package JSON")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "ordinary change\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(pf, "release_config_invalid") {
		t.Fatalf("fixture must have invalid release config: %+v", pf)
	}
	no := false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{CreateTag: &no, PushRemote: &no,
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRelease(t, svc, run.ID); completed.Status != "succeeded" {
		t.Fatalf("plain commit failed: %+v", completed)
	}
	if got := strings.TrimSpace(runGit(t, repo, "show", "HEAD:tracked.txt")); got != "ordinary change" {
		t.Fatalf("commit missing: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "package.json")); string(got) != "broken package JSON" {
		t.Fatalf("plain commit rewrote unrelated file: %q", got)
	}
}

func TestPushingPlainCommitDoesNotCheckTags(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "push only\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	runner := &networkTraceRunner{forbidTags: true}
	svc.runner = runner
	no, yes := false, true
	run, err := svc.Start(context.Background(), "app1", CreateRequest{CreateTag: &no, PushRemote: &yes,
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRelease(t, svc, run.ID); completed.Status != "succeeded" {
		t.Fatalf("plain push failed: %+v", completed)
	}
	if runner.count("fetch") != 0 || runner.count("ls-remote") != 0 || runner.count("push") != 1 {
		t.Fatalf("wrong network actions: %#v", runner.calls)
	}
}

func TestLocalVersionChangeRequiresConfirmationBeforeMutation(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "keep pending\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "tag", "v1.0.2", "main")
	head := runGit(t, repo, "rev-parse", "HEAD")
	runner := &networkTraceRunner{}
	svc.runner = runner
	yes := true
	req := CreateRequest{CreateTag: &yes, PushRemote: &yes, VersionMode: "auto", TargetVersion: pf.SuggestedVersion,
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true}
	_, err = svc.Start(context.Background(), "app1", req)
	pe, ok := err.(*Error)
	if !ok || pe.Code != "version_plan_changed" || pe.Preflight == nil || pe.Preflight.SuggestedVersion != "1.0.3" {
		t.Fatalf("version drift error: %#v", err)
	}
	if runGit(t, repo, "rev-parse", "HEAD") != head || strings.TrimSpace(runGit(t, repo, "tag", "--list")) != "v1.0.2" {
		t.Fatal("unconfirmed version changed repository")
	}
	if runs, _ := svc.store.ListReleaseRuns("app1", 10); len(runs) != 0 {
		t.Fatalf("created run before confirmation: %+v", runs)
	}
	if runner.count("fetch") != 0 || runner.count("ls-remote") != 0 {
		t.Fatalf("duplicate network check: %#v", runner.calls)
	}
	req.TargetVersion = pe.Preflight.SuggestedVersion
	run, err := svc.Start(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitRelease(t, svc, run.ID); completed.Status != "succeeded" || completed.TagName != "v1.0.3" {
		t.Fatalf("confirmed version failed: %+v", completed)
	}
	if runner.count("fetch") != 0 || runner.count("ls-remote") != 0 {
		t.Fatalf("extra per-version network check: %#v", runner.calls)
	}
}

func TestLocalBuildAndCloudPushRequirementWithoutRemote(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "cloud"}[cloud], func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			target := validExecutorTarget()
			selection := store.ReleaseTargetSelection{TargetID: target.ID, Build: true}
			if cloud {
				target.Runner = releaseconfig.Runner{Type: releaseconfig.RunnerGitPush, OS: []string{}}
				target.Steps = releaseconfig.Steps{Publish: "branch-push"}
				selection = store.ReleaseTargetSelection{TargetID: target.ID, Publish: true}
			}
			if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(target)); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "add", releaseconfig.ManifestPath)
			runGit(t, repo, "commit", "-m", "configure build")
			runGit(t, repo, "remote", "remove", "origin")
			writeTestFile(t, filepath.Join(repo, "tracked.txt"), "build locally\n")
			runner := &networkTraceRunner{forbidNetwork: true}
			svc.runner = runner
			targetRunner := &recordingTargetRunner{}
			svc.targetRunner = targetRunner
			pf, err := svc.PreflightLocal(context.Background(), "app1")
			if err != nil || !pf.CanRelease {
				t.Fatalf("preflight: %+v %v", pf, err)
			}
			no := false
			run, err := svc.Start(context.Background(), "app1", CreateRequest{CreateTag: &no, PushRemote: &no,
				SelectedPaths: []string{"tracked.txt"}, SelectedTargets: []store.ReleaseTargetSelection{selection},
				StatusFingerprint: pf.StatusFingerprint, ExternalActionsConfirmed: true})
			if cloud {
				if pe, ok := err.(*Error); !ok || pe.Code != "remote_push_required" {
					t.Fatalf("cloud without upload: %#v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if completed := waitRelease(t, svc, run.ID); completed.Status != "succeeded" {
					t.Fatalf("local build failed: %+v", completed)
				}
				if !contains(targetRunner.Commands(), "build-web") {
					t.Fatalf("local build not run: %+v", targetRunner.Commands())
				}
			}
			if runner.count("fetch")+runner.count("ls-remote")+runner.count("push") != 0 {
				t.Fatalf("unexpected network: %+v", runner.calls)
			}
		})
	}
}

func TestRemoteFailureCategoriesAndRedaction(t *testing.T) {
	tests := []struct{ output, code string }{
		{"fatal: Authentication failed for https://alice:secret@host/repo?access_token=private", "remote_auth_failed"},
		{"Permission denied (publickey).", "remote_auth_failed"},
		{"fatal: couldn't find remote ref feature", "remote_branch_missing"},
		{"fatal: unable to access https://host/: Could not resolve host: host", "remote_network_failed"},
		{"Connection timed out after 20000 milliseconds", "remote_timeout"},
		{"fatal: something unexpected", "remote_check_failed"},
		{"fatal: C:/fixture403123/missing.git does not appear to be a git repository", "remote_check_failed"},
		{"fatal: unable to access https://host/: The requested URL returned error: 403", "remote_auth_failed"},
	}
	for _, tt := range tests {
		issue := remoteFailure(context.Background(), "获取远程分支", tt.output, errors.New("exit status 128"))
		if issue.Code != tt.code || !strings.Contains(issue.Message, "\n") {
			t.Fatalf("classification: %+v", issue)
		}
		if strings.Contains(issue.Message, "secret") || strings.Contains(issue.Message, "private") {
			t.Fatalf("credential leaked: %s", issue.Message)
		}
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if issue := remoteFailure(ctx, "检查远程 Tag", "", errors.New("signal: killed")); issue.Code != "remote_timeout" {
		t.Fatalf("timeout error lost: %+v", issue)
	}
}

func TestLocalPreflightShowsCachedUnpushedChanges(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "already committed locally\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "local commit")
	runner := &networkTraceRunner{forbidNetwork: true}
	svc.runner = runner
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil || !pf.CanRelease || pf.RemoteChecked || pf.AheadCount != 1 || len(pf.UnpushedChanges) != 1 || pf.UnpushedChanges[0].Path != "tracked.txt" {
		t.Fatalf("cached changes: %+v err=%v", pf, err)
	}
	if runner.count("fetch")+runner.count("ls-remote") != 0 {
		t.Fatalf("cached check contacted remote: %+v", runner.calls)
	}
}

func TestLocalNamespacedVersionChangeKeepsRequestedGroupUnmodified(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	cfg := validExecutorConfig(validExecutorTarget())
	cfg.VersionGroups[0].TagPrefix = "web"
	cfg.VersionGroups = append(cfg.VersionGroups, releaseconfig.VersionGroup{ID: "server", Name: "Server", TagPrefix: "server", CurrentVersion: "3.4.0", VersionFiles: []releaseconfig.VersionFile{}})
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure versions")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "unconfirmed group version\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("local preflight: %+v %v", pf, err)
	}
	runGit(t, repo, "tag", "web/v1.0.2", "main")
	yes := true
	_, err = svc.Start(context.Background(), "app1", CreateRequest{CreateTag: &yes, PushRemote: &yes, VersionMode: "auto",
		Versions:      []ReleaseVersionInput{{VersionGroupID: "product", TargetVersion: pf.SuggestedVersions["product"]}},
		SelectedPaths: []string{"tracked.txt"}, SelectedTargets: []store.ReleaseTargetSelection{{TargetID: "web", Build: true}},
		StatusFingerprint: pf.StatusFingerprint, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true})
	pe, ok := err.(*Error)
	if !ok || pe.Code != "version_plan_changed" || pe.Preflight == nil || pe.Preflight.SuggestedVersions["product"] != "1.0.3" {
		t.Fatalf("namespaced drift error: %#v", err)
	}
	if strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")) != pf.HeadSHA {
		t.Fatal("unconfirmed version created a commit")
	}
	if runs, _ := svc.store.ListReleaseRuns("app1", 10); len(runs) != 0 {
		t.Fatalf("unconfirmed version created a run: %+v", runs)
	}
	assertFileContains(t, filepath.Join(repo, "package.json"), `"version": "1.0.0"`)
}

type stalledRemoteRunner struct{ called int }

func (r *stalledRemoteRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.called++
	<-ctx.Done()
	return "", errors.New("signal: killed")
}

func TestRemoteCheckCancellationStopsBeforeTagQuery(t *testing.T) {
	runner := &stalledRemoteRunner{}
	svc := &Service{runner: runner}
	pf := &Preflight{RepoRoot: t.TempDir(), RemoteName: "origin", Branch: "main"}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	svc.checkRemote(ctx, pf, true)
	if time.Since(started) > time.Second || runner.called != 1 || !hasIssue(pf, "remote_timeout") {
		t.Fatalf("remote timeout did not stop the check: elapsed=%v calls=%d issues=%+v", time.Since(started), runner.called, pf.BlockingIssues)
	}
}
