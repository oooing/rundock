package publisher

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func decodeTagReleasePlan(t *testing.T, message string) tagReleasePlan {
	t.Helper()
	marker := "<!-- launcher-release-plan:"
	start := strings.Index(message, marker)
	if start < 0 {
		t.Fatalf("tag metadata missing: %s", message)
	}
	end := strings.Index(message[start:], " -->")
	if end < 0 {
		t.Fatalf("tag metadata is incomplete: %s", message)
	}
	encoded := message[start+len(marker) : start+end]
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var metadata tagReleasePlan
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("invalid tag metadata: %v raw=%s", err, raw)
	}
	return metadata
}

func TestDraftReleaseNotesIsDeterministicAndRejectsStaleStatus(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runGit(t, repo, "tag", "v1.0.0")
	for _, subject := range []string{
		"feat(search): 支持按关键词筛选视频",
		"fix(download): 修复重复下载任务",
		"perf(catalog): 缩短模型目录加载耗时",
		"refactor: 调整目录结构",
		"chore(release): v1.0.1",
		"888",
	} {
		runGit(t, repo, "commit", "--allow-empty", "-m", subject)
	}
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "修复后的内容\n")
	writeTestFile(t, filepath.Join(repo, "新增说明.md"), "说明\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	req := NotesDraftRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"新增说明.md", "tracked.txt"}}
	first, err := svc.DraftReleaseNotes(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.DraftReleaseNotes(context.Background(), "app1", req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != second.Text || first.SourceFingerprint != second.SourceFingerprint {
		t.Fatalf("draft is not deterministic: first=%+v second=%+v", first, second)
	}
	for _, wanted := range []string{
		"**发布范围：** 仅提交代码",
		"## 功能\n- 支持按关键词筛选视频",
		"## 问题修复\n- 修复重复下载任务",
		"## 性能优化\n- 缩短模型目录加载耗时",
	} {
		if !strings.Contains(first.Text, wanted) {
			t.Fatalf("draft missing %q: %s", wanted, first.Text)
		}
	}
	for _, unwanted := range []string{"新增说明.md", "tracked.txt", "新增文件", "更新文件", "refactor", "888", "chore(release)", "## 其他"} {
		if strings.Contains(first.Text, unwanted) {
			t.Fatalf("draft contains noise %q: %s", unwanted, first.Text)
		}
	}
	if first.BaseTag != "v1.0.0" || first.ChangeCount != 2 || first.CommitCount != 6 || first.SourceFingerprint == "" {
		t.Fatalf("draft metadata = %+v", first)
	}
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "仓库再次变化\n")
	if _, err := svc.DraftReleaseNotes(context.Background(), "app1", req); err == nil {
		t.Fatal("stale draft request was accepted")
	} else if pe, ok := err.(*Error); !ok || pe.Code != "status_changed" {
		t.Fatalf("stale draft error = %#v", err)
	}
}

func TestDraftReleaseNotesKeepsEmptySectionsWithoutSemanticCommits(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	runGit(t, repo, "tag", "v1.0.0")
	runGit(t, repo, "commit", "--allow-empty", "-m", "chore(release): v1.0.1")
	runGit(t, repo, "commit", "--allow-empty", "-m", "777")
	writeTestFile(t, filepath.Join(repo, "private-file-name.ts"), "export const secret = true\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.DraftReleaseNotes(context.Background(), "app1", NotesDraftRequest{
		StatusFingerprint: pf.StatusFingerprint,
		SelectedPaths:     []string{"private-file-name.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{"## 功能", "## 问题修复", "## 性能优化"} {
		if !strings.Contains(draft.Text, heading) {
			t.Fatalf("draft did not reserve %q: %s", heading, draft.Text)
		}
	}
	for _, unwanted := range []string{"private-file-name.ts", "新增文件", "更新文件", "chore(release)", "777", "请补充"} {
		if strings.Contains(draft.Text, unwanted) {
			t.Fatalf("review draft contains noise %q: %s", unwanted, draft.Text)
		}
	}
}

func TestReleaseNoteSubjectClassificationAndNoiseFiltering(t *testing.T) {
	tests := []struct {
		subject, category, contains string
		ok                          bool
	}{
		{subject: "feat(search): 支持组合筛选", category: releaseNoteFeature, contains: "支持组合筛选", ok: true},
		{subject: "fix(api): 修复 `src/api/client.ts` 登录失败", category: releaseNoteFix, contains: "登录失败", ok: true},
		{subject: "bug(player): 播放结束后状态未刷新", category: releaseNoteFix, contains: "状态未刷新", ok: true},
		{subject: "perf(cache): reduce src/cache.go latency", category: releaseNotePerformance, contains: "reduce latency", ok: true},
		{subject: "解决问题：重复任务会被创建", category: releaseNoteFix, contains: "重复任务会被创建", ok: true},
		{subject: "提升搜索性能并降低内存占用", category: releaseNotePerformance, contains: "提升搜索性能", ok: true},
		{subject: "refactor: reorganize services", ok: false},
		{subject: "优化页面排版", ok: false},
		{subject: "chore(release): v2.0.4", ok: false},
		{subject: "v2.0.4", ok: false},
		{subject: "888", ok: false},
	}
	for _, test := range tests {
		t.Run(test.subject, func(t *testing.T) {
			category, line, ok := releaseNoteFromSubject(test.subject)
			if ok != test.ok || (ok && (category != test.category || !strings.Contains(line, test.contains))) {
				t.Fatalf("releaseNoteFromSubject(%q) = %q, %q, %t", test.subject, category, line, ok)
			}
			if strings.Contains(line, "src/") || strings.Contains(line, ".ts") || strings.Contains(line, ".go") {
				t.Fatalf("file reference leaked: %q", line)
			}
		})
	}
}

func TestRenderReleaseNotesIsConcise(t *testing.T) {
	categories := map[string][]string{
		releaseNoteFeature:     {"功能 1", "功能 2", "功能 3", "功能 4", "功能 5", "功能 6", "功能 7"},
		releaseNoteFix:         {},
		releaseNotePerformance: {},
	}
	text := renderReleaseNotes("Web", categories)
	if strings.Count(text, "\n- ") != 6 || !strings.Contains(text, "另有 2 项同类更新") || strings.Contains(text, "功能 6\n") {
		t.Fatalf("release notes are not concise: %s", text)
	}
	for _, heading := range []string{"## 功能", "## 问题修复", "## 性能优化"} {
		if !strings.Contains(text, heading) {
			t.Fatalf("release notes did not reserve %q: %s", heading, text)
		}
	}
}

func TestReleaseNotesBaseTagsFollowSelectedVersionGroups(t *testing.T) {
	pf := &Preflight{
		LatestTag: "v1.2.29",
		LatestGroupTags: map[string]string{
			"web":     "web-server/v2.0.3",
			"android": "android/v1.2.32",
		},
	}
	plan := &executionPlan{
		VersionGroups: []planVersionGroup{{ID: "web"}, {ID: "android"}},
		Targets:       []planTarget{{ID: "web-target", VersionGroup: "web"}, {ID: "android-target", VersionGroup: "android"}},
	}
	if got := strings.Join(releaseNotesBaseTags(pf, plan), ","); got != "web-server/v2.0.3,android/v1.2.32" {
		t.Fatalf("selected base tags = %q", got)
	}
	if got := strings.Join(releaseNotesBaseTags(pf, &executionPlan{}), ","); got != "v1.2.29" {
		t.Fatalf("repository base tag = %q", got)
	}
}

func TestReleaseNotesValidation(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		code  string
	}{
		{name: "blank", value: "  \n", code: "release_notes_required"},
		{name: "control", value: "ok\x01", code: "invalid_release_notes"},
		{name: "reserved marker", value: "<!-- launcher-release-plan:forged -->", code: "invalid_release_notes"},
		{name: "too long", value: strings.Repeat("更", maxReleaseNotesBytes), code: "release_notes_too_long"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeReleaseNotes(test.value)
			pe, ok := err.(*Error)
			if !ok || pe.Code != test.code {
				t.Fatalf("error = %#v, want %s", err, test.code)
			}
		})
	}
	if got, err := normalizeReleaseNotes("新增：中文说明\r\n\r\n修复问题"); err != nil || strings.Contains(got, "\r") {
		t.Fatalf("valid notes = %q, err=%v", got, err)
	}
}

func TestAutomationHandoffCopyDistinguishesSourceOnly(t *testing.T) {
	run := &store.ReleaseRun{CreateTag: true, PushRemote: true, Status: "succeeded"}
	plan := &executionPlan{Automation: &releaseconfig.Automation{
		Provider: releaseconfig.AutomationGitHubActions,
		Trigger:  releaseconfig.AutomationTriggerTag,
	}}
	if view := automationHandoffView(run, plan); view == nil || !strings.Contains(view.Message, "源码 Release") {
		t.Fatalf("source-only handoff = %+v", view)
	}
	plan.Targets = []planTarget{{ID: "windows"}}
	if view := automationHandoffView(run, plan); view == nil || !strings.Contains(view.Message, "自动构建") {
		t.Fatalf("Windows handoff = %+v", view)
	}
}

func TestAnnotatedTagContainsFrozenNotesAndReleasePlan(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "tag body\n")
	pf, err := svc.Preflight(context.Background(), "app1")
	if err != nil || !pf.CanRelease {
		t.Fatalf("preflight: %v %+v", err, pf.BlockingIssues)
	}
	run, err := svc.Start(context.Background(), "app1", CreateRequest{
		TargetVersion: "1.0.1", SelectedPaths: []string{"tracked.txt"}, StatusFingerprint: pf.StatusFingerprint,
		ReleaseNotes: "## 修复\n- 修复发布问题", ReleaseNotesConfirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	run = waitRelease(t, svc, run.ID)
	if run.Status != "succeeded" {
		t.Fatalf("release = %+v", run)
	}
	contents := runGit(t, repo, "for-each-ref", "--format=%(contents)", "refs/tags/"+run.TagName)
	if !strings.Contains(contents, "## 修复\n- 修复发布问题") {
		t.Fatalf("tag lost release notes: %s", contents)
	}
	metadata := decodeTagReleasePlan(t, contents)
	if metadata.TagName != "v1.0.1" || metadata.TargetVersion != "1.0.1" || !metadata.PushRemote {
		t.Fatalf("metadata=%+v", metadata)
	}
}

func TestRetryRejectsRunWithoutFrozenTagPlan(t *testing.T) {
	for _, test := range []struct {
		name string
		plan *executionPlan
	}{
		{name: "empty execution plan"},
		{name: "unconfirmed notes", plan: &executionPlan{
			SchemaVersion: executionPlanSchemaVersion, VersionGroups: []planVersionGroup{}, Targets: []planTarget{},
			ReleaseVersions: []store.ReleaseVersion{{VersionGroupID: "repository", TargetVersion: "1.0.1", TagName: "v1.0.1"}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
			var planJSON json.RawMessage
			if test.plan != nil {
				pushRemote := true
				test.plan.PushRemote = &pushRemote
				planJSON, _ = test.plan.marshal()
			}
			run := &store.ReleaseRun{ID: "legacy-retry", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin",
				TargetVersion: "1.0.1", TagName: "v1.0.1", CreateTag: true, PushRemote: true, ExecutionPlan: planJSON,
				Status: "failed", Stage: "tagging", CommitSHA: sha}
			if err := svc.store.CreateReleaseRun(run); err != nil {
				t.Fatal(err)
			}
			_, err := svc.Retry(run.ID)
			pe, ok := err.(*Error)
			if !ok || pe.Code != "release_retry_requires_preflight" {
				t.Fatalf("legacy retry error = %#v", err)
			}
			stored, _ := svc.store.GetReleaseRun(run.ID)
			if stored.Status != "failed" || strings.TrimSpace(runGit(t, repo, "tag", "--list")) != "" {
				t.Fatalf("legacy retry changed repository state: %+v", stored)
			}
		})
	}
}

func TestRetryRejectsExistingTagWithDifferentFrozenContents(t *testing.T) {
	for _, stage := range []string{"tagging", "pushing_branch", "pushing_tag"} {
		t.Run(stage, func(t *testing.T) {
			svc, repo, cleanup := newReleaseFixture(t)
			defer cleanup()
			remoteBefore := strings.TrimSpace(runGit(t, repo, "rev-parse", "origin/main"))
			writeTestFile(t, filepath.Join(repo, "tracked.txt"), "retry content\n")
			runGit(t, repo, "add", "tracked.txt")
			runGit(t, repo, "commit", "-m", "retry content")
			sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
			if stage == "pushing_tag" {
				runGit(t, repo, "push", "origin", "main")
			}
			version := store.ReleaseVersion{VersionGroupID: "repository", TargetVersion: "1.0.1", TagName: "v1.0.1"}
			plan := frozenTagExecutionPlan(true, version)
			planJSON, _ := plan.marshal()
			runGit(t, repo, "tag", "-a", version.TagName, sha, "-m", "different contents")
			run := &store.ReleaseRun{ID: "retry-tag-content", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin",
				TargetVersion: version.TargetVersion, TagName: version.TagName, CreateTag: true, PushRemote: true, Versions: []store.ReleaseVersion{version}, ExecutionPlan: planJSON,
				Status: "failed", Stage: stage, CommitSHA: sha}
			if err := svc.store.CreateReleaseRun(run); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Retry(run.ID, RetryRequest{ExternalActionsConfirmed: true}); err != nil {
				t.Fatal(err)
			}
			failed := waitRelease(t, svc, run.ID)
			if failed.Status != "failed" || failed.ErrorCode != "tag_collision" {
				t.Fatalf("retry accepted inconsistent tag: %+v", failed)
			}
			if remoteTag := strings.TrimSpace(runGit(t, repo, "ls-remote", "--tags", "origin", "refs/tags/"+version.TagName)); remoteTag != "" {
				t.Fatalf("inconsistent tag was pushed: %s", remoteTag)
			}
			if stage == "pushing_branch" {
				remoteAfter := strings.TrimSpace(runGit(t, repo, "rev-parse", "origin/main"))
				if remoteAfter != remoteBefore {
					t.Fatalf("branch changed before tag validation: before=%s after=%s", remoteBefore, remoteAfter)
				}
			}
		})
	}
}

func TestTagPlanPublishesReleaseRequiresRemotePush(t *testing.T) {
	for _, test := range []struct {
		name                   string
		push, configured, want bool
	}{
		{name: "local only", push: false, configured: true, want: false},
		{name: "remote release", push: true, configured: true, want: true},
		{name: "remote without release", push: true, configured: false, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			version := store.ReleaseVersion{VersionGroupID: "repository", TargetVersion: "1.0.1", TagName: "v1.0.1"}
			plan := frozenTagExecutionPlan(test.push, version)
			plan.Automation = &releaseconfig.Automation{PublishesRelease: test.configured}
			message, err := tagMessageForVersion(plan, version)
			if err != nil {
				t.Fatal(err)
			}
			metadata := decodeTagReleasePlan(t, message)
			if metadata.PublishesRelease != test.want {
				t.Fatalf("publishesRelease = %t, want %t", metadata.PublishesRelease, test.want)
			}
		})
	}
}

func TestRetryTagPushHasDeadline(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	version := store.ReleaseVersion{VersionGroupID: "repository", TargetVersion: "1.0.1", TagName: "v1.0.1"}
	plan := frozenTagExecutionPlan(true, version)
	message, err := tagMessageForVersion(plan, version)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "tag", "-a", version.TagName, "--cleanup=verbatim", "-m", message, sha)
	planJSON, _ := plan.marshal()
	run := &store.ReleaseRun{ID: "retry-push-deadline", AppID: "app1", RepoRoot: repo, Branch: "main", RemoteName: "origin",
		TargetVersion: version.TargetVersion, TagName: version.TagName, CreateTag: true, PushRemote: true, Versions: []store.ReleaseVersion{version}, ExecutionPlan: planJSON,
		Status: "failed", Stage: "pushing_tag", CommitSHA: sha}
	if err := svc.store.CreateReleaseRun(run); err != nil {
		t.Fatal(err)
	}
	probe := &pushDeadlineProbe{delegate: execRunner{}}
	svc.runner = probe
	if _, err := svc.Retry(run.ID, RetryRequest{ExternalActionsConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	completed := waitRelease(t, svc, run.ID)
	if completed.Status != "succeeded" {
		t.Fatalf("retry failed: %+v", completed)
	}
	remaining, missing := probe.snapshot()
	if missing || len(remaining) != 1 {
		t.Fatalf("push deadlines missing=%t remaining=%v", missing, remaining)
	}
	if remaining[0] <= time.Minute || remaining[0] > releasePushTimeout+5*time.Second {
		t.Fatalf("unexpected push deadline: %s", remaining[0])
	}
}

type pushDeadlineProbe struct {
	delegate  commandRunner
	mu        sync.Mutex
	remaining []time.Duration
	missing   bool
}

func (p *pushDeadlineProbe) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" && contains(args, "push") {
		p.mu.Lock()
		if deadline, ok := ctx.Deadline(); ok {
			p.remaining = append(p.remaining, time.Until(deadline))
		} else {
			p.missing = true
		}
		p.mu.Unlock()
	}
	return p.delegate.Run(ctx, dir, name, args...)
}

func (p *pushDeadlineProbe) snapshot() ([]time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Duration{}, p.remaining...), p.missing
}

func TestPackageLockVersionUpdatesOnlyRootPackage(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "package-lock.json")
	raw := []byte(`{
  "name": "demo",
  "version": "1.2.3",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "demo", "version": "1.2.3"},
    "node_modules/dep": {"version": "9.8.7"}
  }
}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	updated, ok := replaceVersionBytesForPath(path, raw, "2.0.0")
	if !ok || strings.Count(string(updated), `"version": "2.0.0"`) != 2 || !strings.Contains(string(updated), `"version": "9.8.7"`) {
		t.Fatalf("unexpected package-lock update: %s", updated)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readNpmLockVersion(path); got != "2.0.0" {
		t.Fatalf("package-lock version = %q", got)
	}
}

func TestUnreleasedV2UsesCurrentVersionOnce(t *testing.T) {
	if got := suggestReleaseVersion([]string{"2.0.0", "2.0.0"}, "v0.1.27"); got != "2.0.0" {
		t.Fatalf("first v2 suggestion = %s", got)
	}
	if err := validateReleaseVersion("2.0.0", []string{"2.0.0", "2.0.0"}, "v0.1.27"); err != nil {
		t.Fatalf("first v2 validation: %v", err)
	}
	if got := suggestReleaseVersion([]string{"2.0.0"}, "v2.0.0"); got != "2.0.1" {
		t.Fatalf("post-tag suggestion = %s", got)
	}
	if err := validateReleaseVersion("2.0.0", []string{"2.0.0"}, "v2.0.0"); err == nil {
		t.Fatal("existing v2.0.0 was accepted")
	}
}
