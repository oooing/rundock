package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPreflightSnapshotPreservesHashesAndClassification(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "src/a.go"), "package src\n")
	writeTestFile(t, filepath.Join(root, "local.bin"), strings.Repeat("data", 600000))
	writeTestFile(t, filepath.Join(root, "renamed.txt"), "new name\n")
	changes := []FileChange{
		{Path: "src/a.go", Status: " M", Tracked: true},
		{Path: "local.bin", Status: "??"},
		{Path: "gone.txt", Status: " D", Tracked: true},
		{Path: "renamed.txt", OldPath: "old.txt", Status: "R ", Tracked: true},
		{Path: "missing.txt", Status: "??"},
	}
	hashes, err := fileHashesContext(context.Background(), root, changes, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{}
	for _, change := range changes {
		raw, err := os.ReadFile(filepath.Join(root, change.Path))
		if err != nil {
			legacy[change.Path] = "<missing>"
			continue
		}
		sum := sha256.Sum256(raw)
		legacy[change.Path] = hex.EncodeToString(sum[:])
	}
	if !reflect.DeepEqual(hashes, legacy) {
		t.Fatal("snapshot format changed")
	}
	got := classifyChangesWithHashes(root, changes, nil, nil, hashes)
	for index, change := range changes {
		if want := classifyOne(root, change, nil); !reflect.DeepEqual(got[index], want) {
			t.Fatalf("classification/order differs for %s: %+v / %+v", change.Path, got[index], want)
		}
	}
	if got[2].ContentFingerprint == "<missing>" || got[4].ContentFingerprint != "unreadable:missing.txt" {
		t.Fatal("deletion/missing classification semantics lost")
	}
	if got := fileHashes(root, changes, map[string]bool{"local.bin": true}); len(got) != len(changes)-1 {
		t.Fatal("excluded file hashed")
	}
}

func TestPreflightSnapshotReadsFreshBytesDespiteUnchangedTimestamp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.txt")
	writeTestFile(t, path, "first")
	info, _ := os.Stat(path)
	changes := []FileChange{{Path: "source.txt", Status: " M", Tracked: true}}
	first := statusFingerprint(root, "head", " M source.txt\x00", changes)
	firstHash := fileHashes(root, changes, nil)["source.txt"]
	writeTestFile(t, path, "other")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second := statusFingerprint(root, "head", " M source.txt\x00", changes)
	secondHash := fileHashes(root, changes, nil)["source.txt"]
	if first == second || firstHash == secondHash {
		t.Fatal("same-size edit escaped content validation")
	}
}

func TestPreflightSnapshotCancellationAndUnsafePaths(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fileHashesContext(ctx, t.TempDir(), []FileChange{{Path: "a.txt"}}, nil); err != context.Canceled {
		t.Fatalf("cancelled snapshot reported success: %v", err)
	}
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.txt"), "inside")
	for _, name := range []string{"../outside.txt", "directory"} {
		if name == "directory" {
			_ = os.Mkdir(filepath.Join(root, name), 0755)
		}
		item := classifyOneWithHash(root, FileChange{Path: name, Status: "??"}, nil, strings.Repeat("a", 64))
		if item.ContentFingerprint == strings.Repeat("a", 64) {
			t.Fatal("cached bytes bypassed path validation")
		}
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(filepath.Join(root, "a.txt"), link); err == nil {
		item := classifyOneWithHash(root, FileChange{Path: "link.txt", Status: "??"}, nil, strings.Repeat("a", 64))
		if item.ContentFingerprint == strings.Repeat("a", 64) {
			t.Fatal("link bypassed path validation")
		}
	}
}

func TestRepositoryOperationBatchPreservesAllMarkers(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	if svc.repositoryOperationActive(context.Background(), repo) {
		t.Fatal("clean repository blocked")
	}
	for _, marker := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD", "rebase-merge", "rebase-apply"} {
		markerPath := strings.TrimSpace(runGit(t, repo, "rev-parse", "--git-path", marker))
		if !filepath.IsAbs(markerPath) {
			markerPath = filepath.Join(repo, markerPath)
		}
		writeTestFile(t, markerPath, "active")
		if !svc.repositoryOperationActive(context.Background(), repo) {
			t.Fatalf("missed %s", marker)
		}
		if err := os.Remove(markerPath); err != nil {
			t.Fatal(err)
		}
	}
}
