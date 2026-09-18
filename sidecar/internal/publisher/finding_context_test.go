package publisher

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindingContextUsesExactCandidateSource(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	const marker = "TEST-ONLY-CONTEXT-NOT-A-REAL-SECRET"
	text := strings.Repeat("# unchanged prefix\n", 5000) + "# before\n-----BEGIN RSA PRIVATE KEY-----\n" + marker + "\n# after\n" + strings.Repeat("# tail\n", 20)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), text)
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{Intent: IntentFormal, StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.SensitiveFindings) == 0 {
		t.Fatal("fixture finding missing")
	}
	finding := view.SensitiveFindings[0]
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "different working copy\n")
	source, err := svc.FindingContext("app1", view.ID, finding.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	if source.Line != 5002 || len(source.Lines) != 7 || source.Lines[3].Text != "-----BEGIN RSA PRIVATE KEY-----" || source.Lines[4].Text != marker {
		t.Fatal("wrong source or line context")
	}
	larger, err := svc.FindingContext("app1", view.ID, finding.Fingerprint, true)
	if err != nil || len(larger.Lines) <= len(source.Lines) {
		t.Fatal("more context missing")
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), marker) {
		t.Fatal("raw source leaked into stored finding")
	}
	for _, args := range [][3]string{{"wrong-app", view.ID, finding.Fingerprint}, {"app1", "missing", finding.Fingerprint}, {"app1", view.ID, "../tracked.txt"}} {
		if _, err := svc.FindingContext(args[0], args[1], args[2], false); err == nil {
			t.Fatal("unbound context request allowed")
		}
	}
	cand := svc.lookupCandidate(view.ID)
	writeTestFile(t, filepath.Join(cand.Work, "tracked.txt"), "changed snapshot\n")
	if _, err := svc.FindingContext("app1", view.ID, finding.Fingerprint, false); err == nil {
		t.Fatal("mutated snapshot displayed as checked source")
	}
}
