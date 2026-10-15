package publisher

import (
	"encoding/json"
	"github.com/launcher-sidecar/internal/store"
	"path/filepath"
	"testing"
)

func TestRunPresentationUsesFrozenTargetIdentity(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "run.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateApp(&store.App{ID: "app", Name: "project", Cwd: t.TempDir(), EntryScript: "start.bat", LastStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	selection := store.ReleaseTargetSelection{TargetID: "pc", Publish: true}
	plan := executionPlan{SchemaVersion: executionPlanSchemaVersion, Targets: []planTarget{{ID: "pc", Name: "Saved PC", VersionGroup: "desktop", Selection: selection}}}
	raw, _ := json.Marshal(plan)
	if err = st.CreateReleaseRun(&store.ReleaseRun{ID: "run", AppID: "app", Status: "succeeded", Stage: "completed", ExecutionPlan: raw, SelectedTargets: []store.ReleaseTargetSelection{selection}}); err != nil {
		t.Fatal(err)
	}
	if err = st.CreateReleaseTargetRuns("run", []store.ReleaseTargetSelection{selection}); err != nil {
		t.Fatal(err)
	}
	view, err := (&Service{store: st}).GetRun("run", 0)
	if err != nil || len(view.Targets) != 1 || view.Targets[0].Name != "Saved PC" || view.Targets[0].VersionGroup != "desktop" {
		t.Fatalf("%+v %v", view, err)
	}
}
