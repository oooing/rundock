package publisher

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func TestStartRequiresExplicitExternalActionsConfirmation(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "external action\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag := false
	_, err = svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		SelectedTargets: []store.ReleaseTargetSelection{{TargetID: "web", Publish: true}},
	})
	pe, ok := err.(*Error)
	if !ok || pe.Code != "external_actions_confirmation_required" {
		t.Fatalf("Start error = %#v", err)
	}
	runs, err := svc.store.ListReleaseRuns("app1", 10)
	if err != nil || len(runs) != 0 {
		t.Fatalf("unconfirmed request persisted release runs: runs=%+v err=%v", runs, err)
	}
}

func TestExternalConfirmationOnlyAppliesToPublishAndDeploy(t *testing.T) {
	tests := []struct {
		name      string
		selection store.ReleaseTargetSelection
		wanted    bool
	}{
		{name: "build", selection: store.ReleaseTargetSelection{Build: true}},
		{name: "package", selection: store.ReleaseTargetSelection{Package: true}},
		{name: "publish", selection: store.ReleaseTargetSelection{Publish: true}, wanted: true},
		{name: "deploy", selection: store.ReleaseTargetSelection{Deploy: true}, wanted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiresExternalActionsConfirmation([]store.ReleaseTargetSelection{tt.selection}); got != tt.wanted {
				t.Fatalf("confirmation required = %v, want %v", got, tt.wanted)
			}
		})
	}
}
