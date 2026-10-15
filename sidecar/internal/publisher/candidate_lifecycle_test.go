package publisher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCandidateCloseOnlyCleansIdleOwnedSnapshots(t *testing.T) {
	svc := &Service{active: map[string]bool{}, candidates: map[string]*releaseCandidate{}}
	makeCandidate := func(id string) *releaseCandidate {
		dir, err := os.MkdirTemp("", "rundock-candidate-lifecycle-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		writeTestFile(t, filepath.Join(dir, "tree", "source.txt"), "source")
		return &releaseCandidate{ID: id, Dir: dir, RepoRoot: t.TempDir()}
	}
	idle, active, checking, foreign := makeCandidate("idle"), makeCandidate("active"), makeCandidate("checking"), makeCandidate("foreign")
	for _, cand := range []*releaseCandidate{idle, active, checking} {
		svc.candidates[cand.ID] = cand
	}
	svc.active[gitPathKey(canonicalRepositoryPath(active.RepoRoot))] = true
	checking.View.Status = CheckRunning
	svc.Close()
	if _, err := os.Stat(idle.Dir); !os.IsNotExist(err) {
		t.Fatalf("idle snapshot remains: %v", err)
	}
	for _, cand := range []*releaseCandidate{active, checking, foreign} {
		if _, err := os.Stat(cand.Dir); err != nil {
			t.Fatalf("non-idle or foreign snapshot removed: %s: %v", cand.ID, err)
		}
	}
	if !svc.restarting || svc.reserve(idle.RepoRoot) {
		t.Fatal("closing service still accepts new work")
	}
	delete(svc.active, gitPathKey(canonicalRepositoryPath(active.RepoRoot)))
	checking.View.Status = CheckPassed
	svc.Close()
	if len(svc.candidates) != 0 {
		t.Fatal("finished candidates not cleaned on repeated close")
	}
}

func TestIdleCandidateExpiryAndCancellation(t *testing.T) {
	svc := &Service{active: map[string]bool{}, candidates: map[string]*releaseCandidate{}}
	makeCandidate := func(id string, last time.Time) *releaseCandidate {
		w, err := newScratch("candidate")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.cleanup() })
		c := &releaseCandidate{scratch: w, Dir: w.Dir, ID: id, RepoRoot: filepath.Join(t.TempDir(), id), lastUsed: last}
		svc.candidates[id] = c
		return c
	}
	now := time.Now()
	old := makeCandidate("old", now.Add(-2*time.Hour))
	fresh := makeCandidate("fresh", now)
	active := makeCandidate("active", now.Add(-2*time.Hour))
	cancelled := makeCandidate("cancelled", now)
	cancelled.View.Status = CheckCancelled
	svc.active[gitPathKey(canonicalRepositoryPath(active.RepoRoot))] = true
	svc.cleanIdleCandidates(now)
	for _, c := range []*releaseCandidate{old, cancelled} {
		if _, err := os.Stat(c.Dir); !os.IsNotExist(err) {
			t.Fatalf("expired temp not removed: %s", c.ID)
		}
	}
	for _, c := range []*releaseCandidate{fresh, active} {
		if _, err := os.Stat(c.Dir); err != nil {
			t.Fatalf("live temp removed: %s", c.ID)
		}
	}
	// A cancelled worker retains its tree until the actual repository lease ends.
	active.View.Status = CheckCancelled
	svc.cleanIdleCandidates(now)
	if _, err := os.Stat(active.Dir); err != nil {
		t.Fatal("cancel request deleted running worker tree")
	}
	svc.release(active.RepoRoot)
	if _, err := os.Stat(active.Dir); !os.IsNotExist(err) {
		t.Fatal("finished cancelled worker tree retained")
	}
}

func TestCandidateReplacementPreservesOtherRepositories(t *testing.T) {
	svc := &Service{active: map[string]bool{}, candidates: map[string]*releaseCandidate{}}
	makeCandidate := func(id, repo string) *releaseCandidate {
		w, err := newScratch("candidate")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.cleanup() })
		c := &releaseCandidate{scratch: w, Dir: w.Dir, ID: id, AppID: "app", RepoRoot: repo}
		svc.storeCandidate(c)
		return c
	}
	repo := t.TempDir()
	old := makeCandidate("old", repo)
	other := makeCandidate("other", t.TempDir())
	svc.active[gitPathKey(canonicalRepositoryPath(other.RepoRoot))] = true
	svc.discardSupersededCandidates("app", repo)
	if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
		t.Fatal("previous tree remains before replacement")
	}
	makeCandidate("replacement", repo)
	if svc.candidates[other.ID] != other {
		t.Fatal("storing replacement removed active candidate for another repository")
	}
	if _, err := os.Stat(other.Dir); err != nil {
		t.Fatal("other repository tree removed")
	}
}
