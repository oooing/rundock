package publisher

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadFailureKeepsLocalReleaseWithoutRemotePrecheck(t *testing.T) {
	for _, scenario := range []string{"offline", "tag-conflict", "branch-conflict", "new-branch"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			remote := uploadTestGit(t, repo, "remote", "get-url", "origin")
			originalRemote := uploadTestGit(t, remote, "rev-parse", "main")
			if scenario == "tag-conflict" {
				uploadTestGit(t, remote, "tag", "v1.0.1", "main")
			}
			if scenario == "branch-conflict" {
				other := filepath.Join(t.TempDir(), "other")
				uploadTestGit(t, t.TempDir(), "clone", "--branch", "main", remote, other)
				uploadTestGit(t, other, "config", "user.name", "Other")
				uploadTestGit(t, other, "config", "user.email", "other@example.invalid")
				writeTestFile(t, filepath.Join(other, "remote.txt"), "other work\n")
				uploadTestGit(t, other, "add", ".")
				uploadTestGit(t, other, "commit", "-m", "other work")
				uploadTestGit(t, other, "push", "origin", "main")
				originalRemote = uploadTestGit(t, remote, "rev-parse", "main")
			}
			if scenario == "new-branch" {
				uploadTestGit(t, repo, "checkout", "-b", "new-feature")
			}
			writeTestFile(t, filepath.Join(repo, "tracked.txt"), "saved local work\n")
			runner := &networkTraceRunner{forbidNetwork: scenario == "offline"}
			svc.runner = runner
			pf, err := svc.PreflightLocal(context.Background(), "app1")
			if err != nil {
				t.Fatal(err)
			}
			yes := true
			run, err := svc.Start(context.Background(), "app1", CreateRequest{CreateTag: &yes, PushRemote: &yes, VersionMode: "auto", TargetVersion: pf.SuggestedVersion, SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint, ReleaseNotes: testReleaseNotes, ReleaseNotesConfirmed: true})
			if err != nil {
				t.Fatalf("upload prevented creation of local release: %v", err)
			}
			done := waitRelease(t, svc, run.ID)
			if runner.count("fetch") != 0 || runner.count("ls-remote") != 0 {
				t.Fatalf("unnecessary precheck: %v", runner.calls)
			}
			if done.CommitSHA == "" || uploadTestGit(t, repo, "rev-parse", "v1.0.1^{commit}") != done.CommitSHA || uploadTestGit(t, repo, "show", "HEAD:tracked.txt") != "saved local work" {
				t.Fatal("local release was lost")
			}
			if scenario == "new-branch" {
				if done.Status != "succeeded" || uploadTestGit(t, remote, "rev-parse", "refs/heads/new-feature") != done.CommitSHA {
					t.Fatalf("new remote branch not created: %+v", done)
				}
				return
			}
			if done.Status != "failed" || !strings.Contains(done.ErrorMessage, "本地提交") {
				t.Fatalf("unhelpful upload result: %+v", done)
			}
			if scenario == "tag-conflict" {
				if done.Stage != "pushing_tag" || uploadTestGit(t, remote, "rev-parse", "v1.0.1") != originalRemote {
					t.Fatal("existing tag overwritten")
				}
			} else if done.Stage != "pushing_branch" || uploadTestGit(t, remote, "rev-parse", "main") != originalRemote {
				t.Fatal("remote branch overwritten")
			}
		})
	}
}

func TestUploadFailureMessageGivesActionWithoutLeakingCredentials(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"fatal: unable to access https://alice:secret@host/repo: SSL handshake failed", "重试上传"},
		{"fatal: Authentication failed", "登录有仓库权限的账号"},
		{"! [rejected] main -> main (fetch first)", "拉取并合并"},
		{"! [rejected] v1.0.1 -> v1.0.1 (already exists)", "新版本号"},
	} {
		got := uploadFailureMessage(tc.raw, errors.New("exit status 128"))
		if !strings.Contains(got, tc.want) || strings.Contains(got, "secret") {
			t.Fatalf("wrong guidance: %s", got)
		}
	}
}

func uploadTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, args...))
}
