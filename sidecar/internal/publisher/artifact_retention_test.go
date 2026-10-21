package publisher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/store"
)

func TestSavedArtifactMaintenanceSkipsBusyRepositoryAndRetainsFailedRun(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.DB().Exec(`INSERT INTO apps(id,name,entry_script,cwd) VALUES('app','fixture','','')`); err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	defer svc.Close()
	repo := t.TempDir()
	now := time.Now().UTC()
	source := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(source, []byte("fixture bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("run-%d", i)
		status := "succeeded"
		if i == 5 {
			status = "failed"
		}
		if err := st.CreateReleaseRun(&store.ReleaseRun{ID: id, AppID: "app", RepoRoot: repo, CommitSHA: "commit", Status: status}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`UPDATE release_runs SET finished_at=? WHERE id=?`, now.Add(-time.Duration(40+i)*24*time.Hour).Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
		if err := svc.delivery.Seal(context.Background(), delivery.Batch{RunID: id, AppID: "app", GroupID: "desktop", Repository: "fixture/repo", Commit: "commit"}, []delivery.Source{{TargetID: "desktop", Path: source}}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpdateReleaseDelivery(id, "desktop", "published", 123, "https://github.com/fixture/repo/releases/tag/"+id, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if !svc.reserve(repo) {
		t.Fatal("reserve")
	}
	if err := svc.pruneSavedArtifacts(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.ReleaseDeliveries("run-3")
	if rows[0].LocalArtifactsState != "retained" {
		t.Fatal("busy repository was cleaned")
	}
	svc.release(repo)
	if err := os.WriteFile(filepath.Join(st.ReleaseDataDir(), "run-3", "desktop", "manifest.json"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.pruneSavedArtifacts(context.Background(), now); err == nil {
		t.Fatal("damaged batch must report incomplete cleanup")
	}
	rows, _ = st.ReleaseDeliveries("run-4")
	if rows[0].LocalArtifactsState != "cleaned" {
		t.Fatal("idle success was not cleaned")
	}
	rows, _ = st.ReleaseDeliveries("run-5")
	if rows[0].LocalArtifactsState != "retained" {
		t.Fatal("failed run was cleaned")
	}
	batch, err := svc.delivery.Load("run-5")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.delivery.Verify(context.Background(), batch[0]); err != nil {
		t.Fatal("failed run cannot resume", err)
	}
	logs, err := st.ReleaseLogs("run-4", 0, 10)
	if err != nil || len(logs) != 1 {
		t.Fatal("cleanup not logged", err)
	}
}
