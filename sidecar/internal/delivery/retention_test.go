package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

var retentionNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func retentionEngine(t *testing.T) *Engine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err = st.DB().Exec(`INSERT INTO apps(id,name,entry_script,cwd) VALUES('app','fixture','','')`); err != nil {
		t.Fatal(err)
	}
	return New(st)
}

func retentionFixture(t *testing.T, e *Engine, id string, age time.Duration, status string, change func(*Batch)) Batch {
	t.Helper()
	b := Batch{RunID: id, AppID: "app", GroupID: "desktop", Repository: "fixture/repo", Account: "fixture", Commit: "saved-commit", Tag: id, Version: id}
	if change != nil {
		change(&b)
	}
	if _, err := e.Store.DB().Exec(`INSERT OR IGNORE INTO apps(id,name,entry_script,cwd) VALUES(?,?, '', '')`, b.AppID, b.AppID); err != nil {
		t.Fatal(err)
	}
	run := &store.ReleaseRun{ID: id, AppID: b.AppID, RepoRoot: filepath.Join(e.Store.ReleaseDataDir(), "fixture-repo"), CommitSHA: b.Commit, Status: status}
	if err := e.Store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Store.DB().Exec(`UPDATE release_runs SET finished_at=? WHERE id=?`, retentionNow.Add(-age).Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	var sources []Source
	for i := 0; i < 2; i++ {
		path := filepath.Join(sourceDir, fmt.Sprintf("package-%d.zip", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("fixture-%s-%d", id, i)), 0600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, Source{TargetID: b.GroupID, Path: path})
	}
	if err := e.Seal(context.Background(), b, sources); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.UpdateReleaseDelivery(id, b.GroupID, "published", 123, "https://github.com/fixture/repo/releases/tag/"+id, "", ""); err != nil {
		t.Fatal(err)
	}
	batches, err := e.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	return batches[0]
}

func retentionRecent(t *testing.T, e *Engine) {
	for i := 0; i < 3; i++ {
		retentionFixture(t, e, fmt.Sprintf("recent-%d", i), time.Duration(40+i)*24*time.Hour, "succeeded", nil)
	}
}

func TestRetentionProtectsRetryLocalRecentAndUnfinishedRuns(t *testing.T) {
	e := retentionEngine(t)
	retentionRecent(t, e)
	retentionFixture(t, e, "old-success", 60*24*time.Hour, "succeeded", nil)
	partial := retentionFixture(t, e, "partially-published", 90*24*time.Hour, "succeeded", nil)
	partial.GroupID = "pending-group"
	raw, _ := json.Marshal(partial)
	if err := e.Store.SealReleaseDelivery(partial.RunID, partial.GroupID, raw); err != nil {
		t.Fatal(err)
	}
	retentionFixture(t, e, "sync-not-observed", 90*24*time.Hour, "succeeded", func(b *Batch) { b.SyncURL = "https://fixture.invalid/version" })
	for _, status := range []string{"failed", "cancelled", "running", "queued"} {
		retentionFixture(t, e, "protect-"+status, 90*24*time.Hour, status, nil)
	}
	retentionFixture(t, e, "local-only", 90*24*time.Hour, "succeeded", nil)
	if _, err := e.Store.DB().Exec(`DELETE FROM release_deliveries WHERE release_run_id='local-only'`); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"sealed", "failed", "uploading", "publishing"} {
		id := "delivery-" + state
		retentionFixture(t, e, id, 90*24*time.Hour, "succeeded", nil)
		if err := e.Store.UpdateReleaseDelivery(id, "desktop", state, 0, "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"pending", "unverified", "deploying", "deployment_failed", "deployment_rejected"} {
		id := "sync-" + state
		retentionFixture(t, e, id, 90*24*time.Hour, "succeeded", nil)
		if err := e.Store.UpdateDeliverySync(id, "desktop", state, ""); err != nil {
			t.Fatal(err)
		}
	}
	retentionFixture(t, e, "android-last-copy", 90*24*time.Hour, "succeeded", func(b *Batch) { b.GroupID = "android" })
	retentionFixture(t, e, "other-app-last-copy", 90*24*time.Hour, "succeeded", func(b *Batch) { b.AppID = "other-app" })
	retentionFixture(t, e, "beta-last-copy", 90*24*time.Hour, "succeeded", func(b *Batch) { b.Prerelease = true })
	retentionFixture(t, e, "other-repo-last-copy", 90*24*time.Hour, "succeeded", func(b *Batch) { b.Repository = "fixture/other" })
	got, err := e.RetentionCandidates(context.Background(), retentionNow)
	if err != nil || len(got) != 1 || got[0].RunID != "old-success" {
		t.Fatalf("candidates=%+v err=%v", got, err)
	}
}

func TestRetentionAgeAndPlatformCoverage(t *testing.T) {
	e := retentionEngine(t)
	for i := 0; i < 5; i++ {
		retentionFixture(t, e, fmt.Sprintf("fresh-%d", i), time.Duration(i+1)*24*time.Hour, "succeeded", nil)
	}
	got, err := e.RetentionCandidates(context.Background(), retentionNow)
	if err != nil || len(got) != 0 {
		t.Fatalf("recent files eligible: %+v %v", got, err)
	}
	a := Batch{AppID: "a", GroupID: "combined", Repository: "a/b", Files: []File{{TargetID: "pc"}}}
	b := a
	b.Files = []File{{TargetID: "pc"}, {TargetID: "android"}}
	if retentionStream(a) == retentionStream(b) {
		t.Fatal("a partial-platform release could evict the last full copy")
	}
}

func TestRetentionRemovesOnlySealedBlobsAndKeepsHistory(t *testing.T) {
	e := retentionEngine(t)
	retentionRecent(t, e)
	b := retentionFixture(t, e, "old", 60*24*time.Hour, "succeeded", nil)
	dir, _ := e.batchDir(b.RunID, b.GroupID)
	unknown := filepath.Join(dir, "user-notes.txt")
	if err := os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(e.Store.ReleaseDataDir(), "local-builds", "keep.zip")
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("sole local copy"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.Store.ReleaseDeliveries(b.RunID)
	before := string(rows[0].Manifest)
	candidates, _ := e.RetentionCandidates(context.Background(), retentionNow)
	cleaned, err := e.PrunePublishedArtifacts(context.Background(), candidates[0], retentionNow)
	if err != nil || !cleaned {
		t.Fatalf("cleaned=%v err=%v", cleaned, err)
	}
	for _, f := range b.Files {
		if _, err := os.Stat(filepath.Join(dir, f.SHA256)); !os.IsNotExist(err) {
			t.Fatal("blob retained", err)
		}
	}
	for _, path := range []string{filepath.Join(dir, "manifest.json"), unknown, local} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("protected file removed", err)
		}
	}
	rows, _ = e.Store.ReleaseDeliveries(b.RunID)
	if rows[0].LocalArtifactsState != "cleaned" || rows[0].LocalArtifactsCleanedAt == "" || string(rows[0].Manifest) != before || rows[0].State != "published" || rows[0].URL == "" {
		t.Fatal("history or cleanup state lost", rows[0])
	}
	run, _ := e.Store.GetReleaseRun(b.RunID)
	if run == nil || run.Status != "succeeded" {
		t.Fatal("release record lost")
	}
	if cleaned, err = e.PrunePublishedArtifacts(context.Background(), candidates[0], retentionNow); err != nil || cleaned {
		t.Fatal("cleanup is not idempotent", err)
	}
}

func TestRetentionResumesPartialCleanupAndSkipsBusyRun(t *testing.T) {
	e := retentionEngine(t)
	retentionRecent(t, e)
	b := retentionFixture(t, e, "old", 60*24*time.Hour, "succeeded", nil)
	candidates, _ := e.RetentionCandidates(context.Background(), retentionNow)
	unlock, err := Lock(e.Store.ReleaseDataDir(), "retention:"+b.RunID)
	if err != nil {
		t.Fatal(err)
	}
	cleaned, err := e.PrunePublishedArtifacts(context.Background(), candidates[0], retentionNow)
	unlock()
	if err != nil || cleaned {
		t.Fatal("cleaned a locked run")
	}
	dir, _ := e.batchDir(b.RunID, b.GroupID)
	if err := e.Store.SetDeliveryStorageState(b.RunID, b.GroupID, "cleaning", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, b.Files[0].SHA256)); err != nil {
		t.Fatal(err)
	}
	cleaned, err = e.PrunePublishedArtifacts(context.Background(), candidates[0], retentionNow)
	if err != nil || !cleaned {
		t.Fatal("did not resume interrupted cleanup", err)
	}
}

func TestRetentionRechecksStateAndRejectsTamperedPaths(t *testing.T) {
	for _, mode := range []string{"failed", "sync-pending", "manifest", "directory", "symlink", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			e := retentionEngine(t)
			retentionRecent(t, e)
			b := retentionFixture(t, e, "old", 60*24*time.Hour, "succeeded", nil)
			dir, _ := e.batchDir(b.RunID, b.GroupID)
			candidates, _ := e.RetentionCandidates(context.Background(), retentionNow)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			path := filepath.Join(dir, b.Files[0].SHA256)
			switch mode {
			case "failed":
				if err := e.Store.UpdateReleaseRun(b.RunID, "failed", "delivery_preflight", "", "github_auth_required", "", true); err != nil {
					t.Fatal(err)
				}
			case "sync-pending":
				if err := e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "pending", ""); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "keep.zip")
				if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Skip("symlinks unavailable", err)
				}
				defer func() {
					if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
						t.Fatal("outside file touched")
					}
				}()
			case "cancel":
				cancel()
			}
			cleaned, err := e.PrunePublishedArtifacts(ctx, candidates[0], retentionNow)
			if cleaned {
				t.Fatal("unsafe cleanup succeeded")
			}
			if mode != "failed" && mode != "sync-pending" && err == nil {
				t.Fatal("expected safety error")
			}
			if _, err := os.Stat(filepath.Join(dir, b.Files[1].SHA256)); err != nil {
				t.Fatal("deleted before validating entire batch", err)
			}
		})
	}
}

func TestRetentionRejectsOutOfRootAndLinkedDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := plainRetentionDir(root, outside); err == nil {
		t.Fatal("outside root accepted")
	}
	if err := plainRetentionDir(root, root); err == nil {
		t.Fatal("root deletion accepted")
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if err := plainRetentionDir(root, link); err == nil {
		t.Fatal("linked directory accepted")
	}
}

func TestRetentionStorageMigrationSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 0; i < 2; i++ {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
	}
	// API state must distinguish a cleaned package from a retained build result.
	raw, _ := json.Marshal(store.ReleaseDelivery{LocalArtifactsState: "cleaned", LocalArtifactsCleanedAt: "now"})
	if !strings.Contains(string(raw), `"localArtifactsState":"cleaned"`) {
		t.Fatal(string(raw))
	}
}
