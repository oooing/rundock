package publisher

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightComparesAgainstReleaseNotRemoteBranch(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if pf.CommitsSinceTags[""] != 1 {
		t.Fatal("first release must include existing code", pf.CommitsSinceTags)
	}
	runGit(t, repo, "tag", "-a", "v1.0.0", "-m", "Release")
	pf, err = svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := pf.CommitsSinceTags["v1.0.0"]; !ok || n != 0 {
		t.Fatal("same HEAD must have no new commits", pf.CommitsSinceTags)
	}
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "new feature\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "feat: new feature")
	runGit(t, repo, "push", "origin", "main")
	pf, err = svc.Preflight(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	if pf.AheadCount != 0 || pf.CommitsSinceTags["v1.0.0"] != 1 {
		t.Fatal("pushed commits can still be unreleased", pf)
	}
}

func TestContentComparisonSeparatesVersionGroupsAndUnknownTags(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runGit(t, repo, "tag", "web/v1.0.0")
	runGit(t, repo, "commit", "--allow-empty", "-m", "next")
	runGit(t, repo, "tag", "desktop/v1.0.0")
	pf := &Preflight{RepoRoot: repo, HeadSHA: strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD")), LatestTag: "missing-tag", LatestGroupTags: map[string]string{"web": "web/v1.0.0", "desktop": "desktop/v1.0.0"}}
	svc.compareReleaseContent(context.Background(), pf, nil)
	if pf.CommitsSinceTags["web/v1.0.0"] != 1 || pf.CommitsSinceTags["desktop/v1.0.0"] != 0 {
		t.Fatal(pf.CommitsSinceTags)
	}
	if _, ok := pf.CommitsSinceTags["missing-tag"]; ok {
		t.Fatal("unknown must not become zero")
	}
	svc.compareReleaseContent(context.Background(), pf, map[string]string{"missing-tag": pf.HeadSHA})
	if n, ok := pf.CommitsSinceTags["missing-tag"]; !ok || n != 0 {
		t.Fatal("remote-only tag should resolve via its known commit", pf.CommitsSinceTags)
	}
}

func TestAnnotatedRemoteTagsUsePeeledCommit(t *testing.T) {
	tags := parseTagRevisions("commit refs/tags/v1.0.0^{}\nobject refs/tags/v1.0.0\nlight refs/tags/v2.0.0\n")
	if tags["v1.0.0"] != "commit" || tags["v2.0.0"] != "light" {
		t.Fatal(tags)
	}
}
