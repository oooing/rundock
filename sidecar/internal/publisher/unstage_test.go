package publisher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUnstagePreservesWorkingFilesHeadAndIndexBackup(t *testing.T) {
	s, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "removed.txt"), "remove me")
	writeTestFile(t, filepath.Join(repo, "old name.txt"), "rename me")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "fixture")
	head := runGit(t, repo, "rev-parse", "HEAD")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "staged version\n")
	writeTestFile(t, filepath.Join(repo, "新增 file.txt"), "new file\n")
	runGit(t, repo, "rm", "removed.txt")
	runGit(t, repo, "mv", "old name.txt", "renamed.txt")
	runGit(t, repo, "add", ".")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "later unstaged edits\n")
	index, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	pf, err := s.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Unstage(context.Background(), "app1", pf.StatusFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if hasIssue(result, "staged_changes") || result.RemoteChecked {
		t.Fatalf("bad result: %+v", result)
	}
	if got, err := s.git(context.Background(), repo, "diff", "--cached", "--name-only"); err != nil || got != "" {
		t.Fatalf("still staged: %s", got)
	}
	if runGit(t, repo, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved")
	}
	for file, want := range map[string]string{"tracked.txt": "later unstaged edits\n", "新增 file.txt": "new file\n", "renamed.txt": "rename me"} {
		got, err := os.ReadFile(filepath.Join(repo, file))
		if err != nil || string(got) != want {
			t.Fatalf("working file changed: %s %q %v", file, got, err)
		}
	}
	for _, file := range []string{"removed.txt", "old name.txt"} {
		if _, err := os.Stat(filepath.Join(repo, file)); !os.IsNotExist(err) {
			t.Fatalf("restored working file: %s", file)
		}
	}
	backups, _ := filepath.Glob(filepath.Join(repo, ".git", "rundock-index-backups", "index-*"))
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	backup, _ := os.ReadFile(backups[0])
	if !bytes.Equal(index, backup) {
		t.Fatal("original staged content backup differs")
	}
	if again, err := s.Unstage(context.Background(), "app1", result.StatusFingerprint); err != nil || hasIssue(again, "staged_changes") {
		t.Fatalf("idempotent retry: %v", err)
	}
}

func TestUnstageRejectsChangedStatusAndActiveRelease(t *testing.T) {
	s, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "staged")
	runGit(t, repo, "add", ".")
	pf, err := s.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "edited again")
	_, err = s.Unstage(context.Background(), "app1", pf.StatusFingerprint)
	if pe, ok := err.(*Error); !ok || pe.Code != "status_changed" {
		t.Fatalf("expected status_changed: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if !bytes.Equal(before, after) {
		t.Fatal("stale request changed index")
	}
	s.reserve(repo)
	defer s.release(repo)
	_, err = s.Unstage(context.Background(), "app1", pf.StatusFingerprint)
	if pe, ok := err.(*Error); !ok || pe.Code != "release_in_progress" {
		t.Fatalf("expected busy: %v", err)
	}
}

func TestUnstageUnbornBranchPreservesFiles(t *testing.T) {
	s, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runGit(t, repo, "checkout", "--orphan", "first-release")
	pf, err := s.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Unstage(context.Background(), "app1", pf.StatusFingerprint)
	if err != nil || hasIssue(result, "staged_changes") {
		t.Fatalf("unborn: %v %+v", err, result)
	}
	if got, err := s.git(context.Background(), repo, "ls-files"); err != nil || got != "" {
		t.Fatalf("index not empty: %s %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "tracked.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestUnstageDoesNotClearMergeConflicts(t *testing.T) {
	s, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	// A normal two-sided edit creates real unmerged index entries.
	runGit(t, repo, "checkout", "-b", "other")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "other side\n")
	runGit(t, repo, "commit", "-am", "other edit")
	runGit(t, repo, "checkout", "-")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "this side\n")
	runGit(t, repo, "commit", "-am", "this edit")
	if _, err := s.git(context.Background(), repo, "merge", "other"); err == nil {
		t.Fatal("expected conflict")
	}
	pf, err := s.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	_, err = s.Unstage(context.Background(), "app1", pf.StatusFingerprint)
	if pe, ok := err.(*Error); !ok || (pe.Code != "repository_operation" && pe.Code != "merge_conflict") {
		t.Fatalf("conflict not blocked: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if !bytes.Equal(before, after) {
		t.Fatal("unmerged index was changed")
	}
}
