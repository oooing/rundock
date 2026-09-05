package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestConfiguredVersionUpdatesPreserveUnrelatedContent(t *testing.T) {
	repo := t.TempDir()
	files := []releaseconfig.VersionFile{
		{Path: "tauri.json", Format: "json", JSONPointer: "/package/version"},
		{Path: "Cargo.toml", Format: "cargo"},
		{Path: "Package.toml", Format: "toml"},
		{Path: "app/build.gradle", Format: "gradle"},
	}
	writePublisherFixture(t, repo, "tauri.json", `{"package":{"productName":"Demo","version":"1.2.3"},"keep":true}`)
	writePublisherFixture(t, repo, "Cargo.toml", "[package]\nname = \"demo\"\nversion = \"1.2.3\"\n\n[dependencies]\nfoo = \"9.9.9\"\n")
	writePublisherFixture(t, repo, "Package.toml", "[package]\nname = \"demo\"\nversion = \"1.2.3\"\n")
	writePublisherFixture(t, repo, "app/build.gradle", "android { defaultConfig { versionCode 7\nversionName = \"1.2.3\" } }\n")

	originals, err := updateConfiguredVersionFiles(repo, files, "2.0.0")
	if err != nil || len(originals) != 4 {
		t.Fatalf("update versions: originals=%d err=%v", len(originals), err)
	}
	assertFileContains(t, filepath.Join(repo, "tauri.json"), `"version":"2.0.0"`, `"keep":true`)
	assertFileContains(t, filepath.Join(repo, "Cargo.toml"), `version = "2.0.0"`, `foo = "9.9.9"`)
	assertFileContains(t, filepath.Join(repo, "Package.toml"), `version = "2.0.0"`)
	assertFileContains(t, filepath.Join(repo, "app/build.gradle"), `versionName = "2.0.0"`, `versionCode 7`)
}

func TestCargoLockUpdateChangesOnlyMatchingLocalPackage(t *testing.T) {
	repo := t.TempDir()
	files := []releaseconfig.VersionFile{
		{Path: "src-tauri/Cargo.toml", Format: "cargo"},
		{Path: "src-tauri/Cargo.lock", Format: "cargo-lock"},
	}
	writePublisherFixture(t, repo, "src-tauri/Cargo.toml", "[package]\nname = \"demo\"\nversion = \"1.2.3\"\n")
	writePublisherFixture(t, repo, "src-tauri/Cargo.lock", `version = 4

[[package]]
name = "demo"
version = "9.9.9"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "demo"
version = "1.2.3"
dependencies = ["serde"]
`)

	originals, err := updateConfiguredVersionFiles(repo, files, "2.0.0")
	if err != nil || len(originals) != 2 {
		t.Fatalf("update Cargo versions: originals=%d err=%v", len(originals), err)
	}
	assertFileContains(t, filepath.Join(repo, "src-tauri", "Cargo.toml"), `version = "2.0.0"`)
	lockRaw, err := os.ReadFile(filepath.Join(repo, "src-tauri", "Cargo.lock"))
	if err != nil {
		t.Fatal(err)
	}
	lock := string(lockRaw)
	if !strings.Contains(lock, "version = \"9.9.9\"\nsource = \"registry+") || !strings.Contains(lock, "version = \"2.0.0\"\ndependencies") {
		t.Fatalf("Cargo.lock root/dependency versions were not isolated:\n%s", lock)
	}
	got, err := readConfiguredVersion(repo, releaseconfig.VersionFile{Path: "src-tauri/Cargo.lock", Format: "cargo-lock"})
	if err != nil || got != "2.0.0" {
		t.Fatalf("read updated Cargo.lock version = %q, err=%v", got, err)
	}
}

func TestCargoLockUpdateFailsWithoutMatchingLocalPackageAndWritesNothing(t *testing.T) {
	repo := t.TempDir()
	files := []releaseconfig.VersionFile{
		{Path: "src-tauri/Cargo.toml", Format: "cargo"},
		{Path: "src-tauri/Cargo.lock", Format: "cargo-lock"},
	}
	manifest := "[package]\nname = \"demo\"\nversion = \"1.2.3\"\n"
	lock := "version = 4\n\n[[package]]\nname = \"demo\"\nversion = \"1.2.2\"\n"
	writePublisherFixture(t, repo, "src-tauri/Cargo.toml", manifest)
	writePublisherFixture(t, repo, "src-tauri/Cargo.lock", lock)

	_, err := updateConfiguredVersionFiles(repo, files, "2.0.0")
	if err == nil || !strings.Contains(err.Error(), "未找到") {
		t.Fatalf("missing Cargo.lock root package error = %v", err)
	}
	manifestRaw, _ := os.ReadFile(filepath.Join(repo, "src-tauri", "Cargo.toml"))
	lockRaw, _ := os.ReadFile(filepath.Join(repo, "src-tauri", "Cargo.lock"))
	if string(manifestRaw) != manifest || string(lockRaw) != lock {
		t.Fatalf("validation failure wrote files: Cargo.toml=%q Cargo.lock=%q", manifestRaw, lockRaw)
	}
}

func TestBuildSideEffectGuardOnlyAllowsDeclaredUntrackedArtifacts(t *testing.T) {
	before := worktreeSnapshot{"local-notes.txt": {Status: "??", Tracked: false, Hash: "same"}}
	after := worktreeSnapshot{
		"local-notes.txt": {Status: "??", Tracked: false, Hash: "same"},
		"dist/app.zip":    {Status: "??", Tracked: false, Hash: "artifact"},
	}
	if ok, changed := verifyBuildSideEffects(before, after, []string{"dist/**"}); !ok {
		t.Fatalf("declared artifact rejected: %v", changed)
	}
	after["src/main.ts"] = worktreeEntry{Status: " M", Tracked: true, Hash: "changed"}
	if ok, changed := verifyBuildSideEffects(before, after, []string{"dist/**"}); ok || len(changed) != 1 || changed[0] != "src/main.ts" {
		t.Fatalf("tracked side effect was not rejected: ok=%v changed=%v", ok, changed)
	}
}

func TestVersionRollbackDoesNotOverwriteConcurrentEdit(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "package.json")
	original := []byte(`{"version":"1.0.0","keep":true}`)
	expected := []byte(`{"version":"1.0.1","keep":true}`)
	if err := os.WriteFile(path, expected, 0o644); err != nil {
		t.Fatal(err)
	}
	if skipped := restoreFilesSafely(map[string][]byte{path: original}, map[string][]byte{path: expected}); len(skipped) != 0 {
		t.Fatalf("tool-owned version write was not restored: %v", skipped)
	}
	assertFileContains(t, path, `"version":"1.0.0"`)

	concurrent := []byte(`{"version":"1.0.1","keep":false}`)
	if err := os.WriteFile(path, concurrent, 0o644); err != nil {
		t.Fatal(err)
	}
	if skipped := restoreFilesSafely(map[string][]byte{path: original}, map[string][]byte{path: expected}); len(skipped) != 1 {
		t.Fatalf("concurrent edit should be preserved: %v", skipped)
	}
	assertFileContains(t, path, `"keep":false`)
}

func TestSecureProjectPathRejectsSymlinkEscape(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "version.json")
	if err := os.WriteFile(outsideFile, []byte(`{"version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "version.json")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlink unavailable on this host: %v", err)
	}
	if _, err := secureProjectPath(repo, "version.json", false); err == nil {
		t.Fatal("repository path guard accepted a symlink outside the repository")
	}
}

func TestSelectedVersionGroupDrivesAutoVersion(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writePublisherFixture(t, repo, "mobile/package.json", `{"name":"mobile","version":"5.0.0"}`)
	cfg := validExecutorConfig(validExecutorTarget())
	cfg.VersionGroups = append(cfg.VersionGroups, releaseconfig.VersionGroup{
		ID: "mobile", Name: "移动端版本", TagPrefix: "mobile", CurrentVersion: "5.0.0",
		VersionFiles: []releaseconfig.VersionFile{{Path: "mobile/package.json", Format: "json", JSONPointer: "/version"}},
	})
	mobileTarget := validExecutorTarget()
	mobileTarget.ID = "mobile"
	mobileTarget.Name = "Mobile"
	mobileTarget.VersionGroup = "mobile"
	cfg.Targets = append(cfg.Targets, mobileTarget)
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath, "mobile/package.json")
	runGit(t, repo, "commit", "-m", "configure multi-version release")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "visible version suggestion\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease || pf.SuggestedVersion != "0.1.0" {
		t.Fatalf("preflight suggestion=%s issues=%+v err=%v", pf.SuggestedVersion, pf.BlockingIssues, err)
	}
	svc.targetRunner = &recordingTargetRunner{}
	createTag := true
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, VersionMode: "auto", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		SelectedTargets: []store.ReleaseTargetSelection{{TargetID: "mobile", Build: true}},
		ReleaseNotes:    testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.TargetVersion != "5.0.1" || run.TagName != "mobile/v5.0.1" {
		t.Fatalf("auto run did not use the selected mobile version group: %+v", run)
	}
	assertFileContains(t, filepath.Join(repo, "package.json"), `"version": "1.0.0"`)
	assertFileContains(t, filepath.Join(repo, "mobile/package.json"), `"version":"5.0.1"`)
}

func TestNamespacedVersionIncludesHigherRemoteTag(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writePublisherFixture(t, repo, "mobile/package.json", `{"name":"mobile","version":"5.0.0"}`)
	cfg := validExecutorConfig(validExecutorTarget())
	cfg.VersionGroups = append(cfg.VersionGroups, releaseconfig.VersionGroup{
		ID: "mobile", Name: "移动端版本", TagPrefix: "mobile", CurrentVersion: "5.0.0",
		VersionFiles: []releaseconfig.VersionFile{{Path: "mobile/package.json", Format: "json", JSONPointer: "/version"}},
	})
	mobileTarget := validExecutorTarget()
	mobileTarget.ID, mobileTarget.Name, mobileTarget.VersionGroup = "mobile", "Mobile", "mobile"
	cfg.Targets = append(cfg.Targets, mobileTarget)
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath, "mobile/package.json")
	runGit(t, repo, "commit", "-m", "configure remote version history")
	runGit(t, repo, "push", "origin", "main")
	runGit(t, repo, "tag", "mobile/v5.2.0")
	runGit(t, repo, "push", "origin", "refs/tags/mobile/v5.2.0")
	runGit(t, repo, "tag", "-d", "mobile/v5.2.0")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "remote version history\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	if pf.LatestGroupTags["mobile"] != "mobile/v5.2.0" || pf.SuggestedVersions["mobile"] != "5.2.1" {
		t.Fatalf("remote tag was not reflected: tags=%v suggestions=%v", pf.LatestGroupTags, pf.SuggestedVersions)
	}

	createTag, pushRemote := true, false
	_, err = svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "manual",
		Versions:          []ReleaseVersionInput{{VersionGroupID: "mobile", TargetVersion: "5.1.0"}},
		SelectedPaths:     []string{"tracked.txt"},
		SelectedTargets:   []store.ReleaseTargetSelection{{TargetID: "mobile", Build: true}},
		StatusFingerprint: pf.StatusFingerprint, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if pe, ok := err.(*Error); !ok || pe.Code != "version_not_newer" {
		t.Fatalf("lower manual version error = %#v", err)
	}

	svc.targetRunner = &recordingTargetRunner{}
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "auto",
		SelectedPaths:     []string{"tracked.txt"},
		SelectedTargets:   []store.ReleaseTargetSelection{{TargetID: "mobile", Build: true}},
		StatusFingerprint: pf.StatusFingerprint, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || run.TagName != "mobile/v5.2.1" || run.TargetVersion != "5.2.1" {
		t.Fatalf("auto version ignored remote namespaced tag: %+v", run)
	}
}

func TestRepositoryScopedReleaseRejectsVersionGroupKeys(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writePublisherFixture(t, repo, "mobile/package.json", `{"name":"mobile","version":"5.0.0"}`)
	cfg := validExecutorConfig(validExecutorTarget())
	cfg.VersionGroups = append(cfg.VersionGroups, releaseconfig.VersionGroup{
		ID: "mobile", Name: "移动端版本", TagPrefix: "mobile", CurrentVersion: "5.0.0",
		VersionFiles: []releaseconfig.VersionFile{{Path: "mobile/package.json", Format: "json", JSONPointer: "/version"}},
	})
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath, "mobile/package.json")
	runGit(t, repo, "commit", "-m", "configure repository scope")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "repository scope\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag, pushRemote := true, false
	for _, versions := range [][]ReleaseVersionInput{
		{{VersionGroupID: "mobile", TargetVersion: "5.1.0"}},
		{{VersionGroupID: "repository", TargetVersion: "0.1.0"}, {VersionGroupID: "mobile", TargetVersion: "5.1.0"}},
	} {
		_, err := svc.Start(context.Background(), "app1", CreateRequest{
			CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "manual", TargetVersion: "0.1.0",
			Versions: versions, SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
			ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
		})
		if pe, ok := err.(*Error); !ok || pe.Code != "invalid_version_group" {
			t.Fatalf("repository-scoped versions %#v error = %#v", versions, err)
		}
	}
}

func TestRetryRejectsChangedCommitBeforeRemoteMutation(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	frozenCommit := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	pushRemote := true
	plan := &executionPlan{SchemaVersion: executionPlanSchemaVersion, PushRemote: &pushRemote, VersionGroups: []planVersionGroup{}, Targets: []planTarget{}}
	planJSON, err := plan.marshal()
	if err != nil {
		t.Fatal(err)
	}
	run := &store.ReleaseRun{
		ID: "changed-head-retry", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin",
		CreateTag: false, PushRemote: true, CommitSHA: frozenCommit, ExecutionPlan: planJSON,
		Status: "failed", Stage: "pushing_branch", ErrorCode: "push_branch_failed",
	}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "--allow-empty", "-m", "move head after failure")
	if _, err := svc.Retry(run.ID, RetryRequest{ExternalActionsConfirmed: true}); err == nil {
		t.Fatal("retry accepted a different HEAD")
	} else if pe, ok := err.(*Error); !ok || pe.Code != "release_commit_changed" {
		t.Fatalf("changed HEAD retry error = %#v", err)
	}
	remoteHead := strings.TrimSpace(runGit(t, repo, "rev-parse", "origin/main"))
	if remoteHead != frozenCommit {
		t.Fatalf("remote changed during rejected retry: got %s want %s", remoteHead, frozenCommit)
	}
}

func TestTargetCommandChangingHeadStopsRelease(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	target := validExecutorTarget()
	target.Steps = releaseconfig.Steps{Build: "git commit --allow-empty -m target-moved-head"}
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", validExecutorConfig(target)); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath)
	runGit(t, repo, "commit", "-m", "configure head guard")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "head guard\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	createTag, pushRemote := false, false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "auto",
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		SelectedTargets: []store.ReleaseTargetSelection{{TargetID: target.ID, Build: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "failed" || run.ErrorCode != "release_commit_changed" || run.Stage != "target_build" {
		t.Fatalf("HEAD-changing target was not blocked: %+v", run)
	}
	if tags := strings.TrimSpace(runGit(t, repo, "tag", "--list")); tags != "" {
		t.Fatalf("HEAD-changing target unexpectedly created tags: %s", tags)
	}
}

func TestFrozenArtifactVerificationDetectsReplacement(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writePublisherFixture(t, repo, "dist/app.zip", "first")
	run := &store.ReleaseRun{ID: "artifact-guard", AppID: "app1", RepoRoot: repo, Status: "running", Stage: "target_publish"}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "dist", "app.zip")
	hash, err := hashArtifact(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if err := svc.store.AddReleaseArtifact(run.ID, "web", "dist/app.zip", info.Size(), hash); err != nil {
		t.Fatal(err)
	}
	target := planTarget{ID: "web", Name: "Web", Artifacts: []string{"dist/*.zip"}}
	if err := svc.verifyFrozenTargetArtifacts(run, target); err != nil {
		t.Fatalf("unchanged artifact rejected: %v", err)
	}
	writeTestFile(t, path, "other")
	if err := svc.verifyFrozenTargetArtifacts(run, target); err == nil {
		t.Fatal("replaced artifact was accepted")
	}
}

func writePublisherFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContains(t *testing.T, path string, wanted ...string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range wanted {
		if !strings.Contains(string(raw), value) {
			t.Fatalf("%s missing %q:\n%s", path, value, raw)
		}
	}
}
