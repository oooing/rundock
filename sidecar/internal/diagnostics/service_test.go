package diagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/launcher-sidecar/internal/store"
)

func openDiagnosticStore(t *testing.T, cwd string) (*store.Store, *store.App) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "launcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	app := &store.App{ID: "app-1", Name: "demo", Cwd: cwd, EntryScript: filepath.Join(cwd, "start.bat"),
		AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}}
	if err := st.CreateApp(app); err != nil {
		t.Fatal(err)
	}
	return st, app
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = string(exit.Stderr)
		}
		t.Fatalf("git %v: %v\n%s", args, err, stderr)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T, root string) {
	t.Helper()
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.name", "Diagnostics Test")
	runGit(t, root, "config", "user.email", "diagnostics@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "initial")
}

func TestServiceWritesAIReadableRedactedArchiveWithoutDirtyingGit(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	cwd := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	st, _ := openDiagnosticStore(t, cwd)
	svc := New(st)
	t.Cleanup(func() { _ = svc.Close() })

	if !svc.Record(Event{AppID: "app-1", RunID: "run-1", Kind: "error", Severity: "error",
		Source: "launcher", Operation: "app.start", ErrorCode: "spawn_failed",
		Message: "Authorization: Bearer top-secret password=hunter2 https://user:pass@example.test/a?token=abc",
		Context: map[string]any{"apiKey": "secret-key", "safe": "kept"}}) {
		t.Fatal("event was not queued")
	}
	if !svc.RecordProcessLine("app-1", "run-1", "stdout", "info", "frontend built in 8.92s") {
		t.Fatal("duration line was not queued")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(repo, ".launcher", "diagnostics")
	latestRaw, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var latest latestIndex
	if err := json.Unmarshal(latestRaw, &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Notice != UntrustedDataNotice || latest.EventFile == "" {
		t.Fatalf("latest index = %+v", latest)
	}
	file, err := os.Open(filepath.Join(dir, latest.EventFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid JSONL: %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	encoded, _ := json.Marshal(events[0])
	for _, secret := range []string{"top-secret", "hunter2", "secret-key", "user:pass", "token=abc"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("secret %q leaked in %s", secret, encoded)
		}
	}
	if events[0].Context["safe"] != "kept" || events[1].Kind != "performance" || events[1].DurationMS != 8920 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if got := runGit(t, repo, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
		t.Fatalf("diagnostics dirtied repository: %q", got)
	}
	exclude := runGit(t, repo, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	excludeRaw, err := os.ReadFile(exclude)
	if err != nil || !strings.Contains(string(excludeRaw), "/.launcher/diagnostics/") {
		t.Fatalf("local exclude missing: %v %q", err, excludeRaw)
	}
}

func TestRecordProcessLineIgnoresOrdinaryStderr(t *testing.T) {
	repo := t.TempDir()
	st, _ := openDiagnosticStore(t, repo)
	svc := New(st)
	defer svc.Close()
	if svc.RecordProcessLine("app-1", "run-1", "stderr", "error", "Downloading compiler crate 1 of 20") {
		t.Fatal("ordinary stderr progress must not be persisted as an error")
	}
	if svc.RecordProcessLine("app-1", "run-1", "event", "debug", "urlDiscoverTimeout=30s") {
		t.Fatal("a timeout setting name must not be treated as a timeout warning")
	}
	if !svc.RecordProcessLine("app-1", "run-1", "stderr", "error", "fatal: compiler failed") {
		t.Fatal("explicit failure should be persisted")
	}
}

func TestServiceRefusesTrackedDiagnostics(t *testing.T) {
	repo := t.TempDir()
	initRepo(t, repo)
	dir := filepath.Join(repo, ".launcher", "diagnostics")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.jsonl"), []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".launcher/diagnostics/tracked.jsonl")
	runGit(t, repo, "commit", "-m", "tracked diagnostics")
	st, _ := openDiagnosticStore(t, repo)
	svc := New(st)
	defer svc.Close()
	svc.Record(Event{AppID: "app-1", Kind: "error", Severity: "error", Source: "launcher", Operation: "test", Message: "must not write"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := svc.Flush(ctx)
	if err == nil || !strings.Contains(err.Error(), "tracked files") {
		t.Fatalf("expected tracked diagnostics rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); !os.IsNotExist(err) {
		t.Fatalf("latest.json should not exist, err=%v", err)
	}
}

func TestServiceSupportsGitWorktreeDotGitFile(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	worktree := filepath.Join(parent, "worktree")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	initRepo(t, repo)
	runGit(t, repo, "worktree", "add", "-b", "diagnostics-worktree", worktree)
	cwd := filepath.Join(worktree, "nested")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	st, _ := openDiagnosticStore(t, cwd)
	svc := New(st)
	defer svc.Close()
	svc.Record(Event{AppID: "app-1", Kind: "lifecycle", Severity: "info", Source: "launcher", Operation: "worktree.test", Message: "ok"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".launcher", "diagnostics", "latest.json")); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, worktree, "status", "--porcelain=v1", "--untracked-files=all"); got != "" {
		t.Fatalf("worktree diagnostics dirtied repository: %q", got)
	}
}

func TestPrepareDirectoryRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".launcher"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := os.Symlink(external, filepath.Join(root, ".launcher", "diagnostics"))
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink requires Windows developer mode or elevation")
		}
		t.Fatal(err)
	}
	if _, err := prepareDirectory(root, false); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestRotationAndCleanupNeverDeleteUnknownFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	loc := &location{diagnostics: dir}
	base := filepath.Join(dir, "events-2026-09-05.jsonl")
	file, err := os.OpenFile(base, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxFileBytes); err != nil {
		t.Fatal(err)
	}
	file.Close()
	svc := &Service{}
	next, err := svc.eventPath(loc, now, 1)
	if err != nil || filepath.Base(next) != "events-2026-09-05.001.jsonl" {
		t.Fatalf("next path = %q err=%v", next, err)
	}
	old := filepath.Join(dir, "events-2026-07-01.jsonl")
	unknown := filepath.Join(dir, "keep-me.txt")
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := now.AddDate(0, 0, -retentionDays-1)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("unknown"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(dir, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old event file was not removed: %v", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatalf("unknown file was removed: %v", err)
	}
}

func TestRedactCommonSecretsAndTruncateUTF8(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnopqrstuvwxyz"
	input := "ghp_abcdefghijklmnopqrstuvwxyz1234567890 " + jwt + " api_key='abc' {\"token\":\"json-secret\"} https://u:p@example.test/?secret=q -----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----"
	got := Redact(input)
	for _, secret := range []string{"ghp_", "eyJhbGci", "abc", "json-secret", "u:p", "secret=q", "BEGIN PRIVATE KEY"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked: %s", secret, got)
		}
	}
	long := strings.Repeat("界", maxMessage)
	truncated := truncateText(long, maxMessage)
	if len(truncated) > maxMessage || !strings.HasSuffix(truncated, "[TRUNCATED]") || !utf8.ValidString(truncated) {
		t.Fatalf("bad UTF-8 truncation: bytes=%d", len(truncated))
	}
}
