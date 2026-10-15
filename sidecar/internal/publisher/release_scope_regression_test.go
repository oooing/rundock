package publisher

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

// Opt-in, read-only probe of a user's sample; no Git writes, candidate creation,
// building or publication. Normal CI uses the self-contained tests below.
func TestRealPartialBackupScopeProbe(t *testing.T) {
	root := os.Getenv("RUNDOCK_SCOPE_PROBE_ROOT")
	if root == "" {
		t.Skip("no real sample requested")
	}
	backupRoot := "artifacts/desktop-ui-sync-20261012/before-src"
	files := []string{"components/layout/WebNavbar.jsx", "components/search/SearchBar.jsx", "components/video/VideoCard.jsx", "App.jsx"}
	backups, sources := []string{}, []string{}
	for _, rel := range files {
		backup := filepath.ToSlash(filepath.Join(backupRoot, rel))
		if !fileExists(filepath.Join(root, backup)) {
			t.Fatalf("sample missing: %s", backup)
		}
		if got := classifyOne(root, FileChange{Path: backup, Status: "??"}, nil); got.Category != CategoryLocal || got.SelectedDefault {
			t.Fatalf("sample backup still default-selected: %+v", got)
		}
		backups = append(backups, backup)
		sources = append(sources, filepath.ToSlash(filepath.Join("code/frontend/src", rel)))
	}
	if got := findMissingLocalDependencies(root, root, sources, nil); hasBlockingDependency(got) {
		t.Fatalf("actual source dependencies failed: %+v", got)
	}
	got := findMissingLocalDependencies(root, root, backups, nil)
	if len(got) == 0 || hasBlockingDependency(got) {
		t.Fatal("partial backup must produce only reference advisories")
	}
	t.Logf("four actual source files passed; four backup files default local; explicit backup scan found %d missing references", len(got))
}

func TestVerificationMaterialsDefaultLocalWithExplicitOverrides(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"artifacts/desktop-ui-sync/before-src/App.jsx", "outputs/ui/entry.ts", "reports/snapshot/main.py"} {
		writeTestFile(t, filepath.Join(root, rel), "source\n")
		change := FileChange{Path: rel, Status: "??"}
		item := classifyOne(root, change, nil)
		if item.Category != CategoryLocal || item.SelectedDefault {
			t.Fatalf("verification material default-selected: %+v", item)
		}
		tracked := change
		tracked.Tracked, tracked.Status = true, " M"
		if got := classifyOne(root, tracked, nil); got.Category != CategoryRecommend || !got.SelectedDefault {
			t.Fatalf("tracked content silently excluded: %+v", got)
		}
		rule := releaseconfig.FileRule{ID: "official", Pattern: rel, Kind: releaseconfig.RuleRecommend}
		if got := classifyOne(root, change, []releaseconfig.FileRule{rule}); got.Category != CategoryRecommend || !got.SelectedDefault {
			t.Fatalf("explicit project rule lost: %+v", got)
		}
		decision := ManualDecision{Path: rel, Decision: DecisionInclude, ContentFingerprint: item.ContentFingerprint}
		if got := classifyChanges(root, []FileChange{change}, nil, []ManualDecision{decision})[0]; !got.SelectedDefault {
			t.Fatalf("manual inclusion lost: %+v", got)
		}
	}
	for _, rel := range []string{"src/artifacts/module.ts", "artifacts-old/module.ts", "packages/outputs/module.ts"} {
		if got := classifyOne(root, FileChange{Path: rel, Status: "??"}, nil); !got.SelectedDefault {
			t.Fatalf("source outside root evidence folder excluded: %+v", got)
		}
	}
}

func TestPartialBackupDoesNotBlockRealSourceButExplicitInclusionStillChecks(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	backup := "artifacts/desktop-ui-sync-20261012/before-src/components/search/SearchBar.jsx"
	writeTestFile(t, filepath.Join(repo, backup), "import './SearchBar.css';\n")
	writeTestFile(t, filepath.Join(repo, "src/SearchBar.jsx"), "import './SearchBar.css';\n")
	writeTestFile(t, filepath.Join(repo, "src/SearchBar.css"), ".search {}\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	selected := defaultSelectedPaths(pf.Classifications)
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: selected, Intent: IntentSaveProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasBlockingDependency(view.DependencyFindings) || fileExists(filepath.Join(candidateWork(t, svc, view.ID), backup)) {
		t.Fatalf("partial backup affected default release: %+v", view.DependencyFindings)
	}
	selected = append(selected, backup)
	view, err = svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: selected, Intent: IntentSaveProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.DependencyFindings) == 0 || hasBlockingDependency(view.DependencyFindings) || view.DependencyFindings[0].Kind != "missing" {
		t.Fatalf("explicitly selected backup must be advisory: %+v", view.DependencyFindings)
	}
}

func TestDependencyMissingExtensionlessAndResourceModifiers(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "src/main.ts"), "import './missing-helper'; import icon from './icon.svg?url'; import './theme.css?inline';\n")
	writeTestFile(t, filepath.Join(root, "src/icon.svg"), "<svg/>\n")
	writeTestFile(t, filepath.Join(root, "src/theme.css"), "body {}\n")
	got := findMissingLocalDependencies(root, root, []string{"src/main.ts"}, nil)
	if len(got) != 1 || got[0].Blocked || got[0].Kind != "unresolved" || got[0].Missing != "src/missing-helper" {
		t.Fatalf("unresolved imports must be advisory, not bundler modifier errors: %+v", got)
	}
	writeTestFile(t, filepath.Join(root, "src/missing-helper.ts"), "export {}\n")
	if got := findMissingLocalDependencies(root, root, []string{"src/main.ts"}, nil); len(got) != 0 {
		t.Fatalf("restored dependency still blocks: %+v", got)
	}
	other := t.TempDir()
	writeTestFile(t, filepath.Join(other, "src/main.ts"), "import './missing-helper';\n")
	got = findMissingLocalDependencies(other, root, []string{"src/main.ts"}, nil)
	if len(got) != 1 || got[0].Kind != "not_selected" || got[0].Missing != "src/missing-helper.ts" {
		t.Fatalf("omitted file confused with absent file: %+v", got)
	}
}

func TestInferredDependenciesNeverBlockRelease(t *testing.T) {
	cases := []struct{ name, file, content string }{
		{"generated module", "src/main.ts", "import './generated-client';"},
		{"typescript emitted suffix", "src/main.ts", "import './helper.js';"},
		{"shadowed require", "src/main.js", "function require(x) { return x }; require('./sample');"},
		{"python comment", "main.py", "# from .missing import demo\n"},
		{"python string", "main.py", "example = 'from .missing import demo'\n"},
		{"python module", "main.py", "from .generated import demo\n"},
		{"outside root", "main.js", "import '../outside.js';"},
		{"backup source", "artifacts/before-src/main.jsx", "import './style.css';"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, tc.file), tc.content)
			writeTestFile(t, filepath.Join(root, "src/helper.ts"), "export {};\n")
			got := findMissingLocalDependencies(root, root, []string{tc.file}, nil)
			if hasBlockingDependency(got) {
				t.Fatalf("inference blocked release: %+v", got)
			}
			if filepath.Ext(tc.file) == ".py" && len(got) != 0 {
				t.Fatalf("Python regex inference retained: %+v", got)
			}
		})
	}
}

func TestUnreadableSelectionStillBlocksButDeletionDoesNot(t *testing.T) {
	root := t.TempDir()
	got := findMissingLocalDependencies(root, root, []string{"missing.txt"}, nil)
	if len(got) != 1 || got[0].Kind != "unreadable" || !hasBlockingDependency(got) {
		t.Fatalf("unreadable selection bypassed checks: %+v", got)
	}
	got = findMissingLocalDependencies(root, root, []string{"deleted.txt"}, []FileClassification{{Path: "deleted.txt", Status: " D"}})
	if len(got) != 0 {
		t.Fatalf("intentional deletion reported unreadable: %+v", got)
	}
}

func TestAdvisoryDoesNotBypassRequiredProjectValidation(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "src/main.js"), "import './generated.js';\n")
	writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{ID: "project", Name: "项目检查", Required: true, Command: "exit 7"}})
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"src/main.js"}, Intent: IntentFormal})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.DependencyFindings) == 0 || hasBlockingDependency(view.DependencyFindings) {
		t.Fatalf("expected nonblocking reference: %+v", view)
	}
	view, err = svc.RunCandidateChecks(context.Background(), "app1", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Accepted || view.CanFormal || view.Status != CheckFailed {
		t.Fatalf("required check bypassed: %+v", view)
	}
}
