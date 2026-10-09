package publisher

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func TestAutomaticCandidateChecksNeedNoCustomConfiguration(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeCheckProfiles(t, repo, nil)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "ready for release\n")
	head, index := runGit(t, repo, "rev-parse", "HEAD"), runGit(t, repo, "diff", "--cached")
	for _, mode := range []string{"github", "local"} {
		pf, err := svc.PreflightLocal(context.Background(), "app1")
		if err != nil {
			t.Fatal(err)
		}
		view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}, Intent: IntentFormal, BuildMode: mode, TargetVersion: "1.0.1", VersionMode: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		if view.Accepted {
			t.Fatal("prepare must not skip the final content validation")
		}
		svc.candidateCommand = func(context.Context, string, releaseconfig.CheckProfile) (string, string, string) {
			t.Error("default checks must not execute arbitrary project commands")
			return CheckFailed, "", "unexpected command"
		}
		view, err = svc.RunCandidateChecks(context.Background(), "app1", view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !view.Accepted || !view.CanFormal || view.Status != CheckPassed {
			t.Fatalf("%s: default checks did not finish: %+v", mode, view)
		}
		if len(view.CheckResults) != 3 {
			t.Fatalf("expected actual built-in results: %+v", view.CheckResults)
		}
		for _, check := range view.CheckResults {
			if check.Status != CheckPassed || check.Reason == "" {
				t.Fatal("check must explain what was verified")
			}
		}
	}
	if runGit(t, repo, "rev-parse", "HEAD") != head || runGit(t, repo, "diff", "--cached") != index {
		t.Fatal("checks changed real commit or index")
	}
}

func TestAutomaticChecksDoNotBypassRequiredProjectCheck(t *testing.T) {
	for _, command := range []string{"exit 7", "rundock_missing_required_tool_72891"} {
		svc, repo, cleanup := newReleaseFixture(t)
		defer cleanup()
		writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{ID: "required", Name: "Project tests", Command: command, Required: true}})
		writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
		pf, err := svc.PreflightLocal(context.Background(), "app1")
		if err != nil {
			t.Fatal(err)
		}
		view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}, Intent: IntentFormal})
		if err != nil {
			t.Fatal(err)
		}
		view, err = svc.RunCandidateChecks(context.Background(), "app1", view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Accepted || view.CanFormal || view.Status == CheckPassed {
			t.Fatal("built-in checks bypassed required project tests")
		}
	}
}

func TestAutomaticChecksReportBlockingFindings(t *testing.T) {
	checks := automaticCandidateChecks([]SensitiveFinding{{Kind: "private-key"}}, []DependencyFinding{{Blocked: true}}, 1)
	for i, check := range checks {
		want := CheckFailed
		if i == 0 {
			want = CheckBlocked
		}
		if check.Status != want || !check.Required {
			t.Fatalf("blocking check reported success: %+v", check)
		}
	}
}

func TestUnresolvedFilesStayBlockedUntilReviewed(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeCheckProfiles(t, repo, nil)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	writeTestFile(t, filepath.Join(repo, "unknown.bin"), "unknown local material\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	req := CandidateRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}, Intent: IntentFormal}
	view, err := svc.PrepareCandidate(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != CheckBlocked || view.Accepted || view.CanFormal {
		t.Fatalf("pending confirmation was not blocked: %+v", view)
	}
	var fingerprint string
	for _, item := range view.Classifications {
		if item.Path == "unknown.bin" {
			fingerprint = item.ContentFingerprint
		}
	}
	view, err = svc.RunCandidateChecks(context.Background(), "app1", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != CheckBlocked || view.Accepted || view.CanFormal {
		t.Fatalf("recheck bypassed pending confirmation or reported test failure: %+v", view)
	}
	req.ManualDecisions = []ManualDecision{{Path: "unknown.bin", Decision: DecisionExclude, ContentFingerprint: fingerprint}}
	view, err = svc.PrepareCandidate(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	view, err = svc.RunCandidateChecks(context.Background(), "app1", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != CheckPassed || !view.Accepted {
		t.Fatalf("valid file choice did not unblock: %+v", view)
	}
}
