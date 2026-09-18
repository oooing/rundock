package publisher

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func skippedRequest(t *testing.T, svc *Service) CreateRequest {
	t.Helper()
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	req := CreateRequest{Intent: IntentFormal, SkipChecks: true, CreateTag: boolPtr(true), PushRemote: boolPtr(false), BuildMode: BuildModeNone, VersionMode: "manual", TargetVersion: "1.0.1", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint, CommitMessage: "release without checks", ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true}
	view, err := svc.PrepareCandidate(context.Background(), "app1", candidateRequestFromCreate(req, IntentFormal))
	if err != nil {
		t.Fatal(err)
	}
	if !candidateReleaseReady(view, true) || view.Status == CheckPassed {
		t.Fatalf("skip was not explicitly recorded: %+v", view)
	}
	req.CandidateID = view.ID
	return req
}

func TestSkipChecksStillFreezesAndPublishesSelectedContent(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{ID: "fail", Name: "Must not run", Command: "exit 9", Required: true}})
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "release change\n")
	writeTestFile(t, filepath.Join(repo, "private-note.txt"), "must remain local\n")
	svc.candidateCommand = func(context.Context, string, releaseconfig.CheckProfile) (string, string, string) {
		t.Error("disabled check executed")
		return CheckFailed, "", ""
	}
	req := skippedRequest(t, svc)
	checked, err := svc.RunCandidateChecks(context.Background(), "app1", req.CandidateID)
	if err != nil || checked.Status != CheckSkipped {
		t.Fatalf("skip became a passed check: %v %+v", err, checked)
	}
	run, err := svc.Start(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	finished := waitRelease(t, svc, run.ID)
	if finished.Status != "succeeded" {
		t.Fatalf("release failed: %+v", finished)
	}
	if strings.TrimSpace(runGit(t, repo, "show", "HEAD:tracked.txt")) != "release change" {
		t.Fatal("selected content not committed")
	}
	if !strings.Contains(runGit(t, repo, "status", "--porcelain"), "private-note.txt") {
		t.Fatal("unselected file changed")
	}
	if !strings.Contains(runGit(t, repo, "tag", "--list"), "v1.0.1") {
		t.Fatal("local tag missing")
	}
	plan, err := parseExecutionPlan(finished.ExecutionPlan)
	if err != nil || !plan.SkipChecks {
		t.Fatalf("skip missing from frozen plan: %v", err)
	}
}

func TestSkipChecksCannotReuseAfterToggleOrMutation(t *testing.T) {
	for _, change := range []string{"toggle", "content", "cancel"} {
		t.Run(change, func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			writeTestFile(t, filepath.Join(repo, "tracked.txt"), "change\n")
			req := skippedRequest(t, svc)
			switch change {
			case "toggle":
				req.SkipChecks = false
			case "content":
				writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed again\n")
			case "cancel":
				if _, err := svc.CancelCandidate("app1", req.CandidateID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.Start(context.Background(), "app1", req); err == nil {
				t.Fatal("reused invalid candidate")
			}
		})
	}
}

func TestSkipChecksDefaultAndSensitiveScan(t *testing.T) {
	var req CandidateRequest
	if err := json.Unmarshal([]byte(`{"intent":"formal"}`), &req); err != nil || req.SkipChecks {
		t.Fatal("checks must default on")
	}
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "-----BEGIN RSA PRIVATE KEY-----\nTEST-ONLY-NOT-A-REAL-KEY\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	req.StatusFingerprint = pf.StatusFingerprint
	req.SelectedPaths = []string{"tracked.txt"}
	view, err := svc.PrepareCandidate(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.SensitiveFindings) == 0 || view.Accepted {
		t.Fatal("default lost sensitive scan")
	}
	req.SkipChecks = true
	view, err = svc.PrepareCandidate(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	if !view.ChecksSkipped || len(view.SensitiveFindings) != 0 || view.Status != CheckSkipped {
		t.Fatal("explicit opt-out ignored")
	}
	req.SelectedPaths = []string{"../outside.txt"}
	if _, err = svc.PrepareCandidate(context.Background(), "app1", req); err == nil {
		t.Fatal("skip bypassed file scope")
	}
}

func TestSkipChecksDoesNotSkipSelectedBuild(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	target := validExecutorTarget()
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(target)); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "build change\n")
	runner := &recordingTargetRunner{}
	svc.targetRunner = runner
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	req := CreateRequest{Intent: IntentFormal, SkipChecks: true, BuildMode: "local", CreateTag: boolPtr(false), PushRemote: boolPtr(false), VersionMode: "auto", SelectedPaths: []string{"tracked.txt"}, SelectedTargets: []store.ReleaseTargetSelection{{TargetID: target.ID, Build: true, Package: true}}, StatusFingerprint: pf.StatusFingerprint, CommitMessage: "build with checks off"}
	view, err := svc.PrepareCandidate(context.Background(), "app1", candidateRequestFromCreate(req, IntentFormal))
	if err != nil {
		t.Fatal(err)
	}
	req.CandidateID = view.ID
	run, err := svc.Start(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" {
		t.Fatalf("local build failed: %+v", run)
	}
	if !reflect.DeepEqual(runner.Commands(), []string{"build-web", "package-web"}) {
		t.Fatalf("wrong phases: %v", runner.Commands())
	}
	plan, err := parseExecutionPlan(run.ExecutionPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SkipChecks || plan.Targets[0].Steps.Check != "" {
		t.Fatal("retry would reintroduce skipped checks")
	}
	cfg, err := svc.releaseConfig.Get(context.Background(), "app1")
	if err != nil || cfg.Targets[0].Steps.Check != "check-web" {
		t.Fatal("skip permanently changed project settings")
	}
}
