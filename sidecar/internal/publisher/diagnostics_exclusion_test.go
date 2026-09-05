package publisher

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func writeDiagnosticsTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, content)
}

func TestParseChangesExcludesOnlyUntrackedDiagnostics(t *testing.T) {
	raw := "?? .launcher/diagnostics/events-2026-09-05.jsonl\x00" +
		" M .launcher/diagnostics/tracked.jsonl\x00" +
		"?? ordinary.txt\x00"
	changes := parseChanges(raw)
	if len(changes) != 2 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes[0].Path != ".launcher/diagnostics/tracked.jsonl" || !changes[0].Tracked {
		t.Fatalf("tracked diagnostics change was lost: %+v", changes)
	}
	if changes[1].Path != "ordinary.txt" || changes[1].Tracked {
		t.Fatalf("ordinary untracked change mismatch: %+v", changes)
	}

	lineChanges := parseChanges("?? .launcher/diagnostics/events.jsonl\n M .launcher/diagnostics/tracked.jsonl\n?? ordinary.txt\n")
	if len(lineChanges) != 2 || lineChanges[0].Path != ".launcher/diagnostics/tracked.jsonl" || lineChanges[1].Path != "ordinary.txt" {
		t.Fatalf("line-oriented changes = %+v", lineChanges)
	}
}

func TestDiagnosticsDoNotChangeStatusFingerprint(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "ordinary.txt"), "ordinary\n")
	writeDiagnosticsTestFile(t, filepath.Join(repo, ".launcher", "diagnostics", "events.jsonl"), "first\n")
	baseRaw := "?? ordinary.txt\x00"
	withDiagnostics := "?? .launcher/diagnostics/events.jsonl\x00" + baseRaw
	base := statusFingerprint(repo, "head", baseRaw, parseChanges(baseRaw))
	got := statusFingerprint(repo, "head", withDiagnostics, parseChanges(withDiagnostics))
	if got != base {
		t.Fatalf("untracked diagnostics changed fingerprint: got %s want %s", got, base)
	}

	trackedRaw := " M .launcher/diagnostics/events.jsonl\x00" + baseRaw
	tracked := statusFingerprint(repo, "head", trackedRaw, parseChanges(trackedRaw))
	if tracked == base {
		t.Fatal("tracked diagnostics change must affect fingerprint")
	}
}

func TestPreflightAndSnapshotsIgnoreOnlyUntrackedDiagnostics(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()

	trackedPath := filepath.Join(repo, ".launcher", "diagnostics", "tracked.jsonl")
	writeDiagnosticsTestFile(t, trackedPath, "initial\n")
	runGit(t, repo, "add", ".launcher/diagnostics/tracked.jsonl")
	runGit(t, repo, "commit", "-m", "track selected diagnostic")
	runGit(t, repo, "push", "origin", "main")

	writeTestFile(t, trackedPath, "changed\n")
	writeDiagnosticsTestFile(t, filepath.Join(repo, ".launcher", "diagnostics", "events-2026-09-05.jsonl"), "event one\n")
	writeTestFile(t, filepath.Join(repo, "ordinary.txt"), "ordinary\n")

	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileChange{}
	for _, change := range pf.Changes {
		byPath[change.Path] = change
	}
	if _, ok := byPath[".launcher/diagnostics/events-2026-09-05.jsonl"]; ok {
		t.Fatalf("untracked diagnostics leaked into preflight: %+v", pf.Changes)
	}
	if change, ok := byPath[".launcher/diagnostics/tracked.jsonl"]; !ok || !change.Tracked {
		t.Fatalf("tracked diagnostics missing from preflight: %+v", pf.Changes)
	}
	if _, ok := byPath["ordinary.txt"]; !ok {
		t.Fatalf("ordinary untracked file missing from preflight: %+v", pf.Changes)
	}

	fingerprint := pf.StatusFingerprint
	writeDiagnosticsTestFile(t, filepath.Join(repo, ".launcher", "diagnostics", "events-2026-09-05.jsonl"), "event two\n")
	writeDiagnosticsTestFile(t, filepath.Join(repo, ".launcher", "diagnostics", "another.jsonl"), "another\n")
	pfAfter, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if pfAfter.StatusFingerprint != fingerprint {
		t.Fatal("diagnostics append/create changed preflight fingerprint")
	}

	if _, err := validateSelected([]string{".launcher/diagnostics/events-2026-09-05.jsonl"}, []FileChange{{
		Path: ".launcher/diagnostics/events-2026-09-05.jsonl", Status: "??", Tracked: false,
	}}); err == nil {
		t.Fatal("untracked diagnostics path must never be selectable for git add")
	}
	selected, err := validateSelected([]string{".launcher/diagnostics/tracked.jsonl"}, pfAfter.Changes)
	if err != nil || len(selected) != 1 {
		t.Fatalf("tracked diagnostics path should remain selectable: selected=%v err=%v", selected, err)
	}

	before, err := svc.worktreeSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	writeDiagnosticsTestFile(t, filepath.Join(repo, ".launcher", "diagnostics", "during-build.jsonl"), "build event\n")
	after, err := svc.worktreeSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if ok, changed := verifyBuildSideEffects(before, after, nil); !ok {
		t.Fatalf("untracked diagnostics was treated as a build side effect: %v", changed)
	}

	writeTestFile(t, trackedPath, "changed again\n")
	afterTracked, err := svc.worktreeSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if ok, changed := verifyBuildSideEffects(after, afterTracked, nil); ok || len(changed) != 1 || changed[0] != ".launcher/diagnostics/tracked.jsonl" {
		t.Fatalf("tracked diagnostics side effect must be reported: ok=%v changed=%v", ok, changed)
	}
}

func TestPreflightBlocksUntrackedDiagnosticsVersionFile(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()

	versionPath := filepath.Join(repo, ".launcher", "diagnostics", "version.json")
	writeDiagnosticsTestFile(t, versionPath, "{\"version\":\"1.0.0\"}\n")
	cfg := &releaseconfig.Config{
		SchemaVersion: releaseconfig.SchemaVersion,
		VersionGroups: []releaseconfig.VersionGroup{{
			ID: "product", Name: "Product", TagPrefix: "product",
			VersionFiles: []releaseconfig.VersionFile{{
				Path: ".launcher/diagnostics/version.json", Format: "json", JSONPointer: "/version",
			}},
		}},
		Targets:  []releaseconfig.Target{},
		Warnings: []string{},
	}
	if _, err := svc.releaseConfig.Put(context.Background(), "app1", cfg); err != nil {
		t.Fatal(err)
	}
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if pf.CanRelease || !hasIssue(pf, "diagnostics_version_file_untracked") {
		t.Fatalf("untracked diagnostics version file must block release: %+v", pf.BlockingIssues)
	}
	for _, change := range pf.Changes {
		if change.Path == ".launcher/diagnostics/version.json" {
			t.Fatalf("untracked diagnostics version file leaked into preflight: %+v", pf.Changes)
		}
	}

	runGit(t, repo, "add", ".launcher/diagnostics/version.json")
	paths, err := svc.untrackedDiagnosticsPaths(context.Background(), repo, []string{".launcher/diagnostics/version.json"})
	if err != nil || len(paths) != 0 {
		t.Fatalf("tracked diagnostics file must not be excluded: paths=%v err=%v", paths, err)
	}
}
