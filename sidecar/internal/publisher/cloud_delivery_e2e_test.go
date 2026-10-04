package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/store"
)

type dispatchRemote struct {
	*deliveryRemote
	requests, accepted   int
	hidden, lost, reject bool
	marker, commit, tag  string
}

func (d *dispatchRemote) JSON(ctx context.Context, method, endpoint string, body, out any) error {
	if !strings.Contains(endpoint, "/actions/") {
		return d.deliveryRemote.JSON(ctx, method, endpoint, body, out)
	}
	if method == "POST" {
		d.requests++
		if d.reject {
			return &delivery.HTTPError{Status: 422}
		}
		value := body.(map[string]any)
		inputs := value["inputs"].(map[string]string)
		d.marker = "rundock:" + inputs["release_run_id"] + ":" + inputs["target_id"]
		d.commit = inputs["release_commit"]
		d.tag = value["ref"].(string)
		d.accepted++
		if d.lost {
			return fmt.Errorf("response lost")
		}
		return nil
	}
	runs := []map[string]any{}
	if !d.hidden && d.accepted > 0 {
		runs = append(runs, map[string]any{"id": 1, "display_title": d.marker, "head_sha": d.commit, "head_branch": d.tag, "event": "workflow_dispatch", "path": ".github/workflows/release.yml", "status": "completed", "conclusion": "success", "created_at": time.Now().UTC()})
	}
	return remoteResult(map[string]any{"total_count": len(runs), "workflow_runs": runs}, out)
}

// Runs the complete candidate -> Git push -> dispatch -> restart/retry -> monitor flow.
func TestCloudDispatchE2E(t *testing.T) {
	if os.Getenv("RUNDOCK_DELIVERY_E2E") != "1" {
		t.Skip("opt-in acceptance")
	}
	evidence := []map[string]any{}
	defer func() {
		raw, _ := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "boundary": "real Git, candidates and SQLite; simulated Actions API", "scenarios": evidence}, "", "  ")
		if dir := os.Getenv("RUNDOCK_DELIVERY_EVIDENCE"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, "cloud-dispatch-e2e.json"), raw, 0600); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, scenario := range []string{"accepted", "lost-response-visible", "lost-response-restart", "rejected-then-retry"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDeliveryFixture(t, "cloud", false)
			remote := &dispatchRemote{deliveryRemote: f.gh, lost: strings.HasPrefix(scenario, "lost"), hidden: scenario == "lost-response-restart", reject: scenario == "rejected-then-retry"}
			f.svc.SetDeliveryClient(remote)
			run := f.start(t)
			run = f.settle(t, run.ID)
			defer func() {
				evidence = append(evidence, map[string]any{"scenario": scenario, "passed": !t.Failed(), "requests": remote.requests, "accepted": remote.accepted, "localBuilds": f.builds(), "status": run.Status, "errorCode": run.ErrorCode})
			}()
			if remote.hidden {
				if run.Status != "failed" {
					t.Fatalf("uncertain dispatch must wait: %+v", run)
				}
				f.restart(t, run.ID)
				f.svc.SetDeliveryClient(remote)
				run = f.retry(t, run.ID)
				if run.Status != "failed" || remote.requests != 1 {
					t.Fatal("uncertain request was submitted twice")
				}
				remote.hidden = false
				run = f.retry(t, run.ID)
			}
			if remote.reject {
				remote.reject = false
				run = f.retry(t, run.ID)
			}
			if run.Status != "succeeded" || remote.accepted != 1 || f.builds() != 0 {
				t.Fatalf("cloud handoff failed: %+v; accepted=%d", run, remote.accepted)
			}
			plan, err := parseExecutionPlan(run.ExecutionPlan)
			if err != nil {
				t.Fatal(err)
			}
			build := &store.CloudBuild{}
			err = f.svc.inspectCloudBuild(context.Background(), func(ctx context.Context, p string, out any) error { return remote.JSON(ctx, "GET", p, nil, out) }, "fixture/project", run, plan, build, time.Now().UTC().Add(3*time.Minute))
			if err != nil || build.State != "succeeded" {
				t.Fatalf("dispatch monitor mismatch: %+v %v", build, err)
			}
		})
	}
}
