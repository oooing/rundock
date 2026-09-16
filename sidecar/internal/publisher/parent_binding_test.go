package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"path/filepath"
	"testing"
)

// Guard-level probes: no Start/commit/push is invoked by these checks.
func TestParentAcceptedCandidateMustBindCurrentRequest(t *testing.T) {
	for _, change := range []string{"source", "selection", "version", "check-config"} {
		t.Run(change, func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			writeTestFile(t, filepath.Join(repo, "tracked.txt"), "accepted content\n")
			req := acceptCandidate(t, svc, CreateRequest{
				Intent: IntentFormal, TargetVersion: "1.0.1", VersionMode: "manual",
				CreateTag: boolPtr(true), PushRemote: boolPtr(false), BuildMode: BuildModeNone,
				SelectedPaths: []string{"tracked.txt"}, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
			})
			switch change {
			case "source":
				writeTestFile(t, filepath.Join(repo, "tracked.txt"), "changed after successful check\n")
			case "selection":
				req.SelectedPaths = nil
			case "version":
				req.TargetVersion = "2.0.0"
			case "check-config":
				writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{ID: "required-new", Name: "Required", Command: "exit 9", Required: true}})
			}
			pf, err := svc.PreflightLocal(context.Background(), "app1")
			if err != nil {
				t.Fatal(err)
			}
			req.StatusFingerprint = pf.StatusFingerprint
			if _, err := svc.ensureReleaseCandidate(context.Background(), "app1", req, req.SelectedPaths, IntentFormal); err == nil {
				t.Fatalf("accepted old candidate after %s changed; publication guard must invalidate content/plan/config", change)
			}
		})
	}
}
