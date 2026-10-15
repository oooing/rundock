package publisher

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func reviewFixture(t *testing.T) (*Service, string, *CandidateView) {
	t.Helper()
	svc, repo, cleanup := newReleaseFixture(t)
	t.Cleanup(cleanup)
	writeTestFile(t, filepath.Join(repo, "sample.py"), "TOKEN = 'A7mQ9vB2nL5pR8sT6xY3zW1c'\n")
	writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{ID: "required", Name: "Project check", Command: "exit 0", Required: true}})
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{Intent: IntentFormal, SelectedPaths: []string{"sample.py"}, StatusFingerprint: pf.StatusFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.SensitiveFindings) != 1 {
		t.Fatalf("missing review finding: %+v", view)
	}
	return svc, repo, view
}

func reviewException(view *CandidateView) SensitiveException {
	f := view.SensitiveFindings[0]
	return SensitiveException{Path: f.Path, ContentFingerprint: f.ContentFingerprint, FindingFingerprint: f.Fingerprint, Reason: "Verified test fixture, only these exact bytes"}
}

func TestReviewCandidateReusesTreeAndOnlyRunsPendingCommands(t *testing.T) {
	svc, repo, view := reviewFixture(t)
	cand := svc.lookupCandidate(view.ID)
	work, tree, fp := cand.Work, cand.TreeHash, cand.Fingerprint
	head, index := runGit(t, repo, "rev-parse", "HEAD"), runGit(t, repo, "diff", "--cached")
	calls := 0
	svc.candidateCommand = func(context.Context, string, releaseconfig.CheckProfile) (string, string, string) {
		calls++
		return CheckPassed, "actual check output", ""
	}
	ex := reviewException(view)
	bad := ex
	bad.ContentFingerprint = "wrong"
	if _, err := svc.ReviewCandidate(context.Background(), "app1", view.ID, []SensitiveException{bad}); err == nil {
		t.Fatal("unbound confirmation accepted")
	}
	updated, err := svc.ReviewCandidate(context.Background(), "app1", view.ID, []SensitiveException{ex})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.SensitiveFindings) != 0 || updated.Accepted || calls != 0 {
		t.Fatal("confirmation fabricated completed commands")
	}
	if cand.Work != work || cand.TreeHash != tree || cand.Fingerprint != fp || updated.ID != view.ID {
		t.Fatal("confirmation rebuilt the candidate")
	}
	if err := svc.validateCandidateBinding(context.Background(), cand, cand.Request); err != nil {
		t.Fatal(err)
	}
	checked, err := svc.RunCandidateChecks(context.Background(), "app1", view.ID)
	if err != nil || !checked.Accepted || calls != 1 {
		t.Fatalf("pending command did not run once: %+v %v calls=%d", checked, err, calls)
	}
	replayed, err := svc.ReviewCandidate(context.Background(), "app1", view.ID, []SensitiveException{ex})
	if err != nil || !replayed.Accepted {
		t.Fatal("replay discarded validated result")
	}
	checked, err = svc.RunCandidateChecks(context.Background(), "app1", view.ID)
	if err != nil || !checked.Accepted || calls != 1 {
		t.Fatal("completed commands ran again")
	}
	if runGit(t, repo, "rev-parse", "HEAD") != head || runGit(t, repo, "diff", "--cached") != index {
		t.Fatal("review changed original repository")
	}
}

func TestReviewCandidateRejectsChangedInputsAndCancelledState(t *testing.T) {
	for _, scenario := range []string{"source", "copy", "config", "cancelled", "wrong-app", "invalid-batch"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, view := reviewFixture(t)
			ex := reviewException(view)
			appID := "app1"
			exceptions := []SensitiveException{ex}
			switch scenario {
			case "source":
				writeTestFile(t, filepath.Join(repo, "sample.py"), "changed\n")
			case "copy":
				writeTestFile(t, filepath.Join(svc.lookupCandidate(view.ID).Work, "sample.py"), "changed\n")
			case "config":
				writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{ID: "required", Command: "exit 7", Required: true}})
			case "cancelled":
				svc.CancelCandidate("app1", view.ID)
			case "wrong-app":
				appID = "other"
			case "invalid-batch":
				bad := ex
				bad.Path = "other.py"
				exceptions = append(exceptions, bad)
			}
			if _, err := svc.ReviewCandidate(context.Background(), appID, view.ID, exceptions); err == nil {
				t.Fatal("invalid review accepted")
			}
			remaining := svc.lookupCandidate(view.ID)
			if scenario == "cancelled" && remaining != nil {
				t.Fatal("cancelled candidate retained its temporary tree")
			}
			if remaining != nil && len(remaining.Request.SensitiveExceptions) != 0 {
				t.Fatal("failed confirmation partially applied")
			}
		})
	}
}

func TestReviewCandidateDoesNotBypassFailedProjectCommand(t *testing.T) {
	svc, _, view := reviewFixture(t)
	svc.candidateCommand = func(context.Context, string, releaseconfig.CheckProfile) (string, string, string) {
		return CheckFailed, "assertion failed", "project failed"
	}
	if _, err := svc.ReviewCandidate(context.Background(), "app1", view.ID, []SensitiveException{reviewException(view)}); err != nil {
		t.Fatal(err)
	}
	checked, err := svc.RunCandidateChecks(context.Background(), "app1", view.ID)
	if err != nil || checked.Accepted || checked.Status != CheckFailed {
		t.Fatal("review bypassed real project failure")
	}
}
