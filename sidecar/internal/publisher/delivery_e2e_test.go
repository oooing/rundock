package publisher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

type deliveryEvidence struct {
	Scenario       string                   `json:"scenario"`
	Passed         bool                     `json:"passed"`
	Status         string                   `json:"status"`
	ErrorCode      string                   `json:"errorCode"`
	Builds         int                      `json:"builds"`
	UploadAttempts int                      `json:"uploadAttempts"`
	UploadedFiles  int                      `json:"uploadedFiles"`
	DraftsCreated  int                      `json:"draftsCreated"`
	Publications   int                      `json:"publications"`
	Deliveries     []*store.ReleaseDelivery `json:"deliveries"`
	Artifacts      []*store.ReleaseArtifact `json:"artifacts"`
}

func TestReleaseDeliveryE2E(t *testing.T) {
	if os.Getenv("RUNDOCK_DELIVERY_E2E") != "1" {
		t.Skip("opt-in end-to-end acceptance; see development plan")
	}
	report := []deliveryEvidence{}
	defer func() {
		dir := os.Getenv("RUNDOCK_DELIVERY_EVIDENCE")
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Error(err)
			return
		}
		raw, err := json.MarshalIndent(map[string]any{"schemaVersion": 1, "boundary": "real Git, SQLite, signed synthetic packages and command processes; simulated GitHub", "passed": !t.Failed(), "scenarios": report}, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err = os.WriteFile(filepath.Join(dir, "delivery-e2e.json"), raw, 0600); err != nil {
			t.Error(err)
		}
	}()
	scenarios := []string{"multi-target-complete", "network-resume", "restart-head-advanced", "duplicate-submit", "lost-draft-response", "lost-upload-response", "lost-publish-response", "digest-fallback", "missing-signature", "missing-required-file", "unknown-draft", "same-name-different-content", "tag-conflict", "tampered-sealed-file", "account-changed", "cancel-build", "concurrent-services", "timeout-build", "workflow-blocked", "verify-source-change"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			mode := "normal"
			switch scenario {
			case "missing-signature":
				mode = "missing"
			case "missing-required-file":
				mode = "rule-missing"
			case "cancel-build", "concurrent-services":
				mode = "wait"
			case "timeout-build":
				mode = "timeout"
			case "workflow-blocked":
				mode = "workflow"
			case "verify-source-change":
				mode = "verify-source-change"
			}
			f := newDeliveryFixture(t, mode, scenario == "multi-target-complete")
			switch scenario {
			case "network-resume", "restart-head-advanced", "same-name-different-content", "tampered-sealed-file", "account-changed":
				f.gh.failUploadAt = 2
			case "lost-draft-response":
				f.gh.loseCreate = true
			case "lost-upload-response":
				f.gh.loseUpload = true
			case "lost-publish-response":
				f.gh.losePublish = true
			case "digest-fallback":
				f.gh.omitDigest = true
			case "unknown-draft":
				f.gh.release = &remoteRelease{ID: 1, Tag: "v1.0.1", Draft: true, Body: "another operation"}
			case "tag-conflict":
				f.gh.tagConflict = true
			}
			run := f.start(t)
			defer func() {
				final, _ := f.svc.store.GetReleaseRun(run.ID)
				rows, _ := f.svc.store.ReleaseDeliveries(run.ID)
				artifacts, _ := f.svc.store.ReleaseArtifacts(run.ID)
				item := deliveryEvidence{Scenario: scenario, Passed: !t.Failed(), Builds: f.builds(), UploadAttempts: f.gh.attempts, UploadedFiles: f.gh.uploaded, DraftsCreated: f.gh.creates, Publications: f.gh.publishes, Deliveries: rows, Artifacts: artifacts}
				if final != nil {
					item.Status = final.Status
					item.ErrorCode = final.ErrorCode
				}
				report = append(report, item)
			}()
			if scenario == "cancel-build" || scenario == "concurrent-services" {
				deadline := time.Now().Add(10 * time.Second)
				for f.builds() == 0 && time.Now().Before(deadline) {
					time.Sleep(20 * time.Millisecond)
				}
				if f.builds() == 0 {
					t.Fatal("build process did not start")
				}
				if scenario == "concurrent-services" {
					other := New(f.svc.store)
					if other.reserve(f.repo) {
						other.release(f.repo)
						t.Fatal("another service acquired the active repository")
					}
				}
				if err := f.svc.Cancel(run.ID); err != nil {
					t.Fatal(err)
				}
			}
			first := f.settle(t, run.ID)
			switch scenario {
			case "network-resume", "restart-head-advanced", "same-name-different-content", "tampered-sealed-file", "account-changed":
				if first.Status != "failed" || f.gh.uploaded != 1 || f.gh.publishes != 0 {
					t.Fatalf("interruption failed contract: %+v uploads=%d", first, f.gh.uploaded)
				}
				builds := f.builds()
				if scenario == "restart-head-advanced" {
					writeTestFile(t, filepath.Join(f.repo, "tracked.txt"), "later work\n")
					runGit(t, f.repo, "add", "tracked.txt")
					runGit(t, f.repo, "commit", "-m", "advance after build")
					// A new process has no in-memory candidate, and recovers the interrupted row.
					if err := f.svc.store.UpdateReleaseRun(run.ID, "running", "delivery_publish", first.CommitSHA, "", "", false); err != nil {
						t.Fatal(err)
					}
					f.restart(t, run.ID)
					recovered, _ := f.svc.store.GetReleaseRun(run.ID)
					if recovered.ErrorCode != "release_interrupted" {
						t.Fatal("unfinished run not recovered")
					}
				}
				if scenario == "same-name-different-content" {
					for name, a := range f.gh.assets {
						a.Digest = "sha256:" + strings.Repeat("b", 64)
						f.gh.assets[name] = a
					}
				}
				if scenario == "tampered-sealed-file" {
					batches, err := f.svc.delivery.Load(run.ID)
					if err != nil {
						t.Fatal(err)
					}
					path, err := f.svc.delivery.FilePath(batches[0], batches[0].Files[0])
					if err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(path, []byte("changed"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "account-changed" {
					f.gh.account = "another-user"
				}
				last := f.retry(t, run.ID)
				if f.builds() != builds {
					t.Fatal("resume rebuilt packages")
				}
				if scenario == "network-resume" || scenario == "restart-head-advanced" {
					if last.Status != "succeeded" || f.gh.uploaded != 2 || f.gh.attempts != 3 || f.gh.publishes != 1 {
						t.Fatalf("resume result %+v uploads=%d attempts=%d", last, f.gh.uploaded, f.gh.attempts)
					}
				} else if last.Status != "failed" || f.gh.publishes != 0 || f.gh.attempts != 2 {
					t.Fatalf("conflict was not blocked: %+v", last)
				}
			case "missing-signature", "missing-required-file", "cancel-build", "concurrent-services", "timeout-build", "workflow-blocked", "verify-source-change":
				if first.Status != "failed" || f.gh.creates != 0 || f.gh.uploaded != 0 {
					t.Fatalf("invalid build reached GitHub: %+v", first)
				}
				if _, err := f.svc.Retry(run.ID); err == nil {
					t.Fatal("unsealed retry was accepted")
				}
				if scenario == "workflow-blocked" && f.builds() != 0 {
					t.Fatal("unsafe workflow was not blocked before build")
				}
			case "unknown-draft", "tag-conflict":
				if first.Status != "failed" || f.gh.uploaded != 0 || f.gh.publishes != 0 {
					t.Fatalf("remote conflict not blocked: %+v", first)
				}
			default:
				if first.Status != "succeeded" {
					logs, _ := f.svc.store.ReleaseLogs(run.ID, 0, 200)
					t.Fatalf("release failed: %+v logs=%+v", first, logs)
				}
				expected := 2
				if scenario == "multi-target-complete" {
					expected = 4
				}
				if f.gh.uploaded != expected || f.gh.publishes != 1 || f.gh.creates != 1 {
					t.Fatalf("incomplete or duplicate publication: %+v", f.gh)
				}
				if scenario == "digest-fallback" && f.gh.downloads == 0 {
					t.Fatal("missing digest was not downloaded and verified")
				}
				if scenario == "duplicate-submit" {
					again := f.start(t)
					if again.ID != run.ID || f.builds() != 1 {
						t.Fatal("duplicate request created a new build")
					}
				}
				// Repeating the external reconciliation itself must recognize the public release.
				batches, err := f.svc.delivery.Load(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.svc.delivery.Publish(context.Background(), batches[0]); err != nil {
					t.Fatal(err)
				}
				if f.gh.creates != 1 || f.gh.publishes != 1 || f.gh.uploaded != expected {
					t.Fatal("published assets were rewritten")
				}
			}
			rows, err := f.svc.store.ReleaseDeliveries(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.State == "published" && row.SyncState != "unconfigured" {
					t.Fatal("unconfigured server was claimed synchronized")
				}
			}
		})
	}
}
