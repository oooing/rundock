package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReleaseProfileRemembersTagAndVersionMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "profile.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	createReleaseTestApp(t, s, "app1")

	defaults, err := s.GetReleaseProfile("app1")
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.CreateTag || defaults.VersionMode != "auto" {
		t.Fatalf("default profile = %+v, want createTag=true/versionMode=auto", defaults)
	}

	defaults.CreateTag = false
	defaults.VersionMode = "manual"
	if err := s.UpsertReleaseProfile(defaults); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening the database models a new Launcher session. The per-project choice
	// must survive it rather than falling back to the first-use default.
	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	remembered, err := s.GetReleaseProfile("app1")
	if err != nil {
		t.Fatal(err)
	}
	if remembered.CreateTag || remembered.VersionMode != "manual" {
		t.Fatalf("remembered profile = %+v, want createTag=false/versionMode=manual", remembered)
	}
}

func TestReleaseRunPersistsSelectedTargetCombination(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	createReleaseTestApp(t, s, "app1")

	wantTargets := []ReleaseTargetSelection{
		{TargetID: "web", Build: true, Publish: true},
		{TargetID: "server", Package: true, Deploy: true},
		{TargetID: "android", Build: true, Package: true},
	}
	want := &ReleaseRun{
		ID: "run-options", AppID: "app1", RepoRoot: `C:\repo`, Branch: "main", RemoteName: "origin",
		TargetVersion: "", TagName: "", CreateTag: false, SelectedTargets: wantTargets,
		Status: "queued", Stage: "preparing", StatusFingerprint: "fingerprint",
	}
	if err := s.CreateReleaseRun(want); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetReleaseRun(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CreateTag || got.TargetVersion != "" || got.TagName != "" {
		t.Fatalf("run tag snapshot = %+v", got)
	}
	if !reflect.DeepEqual(got.SelectedTargets, wantTargets) {
		t.Fatalf("selected targets = %#v, want %#v", got.SelectedTargets, wantTargets)
	}
}

func TestReleaseTargetRunsKeepIndependentPartialState(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "target-runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	createReleaseTestApp(t, s, "app1")

	selections := []ReleaseTargetSelection{
		{TargetID: "web", Build: true, Publish: true},
		{TargetID: "server", Package: true, Deploy: true},
		{TargetID: "android", Build: true, Package: true},
	}
	if err := s.CreateReleaseRun(&ReleaseRun{
		ID: "run-partial", AppID: "app1", RepoRoot: `C:\repo`, Branch: "main", RemoteName: "origin",
		SelectedTargets: selections, Status: "running", Stage: "targets",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateReleaseTargetRuns("run-partial", selections); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateReleaseTargetRun("run-partial", "web", "succeeded", "published", "", "", true, true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateReleaseTargetRun("run-partial", "server", "failed", "deploying", "deploy_failed", "server rejected upload", true, true); err != nil {
		t.Fatal(err)
	}

	targets, err := s.ReleaseTargetRuns("run-partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 {
		t.Fatalf("target states = %#v", targets)
	}
	byID := map[string]*ReleaseTargetRun{}
	for _, target := range targets {
		byID[target.TargetID] = target
	}
	if byID["web"].Status != "succeeded" || byID["web"].StartedAt == nil || byID["web"].FinishedAt == nil {
		t.Fatalf("web state = %+v", byID["web"])
	}
	if byID["server"].Status != "failed" || byID["server"].ErrorCode != "deploy_failed" || byID["server"].FinishedAt == nil {
		t.Fatalf("server state = %+v", byID["server"])
	}
	if byID["android"].Status != "queued" || byID["android"].Stage != "waiting" || byID["android"].StartedAt != nil {
		t.Fatalf("unreached android state = %+v", byID["android"])
	}

	// Retrying or updating one target must never reset an already successful
	// delivery, otherwise a deploy could be performed twice.
	if err := s.UpdateReleaseTargetRun("run-partial", "server", "running", "deploying", "", "", true, false); err != nil {
		t.Fatal(err)
	}
	targets, err = s.ReleaseTargetRuns("run-partial")
	if err != nil {
		t.Fatal(err)
	}
	byID = map[string]*ReleaseTargetRun{}
	for _, target := range targets {
		byID[target.TargetID] = target
	}
	if byID["web"].Status != "succeeded" || byID["web"].FinishedAt == nil {
		t.Fatalf("retry reset successful web target: %+v", byID["web"])
	}
	if byID["server"].Status != "running" || byID["server"].FinishedAt != nil || byID["server"].ErrorCode != "" {
		t.Fatalf("server retry state = %+v", byID["server"])
	}
}

func TestUpgradeReleaseOptionsUsesSafeDefaults(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old-releases.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE release_profiles (
			app_id TEXT PRIMARY KEY,
			remote_name TEXT NOT NULL DEFAULT 'origin',
			version_strategy TEXT NOT NULL DEFAULT 'auto',
			pre_release_command TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE release_runs (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			repo_root TEXT NOT NULL,
			branch TEXT NOT NULL,
			remote_name TEXT NOT NULL,
			target_version TEXT NOT NULL,
			tag_name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'queued',
			stage TEXT NOT NULL DEFAULT 'preparing',
			commit_sha TEXT NOT NULL DEFAULT '',
			status_fingerprint TEXT NOT NULL DEFAULT '',
			error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			finished_at TEXT
		);
		INSERT INTO release_profiles (app_id) VALUES ('app1');
		INSERT INTO release_runs
			(id,app_id,repo_root,branch,remote_name,target_version,tag_name)
			VALUES ('run1','app1','C:\repo','main','origin','1.2.3','v1.2.3');
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("upgrade old release schema: %v", err)
	}
	defer s.Close()
	profile, err := s.GetReleaseProfile("app1")
	if err != nil || profile == nil || !profile.CreateTag || profile.VersionMode != "auto" {
		t.Fatalf("upgraded profile = %+v, err=%v", profile, err)
	}
	run, err := s.GetReleaseRun("run1")
	if err != nil || run == nil || !run.CreateTag || run.SelectedTargets == nil || len(run.SelectedTargets) != 0 {
		t.Fatalf("upgraded run = %+v, err=%v", run, err)
	}
}

func TestUpgradeOldReleaseTargetRunsAddsStepCheckpoints(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old-target-runs.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE release_target_runs (
		release_run_id TEXT NOT NULL,
		target_id TEXT NOT NULL,
		build INTEGER NOT NULL DEFAULT 0,
		package INTEGER NOT NULL DEFAULT 0,
		publish INTEGER NOT NULL DEFAULT 0,
		deploy INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'queued',
		stage TEXT NOT NULL DEFAULT 'waiting',
		error_code TEXT NOT NULL DEFAULT '',
		error_message TEXT NOT NULL DEFAULT '',
		started_at TEXT,
		finished_at TEXT,
		PRIMARY KEY (release_run_id,target_id)
	)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("upgrade old release_target_runs: %v", err)
	}
	defer s.Close()
	createReleaseTestApp(t, s, "app1")
	if err := s.CreateReleaseRun(&ReleaseRun{
		ID: "run-upgraded-target", AppID: "app1", RepoRoot: `C:\repo`, Branch: "main", RemoteName: "origin",
		Status: "running", Stage: "target_build",
	}); err != nil {
		t.Fatal(err)
	}
	selection := ReleaseTargetSelection{TargetID: "web", Build: true, Publish: true}
	if err := s.CreateReleaseTargetRuns("run-upgraded-target", []ReleaseTargetSelection{selection}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkReleaseTargetStepDone("run-upgraded-target", "web", "build"); err != nil {
		t.Fatal(err)
	}
	targets, err := s.ReleaseTargetRuns("run-upgraded-target")
	if err != nil || len(targets) != 1 {
		t.Fatalf("query upgraded target state: targets=%#v err=%v", targets, err)
	}
	if !targets[0].BuildDone || targets[0].CheckDone || targets[0].PackageDone || targets[0].PublishDone || targets[0].DeployDone {
		t.Fatalf("upgraded checkpoint columns = %+v", targets[0])
	}
}

func createReleaseTestApp(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateApp(&App{
		ID: id, Name: "release test", EntryScript: `C:\repo\start.bat`, Cwd: `C:\repo`,
		AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{},
		LastStatus: "stopped",
	}); err != nil {
		t.Fatal(err)
	}
}
