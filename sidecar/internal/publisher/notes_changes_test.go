package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepareNoteDirectories(t *testing.T, repo string) {
	t.Helper()
	for _, dir := range []string{"src/components", "docs"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDraftNotesUsesSelectedCodeAtCurrentTag(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	prepareNoteDirectories(t, repo)
	writeTestFile(t, filepath.Join(repo, "src/components/ReleaseModal.vue"), "<script>const message = 'old'</script>\n")
	writeTestFile(t, filepath.Join(repo, "src/player.ts"), "export const player = true\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "777")
	runGit(t, repo, "tag", "v1.0.0")
	writeTestFile(t, filepath.Join(repo, "src/components/ReleaseModal.vue"), "<script>const retrying = false; const releaseNotes = ''</script>\n")
	writeTestFile(t, filepath.Join(repo, "src/login.ts"), "export const token = 'secret-do-not-copy'\n")
	writeTestFile(t, filepath.Join(repo, "src/download.ts"), "export const download = true\n")
	if err := os.Remove(filepath.Join(repo, "src/player.ts")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "src/components/ReleaseModal.vue")
	before := runGit(t, repo, "status", "--porcelain=v1")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.DraftReleaseNotes(context.Background(), "app1", NotesDraftRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"src/components/ReleaseModal.vue", "src/login.ts", "src/player.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"调整更新说明的生成与编辑", "调整发布失败后的重试流程与提示", "调整登录与账号验证流程", "调整视频播放相关功能"} {
		if !strings.Contains(draft.Text, want) {
			t.Fatalf("missing %q in %s", want, draft.Text)
		}
	}
	for _, unwanted := range []string{"下载", "secret", "src/", ".vue", "新增", "性能优化"} {
		if strings.Contains(draft.Text, unwanted) {
			t.Fatalf("unexpected %q in %s", unwanted, draft.Text)
		}
	}
	if after := runGit(t, repo, "status", "--porcelain=v1"); before != after {
		t.Fatalf("draft changed Git state")
	}
	t.Log(draft.Text)
}

func TestDraftNotesUsesGenericCommittedChangesAndMaintenanceFallback(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	prepareNoteDirectories(t, repo)
	runGit(t, repo, "tag", "v1.0.0")
	writeTestFile(t, filepath.Join(repo, "src/settings.ts"), "export const theme = 'dark'\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "888")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.DraftReleaseNotes(context.Background(), "app1", NotesDraftRequest{StatusFingerprint: pf.StatusFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Text, "调整设置选项与操作") {
		t.Fatal(draft.Text)
	}
	runGit(t, repo, "tag", "v1.0.1")
	writeTestFile(t, filepath.Join(repo, "docs/player.md"), "Documentation only\n")
	pf, err = svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	draft, err = svc.DraftReleaseNotes(context.Background(), "app1", NotesDraftRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"docs/player.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Text, "内部实现与项目维护") || strings.Contains(draft.Text, "视频播放") {
		t.Fatal(draft.Text)
	}
	draft, err = svc.DraftReleaseNotes(context.Background(), "app1", NotesDraftRequest{StatusFingerprint: pf.StatusFingerprint})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.Text, "未检测到代码变化") {
		t.Fatal(draft.Text)
	}
}

func TestChangeNotesDeduplicatesAndLimitsAcrossCategories(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	prepareNoteDirectories(t, repo)
	runGit(t, repo, "tag", "v1.0.0")
	paths := []string{"src/login.ts", "src/auth.ts", "src/download.ts", "src/search.ts", "src/player.ts", "src/settings.ts", "src/subtitles.ts"}
	for _, name := range paths {
		writeTestFile(t, filepath.Join(repo, name), "export const changed = true\n")
	}
	runGit(t, repo, "commit", "--allow-empty", "-m", "fix: 修复启动失败")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := svc.DraftReleaseNotes(context.Background(), "app1", NotesDraftRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(draft.Text, "\n- ") != 5 || strings.Count(draft.Text, "登录与账号验证") != 1 || !strings.Contains(draft.Text, "修复启动失败") {
		t.Fatal(draft.Text)
	}
}
