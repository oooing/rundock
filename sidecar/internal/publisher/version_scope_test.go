package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"testing"
)

func TestDisabledTargetVersionFilesDoNotBlockActiveTarget(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	cfg := &releaseconfig.Config{SchemaVersion: 1, VersionGroups: []releaseconfig.VersionGroup{
		{ID: "web", Name: "Web", VersionFiles: []releaseconfig.VersionFile{{Path: "package.json", Format: "json"}}},
		{ID: "old", Name: "Old", VersionFiles: []releaseconfig.VersionFile{{Path: "missing/version.json", Format: "json"}}},
	}, Targets: []releaseconfig.Target{
		{ID: "web", Name: "Web", Kind: "web", VersionGroup: "web", WorkingDir: ".", Enabled: true, Runner: releaseconfig.Runner{Type: "local"}, Steps: releaseconfig.Steps{Build: "echo build"}},
		{ID: "old", Name: "Old", Kind: "web", VersionGroup: "old", WorkingDir: ".", Enabled: false, Runner: releaseconfig.Runner{Type: "local"}, Steps: releaseconfig.Steps{Build: "echo old"}},
	}}
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if !pf.CanRelease {
		t.Fatal(pf.BlockingIssues)
	}
	selections := []store.ReleaseTargetSelection{{TargetID: "web", Build: true}}
	plan, err := svc.freezeExecutionPlan(context.Background(), "app1", repo, selections)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.VersionGroups) != 1 || plan.VersionGroups[0].ID != "web" {
		t.Fatal(plan)
	}
	if _, err := svc.freezeExecutionPlan(context.Background(), "app1", repo, []store.ReleaseTargetSelection{{TargetID: "old", Build: true}}); err == nil {
		t.Fatal("disabled target accepted")
	}
	cfg.Targets[1].Enabled = true
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	pf, err = svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(pf, "version_file_invalid") {
		t.Fatal("enabled target missing version must block", pf.BlockingIssues)
	}
}
