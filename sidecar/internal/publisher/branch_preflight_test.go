package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func advanceRemoteBranch(t *testing.T, repo string) {
	t.Helper()
	remote := strings.TrimSpace(runGit(t, repo, "remote", "get-url", "origin"))
	clone := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(clone), "clone", "--branch", "main", remote, clone)
	runGit(t, clone, "config", "user.email", "fixture@example.invalid")
	runGit(t, clone, "config", "user.name", "Fixture")
	writeTestFile(t, filepath.Join(clone, "README.md"), "remote documentation update\n")
	runGit(t, clone, "add", "README.md")
	runGit(t, clone, "commit", "-m", "remote docs")
	runGit(t, clone, "push", "origin", "main")
}

func TestRemoteBranchStopsReleaseBeforeBuild(t *testing.T) {
	f := newDeliveryFixture(t, "normal", false)
	before := runGit(t, f.repo, "rev-parse", "HEAD")
	status := runGit(t, f.repo, "status", "--porcelain")
	tracking := runGit(t, f.repo, "rev-parse", "refs/remotes/origin/main")
	fetchHead := filepath.Join(f.repo, ".git", "FETCH_HEAD")
	writeTestFile(t, fetchHead, "preserve fetch head\n")
	advanceRemoteBranch(t, f.repo)
	final := f.settle(t, f.start(t).ID)
	if final.Status != "failed" || final.Stage != "preparing" || final.ErrorCode != "branch_behind" {
		t.Fatalf("unexpected result: %+v", final)
	}
	if f.builds() != 0 || f.gh.creates != 0 || f.gh.attempts != 0 {
		t.Fatal("branch check allowed a build or release write")
	}
	if runGit(t, f.repo, "rev-parse", "HEAD") != before || runGit(t, f.repo, "status", "--porcelain") != status {
		t.Fatal("branch check changed source or index")
	}
	if runGit(t, f.repo, "rev-parse", "refs/remotes/origin/main") != tracking {
		t.Fatal("branch check changed tracking refs")
	}
	raw, err := os.ReadFile(fetchHead)
	if err != nil || string(raw) != "preserve fetch head\n" {
		t.Fatal("branch check changed FETCH_HEAD")
	}
	version, _ := os.ReadFile(filepath.Join(f.repo, "package.json"))
	if !strings.Contains(string(version), "1.0.0") || runGit(t, f.repo, "tag", "--list") != "" {
		t.Fatal("branch check changed release version or tags")
	}
}

func TestReleaseBranchPreflight(t *testing.T) {
	for _, scenario := range []string{"current", "local-ahead", "remote-ahead", "diverged", "new-branch", "push-destination"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			if scenario == "local-ahead" || scenario == "diverged" {
				writeTestFile(t, filepath.Join(repo, "local.txt"), "local change\n")
				runGit(t, repo, "add", "local.txt")
				runGit(t, repo, "commit", "-m", "local code")
			}
			if scenario == "remote-ahead" || scenario == "diverged" {
				advanceRemoteBranch(t, repo)
			}
			if scenario == "push-destination" {
				pushRepo := filepath.Join(t.TempDir(), "push.git")
				runGit(t, filepath.Dir(pushRepo), "clone", "--bare", strings.TrimSpace(runGit(t, repo, "remote", "get-url", "origin")), pushRepo)
				runGit(t, repo, "remote", "set-url", "--push", "origin", pushRepo)
				// Advance only the push destination; the fetch remote stays current.
				other := filepath.Join(t.TempDir(), "writer")
				runGit(t, filepath.Dir(other), "clone", "--branch", "main", pushRepo, other)
				advanceRemoteBranch(t, other)
			}
			branch := "main"
			if scenario == "new-branch" {
				branch = "new-release"
			}
			run := &store.ReleaseRun{RepoRoot: repo, RemoteName: "origin", Branch: branch}
			issue := svc.checkReleaseBranch(context.Background(), run, strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")))
			blocked := scenario == "remote-ahead" || scenario == "diverged" || scenario == "push-destination"
			if blocked && (issue == nil || issue.Code != "branch_behind") || !blocked && issue != nil {
				t.Fatalf("unexpected issue: %+v", issue)
			}
		})
	}
}
