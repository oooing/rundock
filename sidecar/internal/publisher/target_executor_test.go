package publisher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestPlanTargetsValidatesSavedManifest(t *testing.T) {
	tests := []struct {
		name      string
		target    releaseconfig.Target
		selection store.ReleaseTargetSelection
		mutate    func(*releaseconfig.Config)
		wantCode  string
	}{
		{
			name: "target must exist", target: validExecutorTarget(),
			selection: store.ReleaseTargetSelection{TargetID: "missing", Build: true}, wantCode: "target_not_configured",
		},
		{
			name: "target must be enabled", target: func() releaseconfig.Target { v := validExecutorTarget(); v.Enabled = false; return v }(),
			selection: store.ReleaseTargetSelection{TargetID: "web", Build: true}, wantCode: "target_disabled",
		},
		{
			name: "selected command must exist", target: func() releaseconfig.Target { v := validExecutorTarget(); v.Steps.Deploy = ""; return v }(),
			selection: store.ReleaseTargetSelection{TargetID: "web", Deploy: true}, wantCode: "target_action_unconfigured",
		},
		{
			name: "working directory must exist", target: func() releaseconfig.Target { v := validExecutorTarget(); v.WorkingDir = "missing-dir"; return v }(),
			selection: store.ReleaseTargetSelection{TargetID: "web", Build: true}, wantCode: "target_working_dir_invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			cfg := validExecutorConfig(tt.target)
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
				t.Fatal(err)
			}
			_, err := svc.freezeExecutionPlan(context.Background(), "app1", repo, []store.ReleaseTargetSelection{tt.selection})
			pe, ok := err.(*Error)
			if !ok || pe.Code != tt.wantCode {
				t.Fatalf("plan error = %#v, want %s", err, tt.wantCode)
			}
		})
	}

	t.Run("detected proposal is executable without saving", func(t *testing.T) {
		svc, repo, cleanup := newReleaseFixture(t)
		defer cleanup()
		plan, err := svc.freezeExecutionPlan(context.Background(), "app1", repo, []store.ReleaseTargetSelection{{TargetID: "node", Package: true}})
		if err != nil {
			t.Fatalf("plan error = %#v", err)
		}
		if len(plan.Targets) != 1 || plan.Targets[0].ID != "node" || plan.Targets[0].Steps.Package == "" {
			t.Fatalf("detected plan = %#v", plan)
		}
		if _, err := os.Stat(filepath.Join(repo, releaseconfig.ManifestPath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("detected plan must not save a manifest, stat error = %v", err)
		}
	})
}

func TestTargetExecutorRunsOnlySelectedStepsInOrder(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runner := &recordingTargetRunner{}
	svc.targetRunner = runner
	selection := store.ReleaseTargetSelection{TargetID: "web", Build: true, Package: true, Publish: true, Deploy: true}
	plan := executorPlan(validExecutorTarget(), selection)
	run := createTargetExecutionRun(t, svc, repo, "run-order", selection)

	if err := svc.executeTargetPhase(context.Background(), run, plan, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.executeTargetPhase(context.Background(), run, plan, true); err != nil {
		t.Fatal(err)
	}
	if got, want := runner.Commands(), []string{"check-web", "build-web", "package-web", "publish-web", "deploy-web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
	states, err := svc.store.ReleaseTargetRuns(run.ID)
	if err != nil || len(states) != 1 {
		t.Fatalf("states = %#v, err=%v", states, err)
	}
	state := states[0]
	if state.Status != "succeeded" || !state.CheckDone || !state.BuildDone || !state.PackageDone || !state.PublishDone || !state.DeployDone {
		t.Fatalf("completed target state = %+v", state)
	}
}

func TestGitPushTargetTriggersCloudBuildWithoutLocalCommand(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runner := &recordingTargetRunner{}
	svc.targetRunner = runner
	target := validExecutorTarget()
	target.Runner = releaseconfig.Runner{Type: releaseconfig.RunnerGitPush, OS: []string{}}
	target.Steps = releaseconfig.Steps{Publish: "branch-push"}
	selection := store.ReleaseTargetSelection{TargetID: "web", Publish: true}
	plan := executorPlan(target, selection)
	run := createTargetExecutionRun(t, svc, repo, "run-cloud", selection)

	if err := svc.executeTargetChecks(context.Background(), run, plan); err != nil {
		t.Fatal(err)
	}
	if err := svc.executeTargetPhase(context.Background(), run, plan, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.executeTargetPhase(context.Background(), run, plan, true); err != nil {
		t.Fatal(err)
	}
	if got := runner.Commands(); len(got) != 0 {
		t.Fatalf("git-push target executed local commands: %#v", got)
	}
	states, err := svc.store.ReleaseTargetRuns(run.ID)
	if err != nil || len(states) != 1 || states[0].Status != "handed_off" || states[0].Stage != "cloud_pending" || !states[0].PublishDone {
		t.Fatalf("cloud target state = %#v, err=%v", states, err)
	}
}

func TestGitPushReleasePushesTagAndHandsOffWithoutLocalBuild(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()

	target := validExecutorTarget()
	target.Runner = releaseconfig.Runner{Type: releaseconfig.RunnerGitPush, OS: []string{}}
	target.Steps = releaseconfig.Steps{Publish: "tag-push"}
	cfg := validExecutorConfig(target)
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure cloud release")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "cloud release\n")

	runner := &recordingTargetRunner{}
	svc.targetRunner = runner
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag, pushRemote := true, true
	selection := store.ReleaseTargetSelection{TargetID: target.ID, Publish: true}
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "auto",
		SelectedPaths: []string{"tracked.txt"}, SelectedTargets: []store.ReleaseTargetSelection{selection},
		StatusFingerprint: pf.StatusFingerprint, ExternalActionsConfirmed: true,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.Stage != "completed" || run.TagName != "v1.0.1" {
		t.Fatalf("cloud release = %+v", run)
	}
	if got := runner.Commands(); len(got) != 0 {
		t.Fatalf("git-push release executed local commands: %#v", got)
	}
	if remoteBranch := runGit(t, repo, "ls-remote", "origin", "refs/heads/main"); !strings.Contains(remoteBranch, run.CommitSHA) {
		t.Fatalf("release commit was not pushed: %s", remoteBranch)
	}
	if remoteTag := runGit(t, repo, "ls-remote", "origin", "refs/tags/"+run.TagName+"^{}"); !strings.Contains(remoteTag, run.CommitSHA) {
		t.Fatalf("annotated release tag was not pushed: %s", remoteTag)
	}

	view, err := svc.GetRun(run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 1 || view.Targets[0].Status != "handed_off" || view.Targets[0].Stage != "cloud_pending" || !view.Targets[0].PublishDone {
		t.Fatalf("cloud target state = %#v", view.Targets)
	}
	if view.Automation == nil || view.Automation.State != "handed_off" || !strings.Contains(view.Automation.Message, "自动构建") {
		t.Fatalf("automation handoff = %#v", view.Automation)
	}

	contents := runGit(t, repo, "for-each-ref", "--format=%(contents)", "refs/tags/"+run.TagName)
	metadata := decodeTagReleasePlan(t, contents)
	if len(metadata.Targets) != 1 || metadata.Targets[0].ID != target.ID || !metadata.Targets[0].Publish || metadata.Targets[0].Build || metadata.Targets[0].Package || metadata.Targets[0].Deploy {
		t.Fatalf("cloud tag plan = %#v", metadata)
	}
}

func TestGitPushTagTargetRejectsReleaseWithoutTag(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()

	target := validExecutorTarget()
	target.Runner = releaseconfig.Runner{Type: releaseconfig.RunnerGitPush, OS: []string{}}
	target.Steps = releaseconfig.Steps{Publish: "tag-push"}
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(target)); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure tag-triggered cloud release")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "must create tag\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag, pushRemote := false, true
	_, err = svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote,
		SelectedPaths:     []string{"tracked.txt"},
		SelectedTargets:   []store.ReleaseTargetSelection{{TargetID: target.ID, Publish: true}},
		StatusFingerprint: pf.StatusFingerprint, ExternalActionsConfirmed: true,
	})
	pe, ok := err.(*Error)
	if !ok || pe.Code != "cloud_tag_required" || !strings.Contains(pe.Message, "创建版本 Tag") {
		t.Fatalf("tag-free cloud release error = %#v", err)
	}
	runs, listErr := svc.store.ListReleaseRuns("app1", 10)
	if listErr != nil || len(runs) != 0 {
		t.Fatalf("rejected release persisted a run: runs=%#v err=%v", runs, listErr)
	}
}

func TestExecutionPlanRecognizesRemotePushRequirement(t *testing.T) {
	local := &executionPlan{Targets: []planTarget{{Runner: releaseconfig.Runner{Type: releaseconfig.RunnerLocal}}}}
	if local.requiresGitPush() {
		t.Fatal("local target must not require a Git push")
	}
	cloud := &executionPlan{Targets: []planTarget{{Runner: releaseconfig.Runner{Type: releaseconfig.RunnerGitPush}}}}
	if !cloud.requiresGitPush() {
		t.Fatal("git-push target must require a Git push")
	}
}

func TestTargetExecutorRetrySkipsCompletedExternalSteps(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runner := &recordingTargetRunner{failOnce: map[string]bool{"deploy-web": true}}
	svc.targetRunner = runner
	selection := store.ReleaseTargetSelection{TargetID: "web", Publish: true, Deploy: true}
	plan := executorPlan(validExecutorTarget(), selection)
	run := createTargetExecutionRun(t, svc, repo, "run-resume", selection)

	if err := svc.executeTargetPhase(context.Background(), run, plan, false); err != nil {
		t.Fatal(err)
	}
	err := svc.executeTargetPhase(context.Background(), run, plan, true)
	targetErr, ok := err.(*targetExecutionError)
	if !ok || targetErr.Code != "target_step_failed" {
		t.Fatalf("first deploy error = %#v", err)
	}
	states, stateErr := svc.store.ReleaseTargetRuns(run.ID)
	if stateErr != nil || len(states) != 1 || !states[0].PublishDone || states[0].DeployDone {
		t.Fatalf("state after failed deploy = %#v, err=%v", states, stateErr)
	}

	runner.Reset()
	if err := svc.executeTargetPhase(context.Background(), run, plan, true); err != nil {
		t.Fatal(err)
	}
	if got, want := runner.Commands(), []string{"deploy-web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry commands = %#v, want %#v; publish must not run twice", got, want)
	}
	states, stateErr = svc.store.ReleaseTargetRuns(run.ID)
	if stateErr != nil || len(states) != 1 || states[0].Status != "succeeded" || !states[0].PublishDone || !states[0].DeployDone {
		t.Fatalf("state after retry = %#v, err=%v", states, stateErr)
	}
}

func TestTargetExecutorRecordsDeclaredArtifactsAndRejectsOtherChanges(t *testing.T) {
	t.Run("declared artifact", func(t *testing.T) {
		svc, repo, cleanup := newShortTargetExecutorFixture(t)
		defer cleanup()
		svc.targetRunner = &writingTargetRunner{relativePath: "dist/app.zip", content: "artifact"}
		selection := store.ReleaseTargetSelection{TargetID: "web", Build: true}
		target := validExecutorTarget()
		target.Steps.Check = ""
		target.Artifacts = []string{"dist/**"}
		plan := executorPlan(target, selection)
		run := createTargetExecutionRun(t, svc, repo, "run-artifact", selection)

		if err := svc.executeTargetPhase(context.Background(), run, plan, false); err != nil {
			t.Fatal(err)
		}
		artifacts, err := svc.store.ReleaseArtifacts(run.ID)
		if err != nil || len(artifacts) != 1 {
			t.Fatalf("artifacts = %#v, err=%v", artifacts, err)
		}
		artifact := artifacts[0]
		if artifact.TargetID != "web" || artifact.Path != "dist/app.zip" || artifact.SizeBytes != int64(len("artifact")) || len(artifact.SHA256) != 64 {
			t.Fatalf("artifact record = %+v", artifact)
		}
	})

	t.Run("undeclared worktree change", func(t *testing.T) {
		svc, repo, cleanup := newShortTargetExecutorFixture(t)
		defer cleanup()
		svc.targetRunner = &writingTargetRunner{relativePath: "unexpected.tmp", content: "side effect"}
		selection := store.ReleaseTargetSelection{TargetID: "web", Build: true}
		target := validExecutorTarget()
		target.Steps.Check = ""
		target.Artifacts = []string{"dist/**"}
		plan := executorPlan(target, selection)
		run := createTargetExecutionRun(t, svc, repo, "run-side-effect", selection)

		err := svc.executeTargetPhase(context.Background(), run, plan, false)
		targetErr, ok := err.(*targetExecutionError)
		if !ok || targetErr.Code != "build_changed_tree" || targetErr.Stage != "target_build" {
			t.Fatalf("side-effect error = %#v", err)
		}
	})
}

func TestExecutionPlanSnapshotDoesNotFollowLaterConfigEdits(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	initial := validExecutorConfig(validExecutorTarget())
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", initial); err != nil {
		t.Fatal(err)
	}
	plans, err := svc.freezeExecutionPlan(context.Background(), "app1", repo, []store.ReleaseTargetSelection{{TargetID: "web", Build: true}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plans.marshal()
	if err != nil {
		t.Fatal(err)
	}

	changedTarget := validExecutorTarget()
	changedTarget.Steps.Build = "different-build-command"
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(changedTarget)); err != nil {
		t.Fatal(err)
	}
	restored, err := parseExecutionPlan(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Targets) != 1 || restored.Targets[0].Steps.Build != "build-web" {
		t.Fatalf("execution plan changed after manifest edit: %+v", restored)
	}
}

func TestExecutionPlanAllowsIndependentVersionGroups(t *testing.T) {
	plan := &executionPlan{VersionGroups: []planVersionGroup{{ID: "client"}, {ID: "server"}}}
	if err := plan.validateVersionScope(true); err != nil {
		t.Fatalf("independent version groups should be releasable together: %v", err)
	}
}

func TestReleaseTargetPipelineOrdersPreAndPostPushActions(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(validExecutorTarget())); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure release targets")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "target pipeline\n")

	runner := &recordingTargetRunner{}
	svc.targetRunner = runner
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag := true
	selection := store.ReleaseTargetSelection{TargetID: "web", Build: true, Package: true, Publish: true, Deploy: true}
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, VersionMode: "auto", SelectedPaths: []string{"tracked.txt"},
		SelectedTargets: []store.ReleaseTargetSelection{selection}, StatusFingerprint: pf.StatusFingerprint,
		ExternalActionsConfirmed: true, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.TagName != "v1.0.1" {
		t.Fatalf("target release = %+v", run)
	}
	if got, want := runner.Commands(), []string{"check-web", "build-web", "package-web", "publish-web", "deploy-web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("target commands = %#v, want %#v", got, want)
	}

	view, err := svc.GetRun(run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Targets) != 1 || view.Targets[0].Status != "succeeded" || !view.Targets[0].DeployDone {
		t.Fatalf("target view = %#v", view.Targets)
	}
	texts := make([]string, 0, len(view.Logs))
	for _, log := range view.Logs {
		texts = append(texts, log.Text)
	}
	assertOrderedLogText(t, texts,
		stageText("versioning"),
		stageText("checking"),
		"Web："+stageText("target_check"),
		stageText("building_targets"),
		"Web："+stageText("target_build"),
		"Web："+stageText("target_package"),
		stageText("tagging"),
		stageText("pushing_branch"),
		stageText("pushing_tag"),
		stageText("publishing_targets"),
		"Web："+stageText("target_publish"),
		"Web："+stageText("target_deploy"),
	)
}

func TestTargetCheckFailureHappensBeforeVersionAndCommit(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(validExecutorTarget())); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure release targets")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "check must fail before commit\n")
	beforeHead := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	svc.targetRunner = &recordingTargetRunner{failOnce: map[string]bool{"check-web": true}}
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag := true
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, VersionMode: "auto", SelectedPaths: []string{"tracked.txt"},
		SelectedTargets: []store.ReleaseTargetSelection{{TargetID: "web", Build: true}}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "failed" || run.Stage != "target_check" || run.CommitSHA != "" {
		t.Fatalf("failed check run = %+v", run)
	}
	afterHead := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if afterHead != beforeHead {
		t.Fatalf("target check created commit: before=%s after=%s", beforeHead, afterHead)
	}
	assertFileContains(t, filepath.Join(repo, "package.json"), `"version": "1.0.0"`)
}

func TestReleaseRetryAfterDeployFailureDoesNotRepeatPublish(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(validExecutorTarget())); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure release targets")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "deploy retry\n")

	runner := &recordingTargetRunner{failOnce: map[string]bool{"deploy-web": true}}
	svc.targetRunner = runner
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag := false
	selection := store.ReleaseTargetSelection{TargetID: "web", Publish: true, Deploy: true}
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, SelectedPaths: []string{"tracked.txt"},
		SelectedTargets: []store.ReleaseTargetSelection{selection}, StatusFingerprint: pf.StatusFingerprint,
		ExternalActionsConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitRelease(t, svc, run.ID)
	if failed.Status != "failed" || failed.Stage != "target_deploy" || failed.ErrorCode != "target_step_failed" {
		t.Fatalf("failed deploy run = %+v", failed)
	}
	states, err := svc.store.ReleaseTargetRuns(run.ID)
	if err != nil || len(states) != 1 || !states[0].PublishDone || states[0].DeployDone {
		t.Fatalf("failed target state = %#v, err=%v", states, err)
	}

	runner.Reset()
	if _, err := svc.Retry(run.ID); err == nil {
		t.Fatal("deploy retry without renewed external-action confirmation was accepted")
	} else if pe, ok := err.(*Error); !ok || pe.Code != "external_actions_confirmation_required" {
		t.Fatalf("unconfirmed retry error = %#v", err)
	}
	if stillFailed, err := svc.store.GetReleaseRun(run.ID); err != nil || stillFailed.Status != "failed" {
		t.Fatalf("unconfirmed retry changed release state: run=%+v err=%v", stillFailed, err)
	}
	if _, err := svc.Retry(run.ID, RetryRequest{ExternalActionsConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" || completed.Stage != "completed" {
		t.Fatalf("retried run = %+v", completed)
	}
	if got, want := runner.Commands(), []string{"deploy-web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry commands = %#v, want %#v", got, want)
	}
	if tags := strings.TrimSpace(runGit(t, repo, "tag", "--list")); tags != "" {
		t.Fatalf("tag-free target retry created tags: %s", tags)
	}
}

func assertOrderedLogText(t *testing.T, texts []string, wanted ...string) {
	t.Helper()
	from := 0
	for _, needle := range wanted {
		found := -1
		for i := from; i < len(texts); i++ {
			if texts[i] == needle {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("log entry %q missing after index %d; logs=%#v", needle, from, texts)
		}
		from = found + 1
	}
}

type recordingTargetRunner struct {
	mu       sync.Mutex
	commands []string
	failOnce map[string]bool
}

type writingTargetRunner struct {
	relativePath string
	content      string
}

func (r *writingTargetRunner) Run(_ context.Context, dir, _ string, _ ...string) (string, error) {
	path := filepath.Join(dir, filepath.FromSlash(r.relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(r.content), 0o644); err != nil {
		return "", err
	}
	return "simulated output", nil
}

func (r *recordingTargetRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, error) {
	command := ""
	if len(args) > 0 {
		command = args[len(args)-1]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	if r.failOnce[command] {
		delete(r.failOnce, command)
		return "simulated failure", errors.New("simulated command failure")
	}
	return "simulated success", nil
}

func (r *recordingTargetRunner) Commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.commands...)
}

func (r *recordingTargetRunner) Reset() {
	r.mu.Lock()
	r.commands = nil
	r.mu.Unlock()
}

func validExecutorTarget() releaseconfig.Target {
	return releaseconfig.Target{
		ID: "web", Name: "Web", Kind: "web", VersionGroup: "product", WorkingDir: ".",
		Runner: releaseconfig.Runner{Type: "local", OS: []string{}}, Enabled: true, Confidence: 1,
		Steps: releaseconfig.Steps{
			Check: "check-web", Build: "build-web", Package: "package-web",
			Publish: "publish-web", Deploy: "deploy-web",
		},
		Artifacts: []string{},
	}
}

func validExecutorConfig(target releaseconfig.Target) *releaseconfig.Config {
	return &releaseconfig.Config{
		SchemaVersion: releaseconfig.SchemaVersion,
		VersionGroups: []releaseconfig.VersionGroup{{
			ID: "product", Name: "产品版本", CurrentVersion: "1.0.0",
			VersionFiles: []releaseconfig.VersionFile{{Path: "package.json", Format: "json", JSONPointer: "/version"}},
		}},
		Targets: []releaseconfig.Target{target}, Warnings: []string{},
	}
}

func executorPlan(target releaseconfig.Target, selection store.ReleaseTargetSelection) *executionPlan {
	return &executionPlan{
		SchemaVersion: executionPlanSchemaVersion,
		VersionGroups: []planVersionGroup{},
		Targets: []planTarget{{
			ID: target.ID, Name: target.Name, Kind: target.Kind, VersionGroup: target.VersionGroup,
			WorkingDir: target.WorkingDir, Runner: target.Runner, Steps: target.Steps,
			Artifacts: append([]string{}, target.Artifacts...), Selection: selection,
		}},
	}
}

func createTargetExecutionRun(t *testing.T, svc *Service, repo, runID string, selection store.ReleaseTargetSelection) *store.ReleaseRun {
	t.Helper()
	run := &store.ReleaseRun{
		ID: runID, AppID: "app1", RepoRoot: filepath.Clean(repo), Branch: "main", RemoteName: "origin",
		SelectedTargets: []store.ReleaseTargetSelection{selection}, Status: "running", Stage: "targets",
	}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.CreateReleaseTargetRuns(run.ID, run.SelectedTargets); err != nil {
		t.Fatal(err)
	}
	return run
}

func newShortTargetExecutorFixture(t *testing.T) (*Service, string, func()) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := filepath.Clean(filepath.Join(wd, "..", "..", ".tmp"))
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(tempRoot, "te-")
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "r")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		_ = os.RemoveAll(base)
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Launcher Test")
	runGit(t, repo, "config", "user.email", "launcher-test@example.invalid")
	writeTestFile(t, filepath.Join(repo, "package.json"), "{\n  \"name\": \"demo\",\n  \"version\": \"1.0.0\"\n}\n")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "initial\n")
	writeTestFile(t, filepath.Join(repo, "start.bat"), "@echo off\r\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")

	db, err := store.Open(filepath.Join(base, "db.sqlite"))
	if err != nil {
		_ = os.RemoveAll(base)
		t.Fatal(err)
	}
	if err := db.CreateApp(&store.App{
		ID: "app1", Name: "demo", EntryScript: filepath.Join(repo, "start.bat"), Cwd: repo,
		AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped",
	}); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(base)
		t.Fatal(err)
	}
	return New(db), repo, func() {
		_ = db.Close()
		_ = os.RemoveAll(base)
	}
}
