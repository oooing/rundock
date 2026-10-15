package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
	"strings"
	"testing"
	"time"
)

func TestCloudTargetsRetainIndependentOutcomes(t *testing.T) {
	now := time.Now().UTC()
	run := &store.ReleaseRun{ID: "release", CommitSHA: "abc", CreateTag: true, CreatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339)}
	plan := &executionPlan{}
	workflows := []githubWorkflowRun{}
	for i, id := range []string{"desktop", "android", "web", "missing"} {
		tag := id + "/v1.2.3"
		plan.ReleaseVersions = append(plan.ReleaseVersions, store.ReleaseVersion{VersionGroupID: id, TagName: tag})
		plan.Targets = append(plan.Targets, planTarget{ID: id, VersionGroup: id, Selection: store.ReleaseTargetSelection{Publish: true}, Steps: releaseconfig.Steps{Publish: "workflow-dispatch:" + id + ".yml"}})
		if id == "missing" {
			continue
		}
		status, conclusion := "completed", "success"
		if id == "android" {
			conclusion = "failure"
		}
		if id == "web" {
			status = "in_progress"
			conclusion = ""
		}
		workflows = append(workflows, githubWorkflowRun{ID: int64(i + 1), Attempt: 1, Name: id, Path: ".github/workflows/" + id + ".yml", HeadSHA: "abc", HeadBranch: tag, Event: "workflow_dispatch", Title: "rundock:release:" + id, Status: status, Conclusion: conclusion, CreatedAt: now.Add(-4 * time.Minute)})
	}
	read := func(_ context.Context, path string, out any) error {
		if strings.Contains(path, "/jobs?") {
			return json.Unmarshal([]byte(`{"jobs":[{"name":"Android","conclusion":"failure","steps":[{"name":"Signing","conclusion":"failure"}]}]}`), out)
		}
		list, ok := out.(*githubRunList)
		if !ok {
			return fmt.Errorf("unexpected %s", path)
		}
		*list = githubRunList{Total: len(workflows), Runs: workflows}
		return nil
	}
	build := &store.CloudBuild{}
	if err := (&Service{}).inspectCloudBuild(context.Background(), read, "fixture/project", run, plan, build, now); err != nil {
		t.Fatal(err)
	}
	if build.State != "failed" || len(build.Targets) != 4 {
		t.Fatalf("%+v", build)
	}
	for i, want := range []string{"succeeded", "failed", "running", "pending"} {
		if build.Targets[i].State != want {
			t.Fatalf("%+v", build.Targets)
		}
	}
	if !strings.Contains(build.Targets[1].Summary, "Signing") || !strings.HasSuffix(build.Targets[1].URL, "/2") {
		t.Fatalf("wrong failure evidence: %+v", build.Targets[1])
	}
	// A rerun for Android changes only that target; the not-yet-started target
	// still prevents the overall batch from becoming successful.
	workflows[1].Attempt = 2
	workflows[1].Conclusion = "success"
	build = &store.CloudBuild{}
	if err := (&Service{}).inspectCloudBuild(context.Background(), read, "fixture/project", run, plan, build, now); err != nil {
		t.Fatal(err)
	}
	if build.State == "succeeded" || build.Targets[1].State != "succeeded" || build.Targets[2].State != "running" {
		t.Fatalf("%+v", build)
	}
}
