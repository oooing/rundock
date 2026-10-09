package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnoreFileExactPersistentAndUnstaged(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	ctx := context.Background()
	name := "notes/记录 [a] #!.txt"
	writeTestFile(t, filepath.Join(repo, name), "keep this file")
	writeTestFile(t, filepath.Join(repo, "notes/记录 a #!.txt"), "keep sibling visible")
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "# existing\r\n/cache/")
	head := runGit(t, repo, "rev-parse", "HEAD")
	index := runGit(t, repo, "ls-files", "--stage")
	req := IgnoreFileRequest{Path: name, ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: name})}
	result, err := svc.IgnoreFile(ctx, "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IgnoreFile != ".gitignore" {
		t.Fatalf("wrong ignore file: %s", result.IgnoreFile)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# existing\r\n/cache/\r\n/notes/记录\\ \\[a\\]\\ \\#\\!.txt\r\n"
	if string(data) != want {
		t.Fatalf("rules changed: %q", data)
	}
	runGit(t, repo, "check-ignore", "--quiet", "--", name)
	seenSibling, seenIgnore := false, false
	for _, file := range result.Preflight.Classifications {
		if file.Path == name {
			t.Fatal("ignored file still listed")
		}
		if file.Path == "notes/记录 a #!.txt" {
			seenSibling = true
		}
		if file.Path == ".gitignore" {
			seenIgnore = true
			if file.Category != "recommend" {
				t.Fatalf("ignore rules not recommended: %+v", file)
			}
		}
	}
	if !seenSibling || !seenIgnore {
		t.Fatalf("unexpected list: %+v", result.Preflight.Classifications)
	}
	if after, _ := os.ReadFile(filepath.Join(repo, name)); string(after) != "keep this file" {
		t.Fatal("local file modified")
	}
	if runGit(t, repo, "rev-parse", "HEAD") != head || runGit(t, repo, "ls-files", "--stage") != index {
		t.Fatal("operation staged or committed files")
	}
	if _, err := svc.IgnoreFile(ctx, "app1", req); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(repo, ".gitignore")); string(after) != want {
		t.Fatal("retry duplicated rule")
	}
	fresh, err := svc.PreflightLocal(ctx, "app1")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range fresh.Changes {
		if file.Path == name {
			t.Fatal("next preflight asks again")
		}
	}
}

func TestIgnoreFileUsesNestedRules(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	writeTestFile(t, filepath.Join(repo, ".gitignore"), "*.txt\n")
	writeTestFile(t, filepath.Join(repo, "notes/.gitignore"), "!*.txt\n")
	writeTestFile(t, filepath.Join(repo, "notes/local.txt"), "keep")
	result, err := svc.IgnoreFile(context.Background(), "app1", IgnoreFileRequest{Path: "notes/local.txt", ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: "notes/local.txt"})})
	if err != nil {
		t.Fatal(err)
	}
	if result.IgnoreFile != "notes/.gitignore" {
		t.Fatal(result.IgnoreFile)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "notes/.gitignore"))
	if string(data) != "!*.txt\n/local.txt\n" {
		t.Fatalf("wrong nested rule: %q", data)
	}
	runGit(t, repo, "check-ignore", "--quiet", "--", "notes/local.txt")
}

func TestIgnoreFileCreatesRootRules(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	writeTestFile(t, filepath.Join(repo, "local.txt"), "keep")
	result, err := svc.IgnoreFile(context.Background(), "app1", IgnoreFileRequest{Path: "local.txt", ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: "local.txt"})})
	if err != nil {
		t.Fatal(err)
	}
	if result.IgnoreFile != ".gitignore" {
		t.Fatal(result.IgnoreFile)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil || string(data) != "/local.txt\n" {
		t.Fatalf("new rules: %q %v", data, err)
	}
	runGit(t, repo, "check-ignore", "--quiet", "--", "local.txt")
}

func TestIgnoreFileRejectsUnsafeRequests(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	ctx := context.Background()
	writeTestFile(t, filepath.Join(repo, "local.txt"), "keep")
	for _, name := range []string{"tracked.txt", "../outside.txt", ".git/config", ".gitignore", "notes/.gitignore", "local.txt:stream", "missing.txt", "./local.txt", "local.txt "} {
		_, err := svc.IgnoreFile(ctx, "app1", IgnoreFileRequest{Path: name, ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: name})})
		if err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	_, err := svc.IgnoreFile(ctx, "app1", IgnoreFileRequest{Path: "local.txt", ContentFingerprint: "outdated"})
	if err == nil || !strings.Contains(err.Error(), "变化") {
		t.Fatalf("stale content accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("invalid request wrote ignore rules")
	}
	req := IgnoreFileRequest{Path: "local.txt", ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: "local.txt"})}
	if !svc.reserve(repo) {
		t.Fatal("cannot reserve")
	}
	_, err = svc.IgnoreFile(ctx, "app1", req)
	svc.release(repo)
	if err == nil {
		t.Fatal("changed rules during active release")
	}
	runGit(t, repo, "add", "local.txt")
	if _, err := svc.IgnoreFile(ctx, "app1", req); err == nil {
		t.Fatal("ignored staged file")
	}
	runGit(t, repo, "rm", "--cached", "tracked.txt")
	if _, err := svc.IgnoreFile(ctx, "app1", IgnoreFileRequest{Path: "tracked.txt", ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: "tracked.txt"})}); err == nil {
		t.Fatal("ignored a staged deletion of tracked content")
	}
}

func TestIgnoreFilePreservesUnsupportedIgnoreEncoding(t *testing.T) {
	svc, repo, closeDB := newReleaseFixture(t)
	defer closeDB()
	writeTestFile(t, filepath.Join(repo, "local.txt"), "keep")
	original := string([]byte{0xff, 0xfe, '#', 0})
	writeTestFile(t, filepath.Join(repo, ".gitignore"), original)
	_, err := svc.IgnoreFile(context.Background(), "app1", IgnoreFileRequest{Path: "local.txt", ContentFingerprint: fileContentFingerprint(repo, FileChange{Path: "local.txt"})})
	if err == nil {
		t.Fatal("rewrote unsupported encoding")
	}
	data, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if string(data) != original {
		t.Fatal("corrupted existing rules")
	}
}
