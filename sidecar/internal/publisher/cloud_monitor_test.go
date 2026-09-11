package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func TestCloudFailureSupersededByNewerExternalRelease(t *testing.T) {
	now := time.Now().UTC()
	run := &store.ReleaseRun{ID: "old-release", CreateTag: true, CommitSHA: "old-sha", TagName: "web-server/v2.0.27", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)}
	old := githubWorkflowRun{ID: 34493579418, Attempt: 1, Name: "Build Container Image", Path: ".github/workflows/container-image.yml", HeadSHA: run.CommitSHA, HeadBranch: run.TagName, Event: "push", Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-time.Hour)}
	newer := old
	newer.ID, newer.HeadSHA, newer.HeadBranch, newer.Conclusion, newer.CreatedAt = 34498516838, "new-sha", "web-server/v2.0.28", "success", now.Add(-time.Minute)
	for _, tc := range []struct {
		name   string
		change func(*githubWorkflowRun)
		want   string
	}{
		{"new version from another instance", func(*githubWorkflowRun) {}, "superseded"},
		{"other target sharing workflow", func(c *githubWorkflowRun) { c.HeadBranch = "android/v2.0.28" }, "failed"},
		{"different workflow", func(c *githubWorkflowRun) { c.Path = ".github/workflows/other.yml" }, "failed"},
		{"older version published later", func(c *githubWorkflowRun) { c.HeadBranch = "web-server/v2.0.9" }, "failed"},
		{"pending", func(c *githubWorkflowRun) { c.Status, c.Conclusion = "in_progress", "" }, "failed"},
		{"new failure", func(c *githubWorkflowRun) { c.Conclusion = "failure" }, "failed"},
		{"skipped", func(c *githubWorkflowRun) { c.Conclusion = "skipped" }, "failed"},
		{"unrelated event", func(c *githubWorkflowRun) { c.Event = "pull_request" }, "failed"},
		{"old creation", func(c *githubWorkflowRun) { c.CreatedAt = old.CreatedAt.Add(-time.Minute) }, "failed"},
		{"prerelease is not stable recovery", func(c *githubWorkflowRun) { c.HeadBranch += "-beta.1" }, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := newer
			tc.change(&candidate)
			read := func(_ context.Context, endpoint string, out any) error {
				if strings.Contains(endpoint, "/jobs?") {
					return errors.New("no logs")
				}
				candidates := []githubWorkflowRun{old}
				if !strings.Contains(endpoint, "head_sha=") {
					candidates = append(candidates, candidate)
				}
				*out.(*githubRunList) = githubRunList{Total: len(candidates), Runs: candidates}
				return nil
			}
			build := &store.CloudBuild{}
			if err := (&Service{}).inspectCloudBuild(context.Background(), read, "oooing/ingLocalPlay", run, &executionPlan{}, build, now); err != nil {
				t.Fatal(err)
			}
			if build.State != tc.want || (build.AlertKey == "") != (tc.want == "superseded") {
				t.Fatalf("%+v", build)
			}
			if tc.want == "superseded" && build.URL != "https://github.com/oooing/ingLocalPlay/actions/runs/34498516838" {
				t.Fatal(build.URL)
			}
		})
	}
	t.Run("remaining failure retains its own details link", func(t *testing.T) {
		other := old
		other.ID, other.Path = old.ID-1, ".github/workflows/other.yml"
		read := func(_ context.Context, endpoint string, out any) error {
			if strings.Contains(endpoint, "/jobs?") {
				return errors.New("no logs")
			}
			candidates := []githubWorkflowRun{old, other}
			if !strings.Contains(endpoint, "head_sha=") {
				candidates = []githubWorkflowRun{newer}
			}
			*out.(*githubRunList) = githubRunList{Total: len(candidates), Runs: candidates}
			return nil
		}
		build := &store.CloudBuild{}
		if err := (&Service{}).inspectCloudBuild(context.Background(), read, "oooing/ingLocalPlay", run, &executionPlan{}, build, now); err != nil {
			t.Fatal(err)
		}
		if build.State != "failed" || build.AlertKey != "old-release:34493579417:1" || build.URL != "https://github.com/oooing/ingLocalPlay/actions/runs/34493579417" {
			t.Fatalf("%+v", build)
		}
	})
	t.Run("partial workflow recovery and latest rerun", func(t *testing.T) {
		other := old
		other.ID, other.Path = old.ID+1, ".github/workflows/other.yml"
		// Newest rerun of the higher version is pending: an earlier successful
		// attempt must not erase the old failure. API order is deliberately mixed.
		rerun := newer
		rerun.Attempt, rerun.Status, rerun.Conclusion = 2, "in_progress", ""
		for _, runs := range [][]githubWorkflowRun{{newer}, {rerun, newer}} {
			read := func(_ context.Context, _ string, out any) error {
				*out.(*githubRunList) = githubRunList{Total: len(runs), Runs: runs}
				return nil
			}
			got, err := newerSuccessfulBuilds(context.Background(), read, "oooing/ingLocalPlay", []githubWorkflowRun{old, other})
			want := 1
			if len(runs) == 2 {
				want = 0
			}
			if err != nil || len(got) != want {
				t.Fatalf("%+v %v", got, err)
			}
			if _, exists := got[other.ID]; exists {
				t.Fatal("unrelated workflow erased")
			}
		}
	})
	t.Run("pagination and unavailable evidence", func(t *testing.T) {
		for _, offline := range []bool{false, true} {
			read := func(_ context.Context, endpoint string, out any) error {
				list := githubRunList{Total: 101}
				if strings.Contains(endpoint, "page=2") {
					if offline {
						return errors.New("offline")
					}
					list.Runs = []githubWorkflowRun{newer}
				}
				*out.(*githubRunList) = list
				return nil
			}
			got, err := newerSuccessfulBuilds(context.Background(), read, "oooing/ingLocalPlay", []githubWorkflowRun{old})
			if (err != nil) != offline || (len(got) == 1) == offline {
				t.Fatalf("%+v %v", got, err)
			}
		}
	})
}

func TestCloudMonitorPersistsSupersededFailureAndNotifiesOnce(t *testing.T) {
	s, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	plan := executionPlan{SchemaVersion: 1, RemoteURL: "https://github.com/oooing/ingLocalPlay", Automation: &releaseconfig.Automation{Provider: releaseconfig.AutomationGitHubActions, Trigger: releaseconfig.AutomationTriggerTag}}
	raw, _ := json.Marshal(plan)
	run := &store.ReleaseRun{ID: "obsolete", AppID: "app1", TagName: "web-server/v2.0.27", Status: "succeeded", CommitSHA: "old", ExecutionPlan: raw}
	if err := s.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveCloudBuild(&store.CloudBuild{ReleaseRunID: run.ID, State: "failed", AlertKey: "old-failure", CheckedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), NextCheck: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	offline := true
	read := func(_ context.Context, endpoint string, out any) error {
		if strings.Contains(endpoint, "/jobs?") {
			return errors.New("no logs")
		}
		candidate := githubWorkflowRun{ID: 1, Attempt: 1, Path: ".github/workflows/container.yml", HeadSHA: "old", HeadBranch: run.TagName, Event: "push", Status: "completed", Conclusion: "failure", CreatedAt: now}
		if !strings.Contains(endpoint, "head_sha=") {
			if offline {
				return errors.New("offline")
			}
			candidate.ID, candidate.HeadSHA, candidate.HeadBranch, candidate.Conclusion, candidate.CreatedAt = 2, "new", "web-server/v2.0.28", "success", now.Add(time.Minute)
		}
		*out.(*githubRunList) = githubRunList{Total: 1, Runs: []githubWorkflowRun{candidate}}
		return nil
	}
	changes := 0
	s.OnCloudBuildChange = func(*store.CloudBuild) { changes++ }
	s.checkCloudBuilds(context.Background(), read, now.Add(3*time.Minute))
	alerts, _ := s.store.CloudBuildAlerts()
	if len(alerts) != 1 || alerts[0].AlertKey != "old-failure" || changes != 0 {
		t.Fatalf("network erased failure: %+v changes=%d", alerts, changes)
	}
	offline = false
	s.checkCloudBuilds(context.Background(), read, now.Add(6*time.Minute))
	alerts, _ = s.store.CloudBuildAlerts()
	b, _ := s.store.GetCloudBuild(run.ID)
	if len(alerts) != 0 || b.State != "superseded" || changes != 1 {
		t.Fatalf("%+v %+v changes=%d", alerts, b, changes)
	}
	saved, _ := s.store.GetReleaseRun(run.ID)
	if saved.TagName != run.TagName || saved.CommitSHA != run.CommitSHA {
		t.Fatal("release history changed")
	}
	if err := s.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(filepath.Dir(repo), "launcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = New(reopened)
	s.checkCloudBuilds(context.Background(), func(context.Context, string, any) error { t.Fatal("obsolete release was polled again"); return nil }, now.Add(time.Hour))
	alerts, _ = s.store.CloudBuildAlerts()
	if len(alerts) != 0 {
		t.Fatal(alerts)
	}
}

func TestCloudBuildMatchesReleaseAndShowsFailedStep(t *testing.T) {
	now := time.Now().UTC()
	run := &store.ReleaseRun{ID: "release", CommitSHA: "abc", TagName: "web/v2.0.25", CreateTag: true, CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339)}
	plan := &executionPlan{Automation: &releaseconfig.Automation{Workflow: "release.yml"}}
	candidate := githubWorkflowRun{ID: 42, Attempt: 1, Name: "Release", Path: ".github/workflows/release.yml", HeadSHA: "abc", HeadBranch: run.TagName, Event: "push", Status: "completed", Conclusion: "failure", CreatedAt: now.Add(-2 * time.Minute)}
	inspect := func(candidates []githubWorkflowRun, jobsError bool) *store.CloudBuild {
		t.Helper()
		build := &store.CloudBuild{}
		read := func(_ context.Context, endpoint string, out any) error {
			if strings.Contains(endpoint, "/jobs?") {
				if jobsError {
					return errors.New("offline")
				}
				return json.Unmarshal([]byte(`{"jobs":[{"name":"Windows installer","conclusion":"failure","steps":[{"name":"Compile Rust","conclusion":"failure"}]}]}`), out)
			}
			*out.(*githubRunList) = githubRunList{Total: len(candidates), Runs: candidates}
			return nil
		}
		if err := (&Service{}).inspectCloudBuild(context.Background(), read, "oooing/rundock", run, plan, build, now); err != nil {
			t.Fatal(err)
		}
		return build
	}
	t.Run("failure with step", func(t *testing.T) {
		b := inspect([]githubWorkflowRun{candidate}, false)
		if b.State != "failed" || !strings.Contains(b.Summary, "Windows installer / Compile Rust") || b.URL != "https://github.com/oooing/rundock/actions/runs/42" {
			t.Fatalf("%+v", b)
		}
	})
	t.Run("jobs unavailable still a known failure", func(t *testing.T) {
		if b := inspect([]githubWorkflowRun{candidate}, true); b.State != "failed" {
			t.Fatalf("%+v", b)
		}
	})
	for _, field := range []string{"sha", "tag", "event", "created"} {
		t.Run("ignores unrelated "+field, func(t *testing.T) {
			c := candidate
			switch field {
			case "sha":
				c.HeadSHA = "different"
			case "tag":
				c.HeadBranch = "main"
			case "event":
				c.Event = "pull_request"
			case "created":
				c.CreatedAt = now.Add(-time.Hour)
			}
			if b := inspect([]githubWorkflowRun{c}, false); b.AlertKey != "" || b.State == "failed" {
				t.Fatalf("%+v", b)
			}
		})
	}
	t.Run("different workflow for the same release is still monitored", func(t *testing.T) {
		c := candidate
		c.Path = ".github/workflows/container-image.yml"
		if b := inspect([]githubWorkflowRun{c}, false); b.State != "failed" {
			t.Fatalf("matching Tag workflow was discarded: %+v", b)
		}
	})
	t.Run("all workflows for the same tag must succeed", func(t *testing.T) {
		ok := candidate
		ok.Conclusion = "success"
		other := candidate
		other.ID++
		other.Path = ".github/workflows/another-build.yml"
		for _, state := range []string{"failure", "in_progress", "success", "skipped"} {
			other.Status, other.Conclusion = "completed", state
			want := "running"
			if state == "in_progress" {
				other.Status, other.Conclusion = state, ""
			}
			if state == "failure" {
				want = "failed"
			}
			if state == "success" {
				want = "succeeded"
			}
			if b := inspect([]githubWorkflowRun{ok, other}, false); b.State != want {
				t.Fatalf("second workflow %s: %+v", state, b)
			}
		}
	})
	t.Run("success quiet", func(t *testing.T) {
		c := candidate
		c.Conclusion = "success"
		if b := inspect([]githubWorkflowRun{c}, false); b.State != "succeeded" || b.AlertKey != "" {
			t.Fatalf("%+v", b)
		}
	})
	t.Run("new run replaces old failure", func(t *testing.T) {
		c := candidate
		c.ID++
		c.Conclusion = "success"
		if b := inspect([]githubWorkflowRun{candidate, c}, false); b.State != "succeeded" || b.AlertKey != "" {
			t.Fatalf("%+v", b)
		}
	})
	t.Run("rerun in progress", func(t *testing.T) {
		c := candidate
		c.Attempt++
		c.Status = "in_progress"
		c.Conclusion = ""
		if b := inspect([]githubWorkflowRun{c}, false); b.State != "running" || b.AlertKey != "" {
			t.Fatalf("%+v", b)
		}
	})
	t.Run("missing build attention", func(t *testing.T) {
		run.CreatedAt = now.Add(-20 * time.Minute).Format(time.RFC3339)
		if b := inspect(nil, false); b.State != "not_started" || b.AlertKey == "" {
			t.Fatalf("%+v", b)
		}
	})
}

func TestCloudMonitorPersistsDismissalAndSeparatesNetworkFailure(t *testing.T) {
	s, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	plan := executionPlan{SchemaVersion: 1, RemoteURL: "https://github.com/oooing/rundock.git", Automation: &releaseconfig.Automation{Provider: releaseconfig.AutomationGitHubActions, Trigger: releaseconfig.AutomationTriggerTag}}
	raw, _ := json.Marshal(plan)
	run := &store.ReleaseRun{ID: "cloud", AppID: "app1", TagName: "v1.0.1", Status: "succeeded", CommitSHA: "abc", ExecutionPlan: raw}
	if err := s.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	attempt := 1
	reader := func(_ context.Context, endpoint string, out any) error {
		if strings.Contains(endpoint, "/jobs?") {
			return errors.New("jobs unavailable")
		}
		*out.(*githubRunList) = githubRunList{Total: 1, Runs: []githubWorkflowRun{{ID: 42, Attempt: attempt, HeadSHA: "abc", HeadBranch: "v1.0.1", Event: "push", Status: "completed", Conclusion: "failure", CreatedAt: now}}}
		return nil
	}
	alerts := func(want int) {
		t.Helper()
		a, e := s.store.CloudBuildAlerts()
		if e != nil || len(a) != want {
			t.Fatalf("alerts: %+v %v want %d", a, e, want)
		}
	}
	s.checkCloudBuilds(context.Background(), reader, now)
	alerts(1)
	b, err := s.store.GetCloudBuild("cloud")
	if err != nil || b == nil {
		t.Fatalf("%v %+v", err, b)
	}
	if err = s.store.AcknowledgeCloudBuild("cloud", b.AlertKey); err != nil {
		t.Fatal(err)
	}
	alerts(0)
	// Reopen the actual database, as after a process restart.
	if err = s.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(filepath.Dir(repo), "launcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = New(reopened)
	s.checkCloudBuilds(context.Background(), reader, now.Add(6*time.Minute))
	alerts(0)
	attempt++
	s.checkCloudBuilds(context.Background(), reader, now.Add(12*time.Minute))
	alerts(1)
	offline := func(context.Context, string, any) error { return errors.New("network disconnected") }
	s.checkCloudBuilds(context.Background(), offline, now.Add(18*time.Minute))
	alerts(1)
	current, _ := s.store.GetCloudBuild("cloud")
	if current.State != "failed" {
		t.Fatalf("network erased known failure: %+v", current)
	}
	run.ID = "unavailable"
	if err = s.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.checkCloudBuilds(context.Background(), offline, now.Add(time.Duration(25+i*12)*time.Minute))
	}
	current, _ = s.store.GetCloudBuild("unavailable")
	if current.State != "unavailable" || current.AlertKey == "" {
		t.Fatalf("%+v", current)
	}
}

func TestCloudBuildWaitsForAllTagsAndPaginates(t *testing.T) {
	now := time.Now().UTC()
	run := &store.ReleaseRun{ID: "r", CreateTag: true, CommitSHA: "abc", CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339)}
	plan := &executionPlan{ReleaseVersions: []store.ReleaseVersion{{TagName: "web/v1"}, {TagName: "server/v1"}}}
	calls := 0
	second := false
	read := func(_ context.Context, endpoint string, out any) error {
		calls++
		tag := "web/v1"
		if strings.Contains(endpoint, "page=2") {
			tag = "server/v1"
		}
		list := githubRunList{Total: 101}
		if tag == "web/v1" || second {
			list.Runs = []githubWorkflowRun{{ID: int64(calls), HeadSHA: "abc", HeadBranch: tag, Event: "push", Status: "completed", Conclusion: "success", CreatedAt: now}}
		}
		*out.(*githubRunList) = list
		return nil
	}
	for _, all := range []bool{false, true} {
		second = all
		build := &store.CloudBuild{}
		if err := (&Service{}).inspectCloudBuild(context.Background(), read, "oooing/rundock", run, plan, build, now); err != nil {
			t.Fatal(err)
		}
		if (build.State == "succeeded") != all || build.AlertKey != "" {
			t.Fatalf("all=%v %+v", all, build)
		}
	}
	if calls != 4 {
		t.Fatalf("pagination calls %d", calls)
	}
}

// Regression from the actual successful LocalPlay run, captured read-only from
// GitHub. Its exact Tag/SHA match, but the global automation link names extension.
func TestLocalPlayContainerSuccessWithExtensionWorkflowConfigured(t *testing.T) {
	run := &store.ReleaseRun{ID: "20260909T063759-3676e022712f", CreateTag: true,
		CommitSHA: "7a2730096fde11fdf2414b6449b692b0c97be2c2", TagName: "web-server/v2.0.26", CreatedAt: "2026-09-09 06:37:59"}
	plan := &executionPlan{Automation: &releaseconfig.Automation{Workflow: "browser-extension.yml"}}
	read := func(_ context.Context, endpoint string, out any) error {
		if !strings.Contains(endpoint, "head_sha="+run.CommitSHA) {
			t.Fatal(endpoint)
		}
		return json.Unmarshal([]byte(`{"total_count":1,"workflow_runs":[{"id":34319963054,"run_attempt":1,"name":"Build Container Image","path":".github/workflows/container-image.yml","head_sha":"7a2730096fde11fdf2414b6449b692b0c97be2c2","head_branch":"web-server/v2.0.26","event":"push","status":"completed","conclusion":"success","created_at":"2026-09-09T06:38:09Z"}]}`), out)
	}
	build := &store.CloudBuild{}
	if err := (&Service{}).inspectCloudBuild(context.Background(), read, "oooing/ingLocalPlay", run, plan, build, parseReleaseTime("2026-09-09T06:52:38Z")); err != nil {
		t.Fatal(err)
	}
	if build.State != "succeeded" || build.AlertKey != "" || build.URL != "https://github.com/oooing/ingLocalPlay/actions/runs/34319963054" {
		t.Fatalf("%+v", build)
	}
}

func TestCloudMonitorAutomaticallyClearsOldUnmatchedAlert(t *testing.T) {
	s, _, cleanup := newReleaseFixture(t)
	defer cleanup()
	plan := executionPlan{SchemaVersion: 1, RemoteURL: "https://github.com/oooing/rundock", Automation: &releaseconfig.Automation{Provider: releaseconfig.AutomationGitHubActions, Trigger: releaseconfig.AutomationTriggerTag, Workflow: "extension.yml"}}
	raw, _ := json.Marshal(plan)
	run := &store.ReleaseRun{ID: "recover-unmatched", AppID: "app1", TagName: "web-server/v2.0.26", Status: "succeeded", CommitSHA: "abc", ExecutionPlan: raw}
	if err := s.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveCloudBuild(&store.CloudBuild{ReleaseRunID: run.ID, State: "not_started", AlertKey: run.ID + ":not_started"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.store.CloudBuildAlerts()
	if len(before) != 1 {
		t.Fatal(before)
	}
	now := time.Now().UTC()
	read := func(_ context.Context, _ string, out any) error {
		*out.(*githubRunList) = githubRunList{Total: 1, Runs: []githubWorkflowRun{{ID: 42, Attempt: 1, Path: ".github/workflows/container.yml", HeadSHA: "abc", HeadBranch: run.TagName, Event: "push", Status: "completed", Conclusion: "success", CreatedAt: now}}}
		return nil
	}
	s.checkCloudBuilds(context.Background(), read, now.Add(20*time.Minute))
	build, err := s.store.GetCloudBuild(run.ID)
	if err != nil || build.State != "succeeded" || build.AlertKey != "" {
		t.Fatalf("%+v %v", build, err)
	}
	after, err := s.store.CloudBuildAlerts()
	if err != nil || len(after) != 0 {
		t.Fatalf("%+v %v", after, err)
	}
}

func TestCloudMonitorResumesPendingReleaseAndPushesPersistedChanges(t *testing.T) {
	s, _, cleanup := newReleaseFixture(t)
	defer cleanup()
	plan := executionPlan{SchemaVersion: 1, RemoteURL: "https://github.com/oooing/ingLocalPlay", Automation: &releaseconfig.Automation{Provider: releaseconfig.AutomationGitHubActions, Trigger: releaseconfig.AutomationTriggerTag}}
	raw, _ := json.Marshal(plan)
	run := &store.ReleaseRun{ID: "resume", AppID: "app1", TagName: "web-server/v2.0.27", Status: "succeeded", CommitSHA: "3fcf275243da0eaa9c2282f27cd471347e0189ec", ExecutionPlan: raw}
	if err := s.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.store.SaveCloudBuild(&store.CloudBuild{ReleaseRunID: run.ID, State: "running", NextCheck: now.Add(-time.Minute).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	// A fresh service has no in-memory release/session state, as after restart.
	s = New(s.store)
	changes := 0
	s.OnCloudBuildChange = func(build *store.CloudBuild) {
		changes++
		stored, err := s.store.GetCloudBuild(build.ReleaseRunID)
		if err != nil || stored.State != build.State || stored.AlertKey != build.AlertKey {
			t.Fatalf("notification preceded persistence: %+v %v", stored, err)
		}
	}
	conclusion := "failure"
	read := func(_ context.Context, endpoint string, out any) error {
		if strings.Contains(endpoint, "/jobs?") {
			return errors.New("logs unavailable")
		}
		*out.(*githubRunList) = githubRunList{Total: 1, Runs: []githubWorkflowRun{{ID: 34493579418, Attempt: 1, Name: "Build Container Image", Path: ".github/workflows/container-image.yml", HeadSHA: run.CommitSHA, HeadBranch: run.TagName, Event: "push", Status: "completed", Conclusion: conclusion, CreatedAt: now}}}
		return nil
	}
	s.checkCloudBuilds(context.Background(), read, now)
	alerts, err := s.store.CloudBuildAlerts()
	if err != nil || len(alerts) != 1 || alerts[0].State != "failed" || changes != 1 {
		t.Fatalf("alerts=%+v changes=%d error=%v", alerts, changes, err)
	}
	s.checkCloudBuilds(context.Background(), read, now.Add(6*time.Minute))
	if changes != 1 {
		t.Fatal("unchanged failure was broadcast twice")
	}
	conclusion = "success"
	s.checkCloudBuilds(context.Background(), read, now.Add(12*time.Minute))
	alerts, err = s.store.CloudBuildAlerts()
	if err != nil || len(alerts) != 0 || changes != 2 {
		t.Fatalf("recovery alerts=%+v changes=%d error=%v", alerts, changes, err)
	}
}
