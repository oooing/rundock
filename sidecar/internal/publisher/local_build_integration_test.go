package publisher

import (
	"context"
	"encoding/json"
	"github.com/launcher-sidecar/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in real-project acceptance: only runs the saved local check/build commands.
// Uses a temporary database; never calls Start, commits, tags, pushes or deploys.
func TestLocalProjectBuildIntegration(t *testing.T) {
	root := os.Getenv("RUNDOCK_BUILD_TEST_ROOT")
	if root == "" {
		t.Skip("set RUNDOCK_BUILD_TEST_ROOT for an explicitly authorized local build")
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "build.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CreateApp(&store.App{ID: "app1", Name: "Local acceptance", Cwd: root, LastStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	svc := New(db)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cfg, err := svc.releaseConfig.Get(ctx, "app1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "file" || len(cfg.Targets) != 1 || cfg.Targets[0].Runner.Type != "local" {
		t.Fatal("requires one explicit local build target")
	}
	target := cfg.Targets[0]
	selection := store.ReleaseTargetSelection{TargetID: target.ID, Build: true, Package: target.Steps.Package != ""}
	plan, err := svc.freezeExecutionPlan(ctx, "app1", root, []store.ReleaseTargetSelection{selection})
	if err != nil {
		t.Fatal(err)
	}
	head, _ := svc.git(ctx, root, "rev-parse", "HEAD")
	index, _ := svc.gitRaw(ctx, root, "diff", "--cached", "--binary")
	run := createTargetExecutionRun(t, svc, root, "local-build-acceptance", selection)
	started := time.Now()
	defer func() {
		logs, _ := db.ReleaseLogs(run.ID, 0, 200)
		for _, line := range logs {
			t.Log(line.Text)
		}
		if dir := os.Getenv("RUNDOCK_BUILD_TEST_EVIDENCE"); dir != "" {
			artifacts, _ := db.ReleaseArtifacts(run.ID)
			states, _ := db.ReleaseTargetRuns(run.ID)
			raw, _ := json.MarshalIndent(map[string]any{"logs": logs, "artifacts": artifacts, "states": states, "seconds": time.Since(started).Seconds()}, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, "build-result.json"), raw, 0600); err != nil {
				t.Error(err)
			}
		}
		if after, _ := svc.git(ctx, root, "rev-parse", "HEAD"); after != head {
			t.Error("HEAD changed")
		}
		if after, _ := svc.gitRaw(ctx, root, "diff", "--cached", "--binary"); after != index {
			t.Error("index changed")
		}
	}()
	if err := svc.executeTargetChecks(ctx, run, plan); err != nil {
		t.Fatal(err)
	}
	if err := svc.executeTargetPhase(ctx, run, plan, false); err != nil {
		t.Fatal(err)
	}
	states, err := db.ReleaseTargetRuns(run.ID)
	if err != nil || len(states) != 1 || states[0].Status != "succeeded" {
		t.Fatalf("target: %+v %v", states, err)
	}
	artifacts, err := db.ReleaseArtifacts(run.ID)
	if err != nil || len(artifacts) == 0 {
		t.Fatal("no verified artifacts", err)
	}
	t.Logf("real build completed in %.2fs; %d artifacts", time.Since(started).Seconds(), len(artifacts))
}
