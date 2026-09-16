package publisher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestClassifyTemplatesSecretsConflictsAndRetention(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, ".env.example"), "API_KEY=YOUR_API_KEY_HERE\n")
	writeTestFile(t, filepath.Join(repo, ".env"), "API_KEY=sk_live_realvalue123456\n")
	writeTestFile(t, filepath.Join(repo, "src/mod.go"), "package src\n")
	writeTestFile(t, filepath.Join(repo, "tmp/cache.bin"), "x")
	writeManifestRules(t, repo, []releaseconfig.FileRule{
		{ID: "src", Pattern: "src/", Kind: "recommend", Reason: "正式源码"},
		{ID: "src-local", Pattern: "src/", Kind: "local", Reason: "冲突排除"},
		{ID: "tmp", Pattern: "tmp/", Kind: "local", Reason: "缓存"},
	})
	items := classifyChanges(repo, []FileChange{
		{Path: ".env.example", Status: "??"},
		{Path: ".env", Status: "??"},
		{Path: "src/mod.go", Status: "??"},
		{Path: "tmp/cache.bin", Status: "??"},
		{Path: "tracked.txt", Status: " M", Tracked: true},
	}, mustRules(t, repo), nil)
	byPath := map[string]FileClassification{}
	for _, item := range items {
		byPath[item.Path] = item
	}
	if byPath["src/mod.go"].Category != CategoryReview {
		t.Fatalf("rule conflict should be review: %+v", byPath["src/mod.go"])
	}
	if byPath["tmp/cache.bin"].Category != CategoryLocal || byPath["tmp/cache.bin"].SelectedDefault {
		t.Fatalf("local cache: %+v", byPath["tmp/cache.bin"])
	}
	if !byPath["tracked.txt"].SelectedDefault {
		t.Fatalf("tracked should default selected: %+v", byPath["tracked.txt"])
	}
	targets, err := readScanTargets(repo, []string{".env.example", ".env"})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := scanSensitiveContent(targets)
	if err != nil {
		t.Fatal(err)
	}
	hasEnv, hasExample := false, false
	for _, finding := range findings {
		if finding.Path == ".env" {
			hasEnv = true
			if strings.Contains(finding.Redacted, "sk_live_realvalue123456") {
				t.Fatalf("secret leaked: %+v", finding)
			}
		}
		if finding.Path == ".env.example" {
			hasExample = true
		}
	}
	if !hasEnv || hasExample {
		t.Fatalf("template vs real secret: %+v", findings)
	}
	first := classifyChanges(repo, []FileChange{{Path: "src/mod.go", Status: "??"}}, mustRules(t, repo), []ManualDecision{
		{Path: "src/mod.go", Decision: DecisionInclude, Reason: "正式模块", ContentFingerprint: byPath["src/mod.go"].ContentFingerprint},
	})
	if first[0].Category != CategoryRecommend || !first[0].SelectedDefault {
		t.Fatalf("manual include not applied: %+v", first[0])
	}
	writeTestFile(t, filepath.Join(repo, "src/mod.go"), "package src\nchanged\n")
	second := classifyChanges(repo, []FileChange{{Path: "src/mod.go", Status: "??"}}, mustRules(t, repo), []ManualDecision{
		{Path: "src/mod.go", Decision: DecisionInclude, Reason: "正式模块", ContentFingerprint: first[0].ContentFingerprint},
	})
	if !strings.Contains(strings.Join(second[0].Reasons, " "), "内容已变化") {
		t.Fatalf("changed content should require reconfirm: %+v", second[0])
	}
	_ = svc
}

func TestTrackedBaselineSecretAndExcludedTrackedFile(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "secret.txt"), "TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789\n")
	runGit(t, repo, "add", "secret.txt")
	runGit(t, repo, "commit", "-m", "secret")
	writeTestFile(t, filepath.Join(repo, "secret.txt"), "TOKEN=not-a-secret\n")
	writeManifestRules(t, repo, []releaseconfig.FileRule{{ID: "txt", Pattern: "*.txt", Kind: "local", Reason: "文本资料"}})
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	var secret FileClassification
	for _, item := range pf.Classifications {
		if item.Path == "secret.txt" {
			secret = item
		}
	}
	if !secret.BaselineKept {
		t.Fatalf("tracked excluded file must keep baseline: %+v", secret)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: nil, Intent: IntentSaveProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.SensitiveFindings) == 0 {
		t.Fatalf("baseline secret must be scanned: %+v", view)
	}
	if strings.Contains(view.SensitiveFindings[0].Redacted, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatalf("token leaked")
	}
}

func TestCandidateSpacesChineseRenameDeletionAndMissingDeps(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "docs/更新 优化.md"), "old\n")
	runGit(t, repo, "add", "docs/更新 优化.md")
	runGit(t, repo, "commit", "-m", "docs")
	runGit(t, repo, "mv", "docs/更新 优化.md", "docs/新 文件.md")
	writeTestFile(t, filepath.Join(repo, "docs/新 文件.md"), "new\n")
	os.Remove(filepath.Join(repo, "tracked.txt"))
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	writeTestFile(t, filepath.Join(repo, "src/app.js"), "import './helper.js'\n")
	writeTestFile(t, filepath.Join(repo, "src/helper.js"), "export const x = 1\n")
	writeTestFile(t, filepath.Join(repo, "src/private.js"), "export const secret = 1\n")
	writeTestFile(t, filepath.Join(repo, "src/used.js"), "import './private.js'\n")
	writeManifestRules(t, repo, []releaseconfig.FileRule{{ID: "priv", Pattern: "src/private.js", Kind: "local", Reason: "实验实现"}})
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint,
		SelectedPaths:     []string{"docs/新 文件.md", "tracked.txt", "src/app.js", "src/used.js"},
		Intent:            IntentSaveProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	gotDoc, err := os.ReadFile(filepath.Join(candidateWork(t, svc, view.ID), "docs/新 文件.md"))
	if err != nil || strings.TrimSpace(string(gotDoc)) != "new" {
		t.Fatalf("rename/chinese/spaces missing: %v %s", err, gotDoc)
	}
	if _, err := os.Stat(filepath.Join(candidateWork(t, svc, view.ID), "tracked.txt")); !os.IsNotExist(err) && fileExists(filepath.Join(candidateWork(t, svc, view.ID), "tracked.txt")) {
		t.Fatalf("deleted tracked file should be absent from candidate")
	}
	missingHelper, blockedPrivate := false, false
	for _, dep := range view.DependencyFindings {
		if dep.Missing == "src/helper.js" {
			missingHelper = true
		}
		if dep.Missing == "src/private.js" && dep.Blocked {
			blockedPrivate = true
		}
	}
	if !missingHelper || !blockedPrivate {
		t.Fatalf("deps: %+v", view.DependencyFindings)
	}
}

func TestFormalStartWithoutCandidateRequiresCheck(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "x\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Start(context.Background(), "app1", CreateRequest{
		Intent: IntentFormal, TargetVersion: "1.0.1", SelectedPaths: []string{"tracked.txt"},
		StatusFingerprint: pf.StatusFingerprint, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true,
	})
	pe, ok := err.(*Error)
	if !ok || pe.Code != "check_required" {
		t.Fatalf("expected check_required, got %#v", err)
	}
}

func TestUnknownIntentRejected(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "x\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Start(context.Background(), "app1", CreateRequest{
		Intent: "ship-it", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
	})
	pe, ok := err.(*Error)
	if !ok || pe.Code != "invalid_intent" {
		t.Fatalf("expected invalid_intent, got %#v", err)
	}
}

func TestCleanCandidateDoesNotCopyPrivateFiles(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "src/app.js"), "import './helper.js'\n")
	writeTestFile(t, filepath.Join(repo, "src/helper.js"), "export default 1\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"src/app.js"}, Intent: IntentSaveProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(candidateWork(t, svc, view.ID), "src/helper.js")); !os.IsNotExist(err) {
		t.Fatalf("unselected helper must not be copied")
	}
	found := false
	for _, dep := range view.DependencyFindings {
		if dep.Missing == "src/helper.js" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing helper not reported: %+v", view.DependencyFindings)
	}
}

func TestFingerprintIgnoresStatusLettersAndTracksPlan(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "one\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}, Intent: IntentSaveProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "two\n")
	pf2, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if pf.Changes[0].Status != pf2.Changes[0].Status {
		t.Fatalf("expected same git status letter")
	}
	second, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf2.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}, Intent: IntentSaveProgress,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatalf("content change must change fingerprint")
	}
}

func TestRequiredUnavailableCheckAndCancel(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "src/ok.go"), "package src\n")
	writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{
		ID: "linux-only", Name: "linux", Command: "echo ok", Required: true, OS: []string{"linux"},
	}})
	if runtimeIs("windows") {
		writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{
			ID: "linux-only", Name: "linux", Command: "echo ok", Required: true, OS: []string{"linux"},
		}})
	}
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"src/ok.go"}, Intent: IntentFormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtimeIs("windows") && view.CanFormal {
		for _, result := range view.CheckResults {
			if result.ID == "linux-only" && result.Required && result.Status != CheckUnverified && result.Status != CheckSkipped {
				t.Fatalf("required OS mismatch: %+v", result)
			}
		}
	}
	writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{
		ID: "sleep", Name: "sleep", Command: sleepCommand(), Required: true, TimeoutSeconds: 30,
	}})
	pf, err = svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err = svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"src/ok.go"}, Intent: IntentFormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *CandidateView, 1)
	go func() {
		checked, _ := svc.RunCandidateChecks(context.Background(), "app1", view.ID)
		done <- checked
	}()
	time.Sleep(150 * time.Millisecond)
	cancelled, err := svc.CancelCandidate("app1", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != CheckCancelled && cancelled.Status != CheckRunning {
		t.Fatalf("cancel status: %s", cancelled.Status)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("check did not stop")
	}
}

func TestCheckMutationBlocksReuse(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "src/ok.go"), "package src\n")
	writeCheckProfiles(t, repo, []releaseconfig.CheckProfile{{
		ID: "mutate", Name: "mutate", Command: mutateCommand(), Required: true,
	}})
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"src/ok.go"}, Intent: IntentFormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	checked, err := svc.RunCandidateChecks(context.Background(), "app1", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !checked.MutationDetected || checked.CanFormal {
		t.Fatalf("mutation should stale candidate: %+v", checked)
	}
}

func TestSaveProgressHasNoTagPushOrVersionSideEffects(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "progress\n")
	writeTestFile(t, filepath.Join(repo, "scratch.txt"), "unknown\n")
	before := readFile(t, filepath.Join(repo, "package.json"))
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight: %v %+v", err, pf.BlockingIssues)
	}
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		Intent: IntentSaveProgress, TargetVersion: "9.9.9", SelectedPaths: []string{"tracked.txt"},
		StatusFingerprint: pf.StatusFingerprint, VersionMode: "auto", BuildMode: "github", CommitMessage: "wip",
		SelectedTargets: []store.ReleaseTargetSelection{},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" {
		t.Fatalf("progress failed: %+v", completed)
	}
	if completed.CreateTag || completed.PushRemote || completed.TagName != "" {
		t.Fatalf("progress must not tag/push: %+v", completed)
	}
	after := readFile(t, filepath.Join(repo, "package.json"))
	if after != before {
		t.Fatalf("version file changed during save-progress")
	}
	if strings.TrimSpace(runGit(t, repo, "tag")) != "" && strings.Contains(runGit(t, repo, "tag"), "v9.9.9") {
		t.Fatalf("tag created")
	}
	remoteTag := runGit(t, repo, "ls-remote", "origin", "refs/tags/v9.9.9")
	if strings.TrimSpace(remoteTag) != "" {
		t.Fatalf("remote tag pushed: %s", remoteTag)
	}
}

func TestFormalReleaseCommitsAcceptedTree(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "formal\n")
	writeTestFile(t, filepath.Join(repo, "src/app.go"), "package src\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight: %v %+v", err, pf.BlockingIssues)
	}
	run, err := svc.Start(context.Background(), "app1", acceptCandidate(t, svc, CreateRequest{
		Intent: IntentFormal, TargetVersion: "1.0.1", SelectedPaths: []string{"tracked.txt", "src/app.go"},
		StatusFingerprint: pf.StatusFingerprint, CommitMessage: "chore(release): v1.0.1",
		ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true, VersionMode: "manual",
	}))
	if err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" {
		t.Fatalf("formal failed: %+v", completed)
	}
	if got := runGit(t, repo, "show", "HEAD:tracked.txt"); strings.TrimSpace(got) != "formal" {
		t.Fatalf("committed tree mismatch: %q", got)
	}
	if got := runGit(t, repo, "show", "HEAD:src/app.go"); !strings.Contains(got, "package src") {
		t.Fatalf("selected source missing")
	}
}

func TestConcurrentPrepareUsesRepositoryLock(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "c\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if !svc.reserve(repo) {
		t.Fatal("reserve")
	}
	defer svc.release(repo)
	_, err = svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{
		StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"tracked.txt"}, Intent: IntentSaveProgress,
	})
	if err == nil {
		t.Fatal("expected lock")
	}
}

func writeManifestRules(t *testing.T, repo string, rules []releaseconfig.FileRule) {
	t.Helper()
	writeReleaseYAML(t, repo, map[string]any{"schemaVersion": 1, "versionGroups": []any{}, "targets": []any{}, "fileRules": rules})
}

func writeCheckProfiles(t *testing.T, repo string, profiles []releaseconfig.CheckProfile) {
	t.Helper()
	writeReleaseYAML(t, repo, map[string]any{"schemaVersion": 1, "versionGroups": []any{}, "targets": []any{}, "checkProfiles": profiles})
}

func writeReleaseYAML(t *testing.T, repo string, document map[string]any) {
	t.Helper()
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, ".launcher", "release.yaml"), string(raw)+"\n")
}

func mustRules(t *testing.T, repo string) []releaseconfig.FileRule {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".launcher", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg releaseconfig.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.FileRules
}

func candidateWork(t *testing.T, svc *Service, id string) string {
	t.Helper()
	cand := svc.lookupCandidate(id)
	if cand == nil {
		t.Fatal("missing candidate")
	}
	return cand.Work
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func runtimeIs(osName string) bool { return runtime.GOOS == osName }

func sleepCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 6 127.0.0.1 >NUL"
	}
	return "sleep 5"
}

func mutateCommand() string {
	if runtime.GOOS == "windows" {
		return "echo mutated>>src\\ok.go"
	}
	return "echo mutated >> src/ok.go"
}
