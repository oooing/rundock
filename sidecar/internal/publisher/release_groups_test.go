package publisher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func groupedFixture() (*executionPlan, map[string]delivery.Batch, map[string][]delivery.Source) {
	p := &executionPlan{ReleaseNotes: "**发布范围：** PC · 本地、Web · 本地\n\n## 修复\n- 修复续抓"}
	batches := map[string]delivery.Batch{}
	sources := map[string][]delivery.Source{}
	for i, id := range []string{"desktop", "web-server"} {
		version, name := "1.2.43", "Windows 客户端"
		files := []string{"pc.exe", "pc.exe.sig", "pc.exe.sha256", "desktop-release.json"}
		if i == 1 {
			version, name = "2.0.51", "Web＋服务端"
			files = []string{"web.zip", "web.zip.sha256", "web-native-release.json"}
		}
		p.VersionGroups = append(p.VersionGroups, planVersionGroup{ID: id, Name: name})
		p.Targets = append(p.Targets, planTarget{ID: id, VersionGroup: id, Selection: store.ReleaseTargetSelection{Publish: true}, Delivery: &releaseconfig.Delivery{ReleaseGroup: "product", Repository: "fixture/project", Account: "fixture", MakeLatest: i == 1}})
		batches[id] = delivery.Batch{RunID: "group-test", AppID: "app1", GroupID: id, Repository: "fixture/project", Account: "fixture", Commit: strings.Repeat("a", 40), Tag: id + "/v" + version, Version: version, MakeLatest: i == 1, Notes: p.ReleaseNotes, Workflows: map[string]string{}}
		for _, file := range files {
			sources[id] = append(sources[id], delivery.Source{TargetID: id, Path: file})
		}
	}
	return p, batches, sources
}

func TestCombinedReleaseGroups(t *testing.T) {
	p, batches, sources := groupedFixture()
	merged, files, err := combineReleaseGroups(p, batches, sources)
	if err != nil {
		t.Fatal(err)
	}
	b := merged["release-product"]
	if len(merged) != 1 || len(files[b.GroupID]) != 7 || len(b.Components) != 2 || b.Tag != "web-server/v2.0.51" || !b.MakeLatest {
		t.Fatalf("wrong combined release: %+v", b)
	}
	for _, want := range []string{"Windows 客户端 v1.2.43", "Web＋服务端 v2.0.51"} {
		if !strings.Contains(b.Title, want) {
			t.Fatal(b.Title)
		}
	}
	for _, want := range []string{"desktop-release", "PC · 本地"} {
		if strings.Contains(b.Notes, want) {
			t.Fatal(b.Notes)
		}
	}
	for _, want := range []string{"pc.exe", "web.zip", `"desktop":"1.2.43"`, `"web-server":"2.0.51"`, "修复续抓"} {
		if !strings.Contains(b.Notes, want) {
			t.Fatal(b.Notes)
		}
	}
	// A fresh identical plan freezes exactly the same manifest.
	p2, b2, s2 := groupedFixture()
	second, _, _ := combineReleaseGroups(p2, b2, s2)
	one, _ := json.Marshal(b)
	two, _ := json.Marshal(second[b.GroupID])
	if string(one) != string(two) {
		t.Fatal("non-deterministic release")
	}
}

func TestCombinedReleaseOptInAndConflicts(t *testing.T) {
	for _, change := range []string{"none", "account", "repo", "channel", "workflow", "single"} {
		t.Run(change, func(t *testing.T) {
			p, b, s := groupedFixture()
			d := p.Targets[1].Delivery
			switch change {
			case "none":
				for i := range p.Targets {
					p.Targets[i].Delivery.ReleaseGroup = ""
				}
			case "account":
				d.Account = "other"
			case "repo":
				d.Repository = "fixture/other"
			case "channel":
				d.Prerelease = true
			case "workflow":
				d.Deployment = &releaseconfig.Deployment{Workflow: "deploy.yml"}
			case "single":
				p.Targets = p.Targets[:1]
				delete(b, "web-server")
				delete(s, "web-server")
			}
			out, _, err := combineReleaseGroups(p, b, s)
			if change == "none" {
				if err != nil || len(out) != 2 {
					t.Fatal("legacy layout changed")
				}
				return
			}
			if change == "single" {
				if err != nil || out["release-product"].Tag != "desktop/v1.2.43" {
					t.Fatal("single platform broken")
				}
				return
			}
			if err == nil {
				t.Fatal("conflict accepted")
			}
		})
	}
}

// Real local Git/SQLite/vault, simulated GitHub only. Seven files, one draft,
// a failed upload, restart-style reload, then a single public release.
func TestCombinedReleasePublishResume(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	p, batches, sources := groupedFixture()
	commit := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	run := &store.ReleaseRun{ID: "group-test", AppID: "app1", RepoRoot: repo, Branch: "main", CommitSHA: commit, Status: "running", Stage: "delivery_publish"}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	for id, b := range batches {
		b.Commit = commit
		batches[id] = b
		runGit(t, repo, "tag", b.Tag)
		runGit(t, repo, "push", "origin", "refs/tags/"+b.Tag)
		for i, source := range sources[id] {
			source.Path = filepath.Join(t.TempDir(), source.Path)
			if err := os.WriteFile(source.Path, []byte(source.Path), 0600); err != nil {
				t.Fatal(err)
			}
			sources[id][i] = source
		}
	}
	merged, files, err := combineReleaseGroups(p, batches, sources)
	if err != nil {
		t.Fatal(err)
	}
	b := merged["release-product"]
	ctx := context.Background()
	if err := svc.delivery.Seal(ctx, b, files[b.GroupID]); err != nil {
		t.Fatal(err)
	}
	loaded, err := svc.delivery.Load(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.sealedDeliveryReady(run, p) {
		t.Fatal("merged delivery cannot resume")
	}
	gh := &deliveryRemote{repo: filepath.Join(filepath.Dir(repo), "remote.git"), account: "fixture", assets: map[string]remoteAsset{}, failUploadAt: 3}
	svc.SetDeliveryClient(gh)
	if err := svc.delivery.Publish(ctx, loaded[0]); err == nil {
		t.Fatal("expected upload interruption")
	} else {
		t.Logf("interruption: %v", err)
	}
	if gh.creates != 1 || gh.publishes != 0 || !gh.release.Draft {
		t.Fatalf("unexpected partial state: creates=%d publishes=%d release=%+v", gh.creates, gh.publishes, gh.release)
	}
	loaded, err = svc.delivery.Load(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.delivery.Publish(ctx, loaded[0]); err != nil {
		t.Fatal(err)
	}
	if err := svc.delivery.Publish(ctx, loaded[0]); err != nil {
		t.Fatal(err)
	}
	if gh.creates != 1 || gh.publishes != 1 || gh.uploaded != 7 || gh.attempts != 8 {
		t.Fatalf("duplicate or incomplete release: creates=%d publishes=%d uploaded=%d attempts=%d", gh.creates, gh.publishes, gh.uploaded, gh.attempts)
	}
	if gh.release.Name != b.Title {
		t.Fatal("title was not published")
	}
	rows, _ := svc.store.ReleaseDeliveries(run.ID)
	if len(rows) != 1 || rows[0].State != "published" {
		t.Fatal(rows)
	}
	gh.conflictTag = "desktop/v1.2.43"
	if err := svc.delivery.Publish(ctx, loaded[0]); err == nil {
		t.Fatal("secondary platform tag mismatch was ignored")
	}
	if gh.publishes != 1 {
		t.Fatal("tag conflict caused another publication")
	}
}
