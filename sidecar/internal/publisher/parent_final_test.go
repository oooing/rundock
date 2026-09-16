package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParentCancellationCannotBecomeSuccess(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	v, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{Intent: IntentFormal, StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}, CreateTag: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	svc.candidateCommand = func(context.Context, string, releaseconfig.CheckProfile) (string, string, string) {
		close(started)
		<-finish
		return CheckPassed, "", ""
	}
	done := make(chan *CandidateView, 1)
	go func() { result, _ := svc.RunCandidateChecks(context.Background(), "app1", v.ID); done <- result }()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("check did not start")
	}
	if _, err = svc.CancelCandidate("app1", v.ID); err != nil {
		t.Fatal(err)
	}
	close(finish)
	result := <-done
	if result == nil || result.Status != CheckCancelled || result.Accepted || result.CanFormal || result.CanSaveProgress {
		t.Fatalf("cancel overwritten: %+v", result)
	}
}

func TestParentCandidateCleanupStaysInOwnedTemp(t *testing.T) {
	parent := t.TempDir()
	outside := filepath.Join(parent, "rundock-candidate-user-data")
	writeTestFile(t, filepath.Join(outside, "keep.txt"), "keep")
	(&releaseCandidate{Dir: outside}).cleanup()
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("cleanup touched a directory outside the candidate temp root: %v", err)
	}
	owned, err := os.MkdirTemp("", "rundock-candidate-cleanup-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(owned) })
	writeTestFile(t, filepath.Join(owned, "output.txt"), "test")
	(&releaseCandidate{Dir: owned}).cleanup()
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("owned candidate was not removed: %v", err)
	}
}

func TestParentExactFindingException(t *testing.T) {
	root := t.TempDir()
	token := "ghp_" + strings.Repeat("aB3cD4", 6)
	raw := []byte("// " + token + " " + token + "\n// " + token + "\n")
	writeTestFile(t, filepath.Join(root, "config.js"), string(raw))
	findings := scanSensitiveFile("config.js", raw)
	fingerprints := map[string]string{"config.js": "original-content"}
	all := filterSensitiveExceptions(findings, nil, fingerprints)
	if len(all) != 3 {
		t.Fatalf("expected three locations, got %+v", all)
	}
	exception := SensitiveException{Path: all[0].Path, FindingFingerprint: all[0].Fingerprint, ContentFingerprint: all[0].ContentFingerprint, Reason: "synthetic example"}
	remaining := filterSensitiveExceptions(findings, []SensitiveException{exception}, fingerprints)
	if len(remaining) != 2 {
		t.Fatalf("exception matched other locations: %+v", remaining)
	}
	writeTestFile(t, filepath.Join(root, "config.js"), string(raw)+"// changed\n")
	fingerprints["config.js"] = "changed-content"
	if got := filterSensitiveExceptions(findings, []SensitiveException{exception}, fingerprints); len(got) != 3 {
		t.Fatal("exception survived file change")
	}
}

func TestParentRawBlobsAndBuildOutputs(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	// An export attribute must not silently remove a committed file. Git clean
	// filters must not rewrite the bytes after they have been checked.
	writeTestFile(t, filepath.Join(repo, ".gitattributes"), "tracked.txt export-ignore\n*.txt filter=unsafe\n")
	runGit(t, repo, "add", ".gitattributes")
	runGit(t, repo, "commit", "-m", "attributes")
	runGit(t, repo, "config", "filter.unsafe.clean", "echo transformed")
	runGit(t, repo, "config", "filter.unsafe.smudge", "echo transformed")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "exact new bytes\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	v, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{Intent: IntentSaveProgress, StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	cand := svc.lookupCandidate(v.ID)
	if raw := readFile(t, filepath.Join(cand.Work, "tracked.txt")); raw != "exact new bytes\n" {
		t.Fatalf("filter changed candidate %q", raw)
	}
	blob, err := isolatedGit(context.Background(), cand, "show", cand.TreeHash+":tracked.txt")
	if err != nil || strings.TrimSpace(blob) != "exact new bytes" {
		t.Fatalf("checked/committed bytes differ: %q %v", blob, err)
	}
	writeTestFile(t, filepath.Join(cand.Work, "dist/output.js"), "generated")
	if err := validateCandidateBytes(cand); err != nil {
		t.Fatalf("generated output should not alter source: %v", err)
	}
	writeTestFile(t, filepath.Join(cand.Work, "tracked.txt"), "modified by check")
	if err := validateCandidateBytes(cand); err == nil {
		t.Fatal("candidate mutation accepted")
	}
	if raw := readFile(t, filepath.Join(repo, "tracked.txt")); raw != "exact new bytes\n" {
		t.Fatal("original worktree modified")
	}
}

func TestParentStagedChangesSurviveRejection(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "candidate\n")
	req := acceptCandidate(t, svc, CreateRequest{CreateTag: boolPtr(false), PushRemote: boolPtr(false), SelectedPaths: []string{"tracked.txt"}})
	writeTestFile(t, filepath.Join(repo, "other.txt"), "user staging\n")
	runGit(t, repo, "add", "other.txt")
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	head := runGit(t, repo, "rev-parse", "HEAD")
	if _, err := svc.Start(context.Background(), "app1", req); err == nil {
		t.Fatal("stale staged candidate accepted")
	}
	after, _ := os.ReadFile(index)
	if string(after) != string(before) || runGit(t, repo, "rev-parse", "HEAD") != head {
		t.Fatal("user index or HEAD changed")
	}
}
