package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in local acceptance only. No private source or sample is embedded here.
func TestParentCaseReadOnly(t *testing.T) {
	repo := os.Getenv("RUNDOCK_CASE_REPO")
	if repo == "" {
		t.Skip("private case path not supplied")
	}
	t.Setenv("GIT_OPTIONAL_LOCKS", "0")
	configRaw, err := os.ReadFile(os.Getenv("RUNDOCK_CASE_PLAN"))
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Rules        []releaseconfig.FileRule
		ProbeFiles   []string
		CheckCommand string
	}
	if err = json.Unmarshal(configRaw, &plan); err != nil {
		t.Fatal(err)
	}
	head := runGit(t, repo, "rev-parse", "HEAD")
	index := filepath.Join(repo, ".git", "index")
	beforeIndex, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	changes := parseChanges(runGit(t, repo, "status", "--porcelain=v1", "-z", "--untracked-files=all"))
	beforeFiles := map[string]string{}
	for _, c := range changes {
		beforeFiles[c.Path] = fileContentFingerprint(repo, c)
	}
	classes := classifyChanges(repo, changes, plan.Rules, nil)
	counts := map[string]int{}
	localSelected := 0
	sourceAdded := 0
	baselineLocal := 0
	for _, c := range classes {
		counts[c.Category]++
		if c.Category == CategoryLocal {
			if c.SelectedDefault {
				localSelected++
			}
			if c.BaselineKept {
				baselineLocal++
			}
		}
		if c.Category == CategoryRecommend && c.SelectedDefault && !c.Tracked {
			sourceAdded++
		}
	}
	if localSelected != 0 || sourceAdded == 0 || counts[CategoryLocal] < 100 {
		t.Fatalf("unexpected grouping: %v selectedLocal=%d sourceAdded=%d", counts, localSelected, sourceAdded)
	}
	isolated := t.TempDir()
	for _, rel := range plan.ProbeFiles {
		abs, err := secureProjectPath(repo, rel, false)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(isolated, filepath.FromSlash(rel)), string(raw))
	}
	status, log, reason := runProfileCommand(context.Background(), isolated, releaseconfig.CheckProfile{ID: "case-missing", Command: plan.CheckCommand, Required: true, TimeoutSeconds: 30})
	if status != CheckFailed || !strings.Contains(log, "ModuleNotFoundError") || !strings.Contains(log, "asr_span_candidates") {
		t.Fatalf("omitted implementation did not surface: status=%s reason=%s log=%s", status, reason, log)
	}
	afterIndex, _ := os.ReadFile(index)
	if sha256.Sum256(beforeIndex) != sha256.Sum256(afterIndex) || runGit(t, repo, "rev-parse", "HEAD") != head {
		t.Fatal("real repo index or HEAD changed")
	}
	for _, c := range changes {
		if beforeFiles[c.Path] != fileContentFingerprint(repo, c) {
			t.Fatal("real source changed during probe")
		}
	}
	result := map[string]any{"counts": counts, "untrackedSourceRecommended": sourceAdded, "trackedLocalBaselineWarning": baselineLocal, "privateAutomaticallySelected": localSelected, "isolatedMissingImplementation": status, "checkLog": log, "headAndIndexUnchanged": true, "changedFilesUnmodified": len(changes)}
	raw, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(os.Getenv("RUNDOCK_CASE_RESULT"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("read-only classification %v; isolated omission failed as expected; %d files unchanged", counts, len(changes))
}
