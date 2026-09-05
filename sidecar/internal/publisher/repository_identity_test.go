package publisher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func TestRepositoryIdentityResolvesAliases(t *testing.T) {
	repo := t.TempDir()
	alias := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(repo, alias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	assertSameRepositoryLock(t, repo, alias)
}

func assertSameRepositoryLock(t *testing.T, repo, alias string) {
	t.Helper()
	if !samePath(repo, alias) {
		t.Fatalf("repository aliases differ: %q and %q", repo, alias)
	}
	svc := &Service{active: map[string]bool{}}
	if !svc.reserve(repo) || svc.reserve(alias) {
		t.Fatal("repository alias bypassed the release lock")
	}
	svc.release(alias)
	if !svc.reserve(repo) {
		t.Fatal("releasing an alias did not release the repository lock")
	}
	if samePath(repo, t.TempDir()) {
		t.Fatal("different repositories must not compare equal")
	}
}

func TestUnchangedVersionDoesNotRewriteFile(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "package.json")
	writeTestFile(t, path, "{\"version\":\"2.0.0\",\"keep\":true}\n")
	stamp := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	files := []releaseconfig.VersionFile{{Path: "package.json", Format: "json", JSONPointer: "/version"}}
	if _, err := updateConfiguredVersionFiles(repo, files, "2.0.0"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("unchanged version triggered a file write: %v, %v", info, err)
	}
}
