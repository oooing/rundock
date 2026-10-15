package publisher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type ignoreCountingRunner struct {
	mu       sync.Mutex
	calls    int
	statuses int
}

func (r *ignoreCountingRunner) count(args []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if len(args) > 2 && args[2] == "status" {
		r.statuses++
	}
}
func (r *ignoreCountingRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	r.count(args)
	return execRunner{}.Run(ctx, dir, name, args...)
}
func (r *ignoreCountingRunner) RunWithInput(ctx context.Context, dir, name string, input []byte, args ...string) (string, error) {
	r.count(args)
	return execRunner{}.RunWithInput(ctx, dir, name, input, args...)
}

func TestIgnoreFilesBatchScale(t *testing.T) {
	for _, count := range []int{1, 664} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			svc, repo, closeDB := newReleaseFixture(t)
			defer closeDB()
			requests := make([]IgnoreFileRequest, 0, count)
			for i := 0; i < count; i++ {
				name := fmt.Sprintf("demos/frames/frame-%04d.png", i)
				writeTestFile(t, filepath.Join(repo, name), "synthetic image fixture\n")
				requests = append(requests, IgnoreFileRequest{Path: name, ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: name})})
			}
			head, index := runGit(t, repo, "rev-parse", "HEAD"), runGit(t, repo, "ls-files", "--stage")
			runner := &ignoreCountingRunner{}
			svc.runner = runner
			started := time.Now()
			result, err := svc.IgnoreFiles(context.Background(), "app1", requests)
			elapsed := time.Since(started)
			if err != nil {
				t.Fatal(err)
			}
			if runner.statuses != 2 {
				t.Fatalf("expected only two project scans, got %d", runner.statuses)
			}
			if runner.calls > 65 {
				t.Fatalf("unexpected per-file Git calls: %d", runner.calls)
			}
			if len(result.IgnoreFiles) != 1 {
				t.Fatalf("destinations: %v", result.IgnoreFiles)
			}
			data, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
			if strings.Count(string(data), "/demos/frames/") != count {
				t.Fatal("missing or duplicate rules")
			}
			for _, req := range requests {
				if !fileExists(filepath.Join(repo, req.Path)) {
					t.Fatal("local file deleted")
				}
			}
			if runGit(t, repo, "rev-parse", "HEAD") != head || runGit(t, repo, "ls-files", "--stage") != index {
				t.Fatal("batch staged or committed")
			}
			t.Logf("files=%d elapsed=%s project_scans=%d git_commands=%d ignore_files=%d", count, elapsed.Round(time.Millisecond), runner.statuses, runner.calls, len(result.IgnoreFiles))
			if _, err = svc.IgnoreFiles(context.Background(), "app1", requests); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
			if string(after) != string(data) {
				t.Fatal("retry duplicated rules")
			}
		})
	}
}

func TestIgnoreFilesValidatesWholeBatchBeforeWriting(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	writeTestFile(t, filepath.Join(repo, "local.txt"), "keep")
	good := IgnoreFileRequest{Path: "local.txt", ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: "local.txt"})}
	for _, bad := range []IgnoreFileRequest{{Path: "../outside"}, {Path: "tracked.txt", ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: "tracked.txt"})}, {Path: "local.txt", ContentFingerprint: "old"}} {
		if _, err := svc.IgnoreFiles(context.Background(), "app1", []IgnoreFileRequest{good, bad}); err == nil {
			t.Fatal("invalid batch accepted")
		}
		if fileExists(filepath.Join(repo, ".gitignore")) {
			t.Fatal("part of invalid batch was written")
		}
	}
}

func TestIgnoreFilesNestedRetryAndConcurrentRuleEdit(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "# keep root\r\n")
	writeTestFile(t, filepath.Join(repo, "nested/.gitignore"), "!*.txt\n")
	requests := []IgnoreFileRequest{}
	for _, name := range []string{"root [1].txt", "nested/记录 #!.txt"} {
		writeTestFile(t, filepath.Join(repo, name), "keep")
		requests = append(requests, IgnoreFileRequest{Path: name, ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: name})})
	}
	// Simulate a previously completed group, then retry the original batch.
	if _, err := svc.IgnoreFile(context.Background(), "app1", requests[0]); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	result, err := svc.IgnoreFiles(context.Background(), "app1", requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IgnoreFiles) != 2 {
		t.Fatal(result.IgnoreFiles)
	}
	after, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if string(after) != string(original) {
		t.Fatal("completed group rewritten")
	}
	for _, req := range requests {
		runGit(t, repo, "check-ignore", "--quiet", "--", req.Path)
	}
	edit, err := readIgnoreEdit(repo, ".gitignore")
	if err != nil {
		t.Fatal(err)
	}
	edit.updated = append(edit.updated, []byte("/another\n")...)
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "# external change\n")
	if err := writeIgnoreEdit(repo, edit); err == nil {
		t.Fatal("overwrote concurrent rules")
	}
	after, _ = os.ReadFile(filepath.Join(repo, ".gitignore"))
	if string(after) != "# external change\n" {
		t.Fatal("external change lost")
	}
}
