package publisher

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestBuildModeRejectsMixedExecution(t *testing.T) {
	for _, tc := range []struct {
		name, mode, runner string
		selection          store.ReleaseTargetSelection
		push, reject       bool
	}{
		{"cloud", "github", "git-push", store.ReleaseTargetSelection{Publish: true}, true, false},
		{"cloud cannot build locally", "github", "local", store.ReleaseTargetSelection{Build: true}, true, true},
		{"local", "local", "local", store.ReleaseTargetSelection{Build: true, Package: true}, false, false},
		{"local cannot upload", "local", "local", store.ReleaseTargetSelection{Build: true}, true, true},
		{"local cannot deploy", "local", "local", store.ReleaseTargetSelection{Deploy: true}, false, true},
		{"local cannot trigger cloud", "local", "git-push", store.ReleaseTargetSelection{Publish: true}, false, true},
		{"invalid mode", "elsewhere", "local", store.ReleaseTargetSelection{Build: true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := validExecutorTarget()
			target.Runner.Type = tc.runner
			err := validateBuildMode(tc.mode, executorPlan(target, tc.selection), tc.push)
			if (err != nil) != tc.reject {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestLocalBuildModeExecutesWithoutRemoteUpload(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	target := validExecutorTarget()
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(target)); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure local build")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "local changes\n")
	remoteBefore := runGit(t, repo, "ls-remote", "origin", "refs/heads/main")
	runner := &recordingTargetRunner{}
	svc.targetRunner = runner
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	no := false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{BuildMode: "local", CreateTag: &no, PushRemote: &no, VersionMode: "auto", SelectedPaths: []string{"tracked.txt"}, SelectedTargets: []store.ReleaseTargetSelection{{TargetID: target.ID, Build: true, Package: true}}, StatusFingerprint: pf.StatusFingerprint, CommitMessage: "local build"})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" {
		t.Fatalf("local build: %+v", run)
	}
	if got := runner.Commands(); !reflect.DeepEqual(got, []string{"check-web", "build-web", "package-web"}) {
		t.Fatalf("local commands: %v", got)
	}
	if after := runGit(t, repo, "ls-remote", "origin", "refs/heads/main"); after != remoteBefore {
		t.Fatal("local build uploaded commit")
	}
	plan, err := parseExecutionPlan(run.ExecutionPlan)
	if err != nil || plan.BuildMode != "local" {
		t.Fatalf("mode was not frozen: %+v %v", plan, err)
	}
}
