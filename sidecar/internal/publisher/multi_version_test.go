package publisher

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestReleaseMultipleVersionGroupsCreatesIndependentTags(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()

	writePublisherFixture(t, repo, "server/version.json", `{"version":"3.4.0","keep":true}`)
	web := validExecutorTarget()
	web.Name = "Web"
	web.Steps = releaseconfig.Steps{Build: "build-web-${VERSION}-${TAG}"}
	server := validExecutorTarget()
	server.ID = "server"
	server.Name = "服务端"
	server.Kind = "server"
	server.VersionGroup = "server"
	server.Steps = releaseconfig.Steps{Build: "build-server-${VERSION}-${TAG}"}
	cfg := &releaseconfig.Config{
		SchemaVersion: releaseconfig.SchemaVersion,
		VersionGroups: []releaseconfig.VersionGroup{
			{ID: "product", Name: "Web 版本", TagPrefix: "web", CurrentVersion: "1.0.0", VersionFiles: []releaseconfig.VersionFile{{Path: "package.json", Format: "json", JSONPointer: "/version"}}},
			{ID: "server", Name: "服务端版本", TagPrefix: "server", CurrentVersion: "3.4.0", VersionFiles: []releaseconfig.VersionFile{{Path: "server/version.json", Format: "json", JSONPointer: "/version"}}},
		},
		Targets: []releaseconfig.Target{web, server}, Warnings: []string{},
	}
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath, "server/version.json")
	runGit(t, repo, "commit", "-m", "configure independent versions")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "multi-version release\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	svc.targetRunner = &recordingTargetRunner{}
	createTag := true
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, VersionMode: "auto", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		SelectedTargets: []store.ReleaseTargetSelection{{TargetID: "web", Build: true}, {TargetID: "server", Build: true}},
		ReleaseNotes:    testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" {
		t.Fatalf("release failed: %+v", run)
	}
	wantVersions := []store.ReleaseVersion{
		{VersionGroupID: "product", VersionGroupName: "Web 版本", TargetVersion: "1.0.1", TagName: "web/v1.0.1"},
		{VersionGroupID: "server", VersionGroupName: "服务端版本", TargetVersion: "3.4.1", TagName: "server/v3.4.1"},
	}
	if !reflect.DeepEqual(run.Versions, wantVersions) {
		t.Fatalf("versions = %#v, want %#v", run.Versions, wantVersions)
	}
	assertFileContains(t, filepath.Join(repo, "package.json"), `"version": "1.0.1"`)
	assertFileContains(t, filepath.Join(repo, "server/version.json"), `"version":"3.4.1"`, `"keep":true`)
	if got, want := svc.targetRunner.(*recordingTargetRunner).Commands(), []string{"build-web-1.0.1-web/v1.0.1", "build-server-3.4.1-server/v3.4.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
	for _, version := range wantVersions {
		localCommit := strings.TrimSpace(runGit(t, repo, "rev-parse", version.TagName+"^{}"))
		remoteCommit := runGit(t, repo, "ls-remote", "origin", "refs/tags/"+version.TagName+"^{}")
		if localCommit != run.CommitSHA || !strings.Contains(remoteCommit, run.CommitSHA) {
			t.Fatalf("tag %s does not point to release commit: local=%s remote=%s commit=%s", version.TagName, localCommit, remoteCommit, run.CommitSHA)
		}
	}

	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "manual multi-version release\n")
	pf, err = svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("second preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	svc.targetRunner.(*recordingTargetRunner).Reset()
	run, err = svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, VersionMode: "manual", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		Versions:        []ReleaseVersionInput{{VersionGroupID: "product", TargetVersion: "1.2.0"}, {VersionGroupID: "server", TargetVersion: "4.0.0"}},
		SelectedTargets: []store.ReleaseTargetSelection{{TargetID: "web", Build: true}, {TargetID: "server", Build: true}},
		ReleaseNotes:    testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || len(run.Versions) != 2 || run.Versions[0].TagName != "web/v1.2.0" || run.Versions[1].TagName != "server/v4.0.0" {
		t.Fatalf("manual independent versions failed: %+v", run)
	}
	assertFileContains(t, filepath.Join(repo, "package.json"), `"version": "1.2.0"`)
	assertFileContains(t, filepath.Join(repo, "server/version.json"), `"version":"4.0.0"`)
}

func TestSourceOnlySingleVersionGroupUsesConfiguredVersion(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writePublisherFixture(t, repo, "service/version.json", `{"version":"3.4.0","keep":true}`)
	cfg := &releaseconfig.Config{
		SchemaVersion: releaseconfig.SchemaVersion,
		VersionGroups: []releaseconfig.VersionGroup{{
			ID: "service", Name: "服务版本", TagPrefix: "service", CurrentVersion: "3.4.0",
			VersionFiles: []releaseconfig.VersionFile{{Path: "service/version.json", Format: "json", JSONPointer: "/version"}},
		}},
		Targets: []releaseconfig.Target{}, Warnings: []string{},
	}
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	if version, err := readConfiguredVersion(repo, cfg.VersionGroups[0].VersionFiles[0]); err != nil || version != "3.4.0" {
		t.Fatalf("configured version before release = %q, err=%v", version, err)
	}
	saved, err := svc.releaseConfig.Get(context.Background(), "app1")
	if err != nil || len(saved.VersionGroups) != 1 || len(saved.VersionGroups[0].VersionFiles) != 1 {
		t.Fatalf("saved release config = %+v, err=%v", saved, err)
	}
	if version, err := readConfiguredVersion(repo, saved.VersionGroups[0].VersionFiles[0]); err != nil || version != "3.4.0" {
		t.Fatalf("saved configured version = %q, file=%+v, err=%v", version, saved.VersionGroups[0].VersionFiles[0], err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath, "service/version.json")
	runGit(t, repo, "commit", "-m", "configure service version")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "source-only single group\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	if pf.SuggestedVersion != "3.4.1" || !reflect.DeepEqual(pf.VersionFiles, []string{"service/version.json"}) {
		t.Fatalf("source-only preflight = version %s, files %#v", pf.SuggestedVersion, pf.VersionFiles)
	}
	createTag, pushRemote := true, false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "auto",
		TargetVersion: pf.SuggestedVersion,
		Versions:      []ReleaseVersionInput{{VersionGroupID: "repository", TargetVersion: pf.SuggestedVersion}},
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || len(run.Versions) != 1 || run.Versions[0].VersionGroupID != "service" || run.TagName != "v3.4.1" {
		t.Fatalf("source-only single-group release = %+v", run)
	}
	assertFileContains(t, filepath.Join(repo, "service/version.json"), `"version":"3.4.1"`, `"keep":true`)
	plan, err := parseExecutionPlan(run.ExecutionPlan)
	if err != nil || len(plan.Targets) != 0 || len(plan.VersionGroups) != 1 || !plan.usesConfiguredVersionGroups() {
		t.Fatalf("frozen source-only plan = %+v, err=%v", plan, err)
	}
}

func TestSourceOnlyMultipleVersionGroupsStayRepositoryScoped(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writePublisherFixture(t, repo, "server/version.json", `{"version":"3.4.0","keep":true}`)
	cfg := &releaseconfig.Config{
		SchemaVersion: releaseconfig.SchemaVersion,
		VersionGroups: []releaseconfig.VersionGroup{
			{ID: "product", Name: "产品版本", TagPrefix: "product", CurrentVersion: "1.0.0", VersionFiles: []releaseconfig.VersionFile{{Path: "package.json", Format: "json", JSONPointer: "/version"}}},
			{ID: "server", Name: "服务端版本", TagPrefix: "server", CurrentVersion: "3.4.0", VersionFiles: []releaseconfig.VersionFile{{Path: "server/version.json", Format: "json", JSONPointer: "/version"}}},
		},
		Targets: []releaseconfig.Target{}, Warnings: []string{},
	}
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", releaseconfig.ManifestPath, "server/version.json")
	runGit(t, repo, "commit", "-m", "configure independent versions")
	runGit(t, repo, "push", "origin", "main")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "source-only repository release\n")

	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight failed: %v %+v", err, pf.BlockingIssues)
	}
	if pf.SuggestedVersion != "0.1.0" || len(pf.VersionFiles) != 0 {
		t.Fatalf("repository-scoped preflight = version %s, files %#v", pf.SuggestedVersion, pf.VersionFiles)
	}
	createTag, pushRemote := true, false
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		CreateTag: &createTag, PushRemote: &pushRemote, VersionMode: "auto", TargetVersion: pf.SuggestedVersion,
		Versions:      []ReleaseVersionInput{{VersionGroupID: "repository", TargetVersion: pf.SuggestedVersion}},
		SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" || len(run.Versions) != 1 || run.Versions[0].VersionGroupID != "repository" || run.TagName != "v0.1.0" {
		t.Fatalf("source-only repository release = %+v", run)
	}
	assertFileContains(t, filepath.Join(repo, "package.json"), `"version": "1.0.0"`)
	assertFileContains(t, filepath.Join(repo, "server/version.json"), `"version":"3.4.0"`)
	plan, err := parseExecutionPlan(run.ExecutionPlan)
	if err != nil || len(plan.Targets) != 0 || len(plan.VersionGroups) != 2 || plan.NamespacedTags || plan.usesConfiguredVersionGroups() {
		t.Fatalf("frozen repository plan = %+v, err=%v", plan, err)
	}
}
