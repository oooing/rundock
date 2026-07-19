package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/logbus"
	"github.com/launcher-sidecar/internal/security"
	"github.com/launcher-sidecar/internal/store"
)

// newTestLauncher 构造一个仅依赖 store 的 Launcher（不真正启动进程）。
// preflight 只用到 Store + Registry，不依赖 Manager/Hub 的运行态。
func newTestLauncher(t *testing.T, dbPath string) (*Launcher, *store.Store) {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg := adapter.NewRegistry()
	reg.Register(adapter.BatchAdapter{})
	mgr := app.NewManager(s)
	hub := logbus.NewHub()
	l := New(s, mgr, hub, reg)
	return l, s
}

// writeScript 写一个脚本文件（覆盖式），返回路径。
func writeScript(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
}

// seedApp 写脚本并创建 App（带初始 hash + 用户字段），返回 app id。
func seedApp(t *testing.T, s *store.Store, dir, name, script string) (appID, scriptPath, hash string) {
	t.Helper()
	scriptPath = filepath.Join(dir, name)
	writeScript(t, scriptPath, script)
	hash = hashOfFile(t, scriptPath)
	manual := "my-group"
	if err := s.CreateApp(&store.App{
		ID:            "app-1",
		Name:          "用户起的名",
		EntryScript:   scriptPath,
		Cwd:           dir,
		AdapterType:   "batch",
		Cmd:           "",
		Args:          []string{},
		Env:           map[string]string{},
		Tags:          []string{"用户标签"},
		GroupID:       &manual,
		PortHints:     []int{},
		HealthURL:     "http://localhost:9999/health",
		ScriptHash:    hash,
		Confirmed:     true,
		ConfirmedHash: hash,
		SortOrder:     7,
	}); err != nil {
		t.Fatalf("create app: %v", err)
	}
	return "app-1", scriptPath, hash
}

// TestPreflight_HashUnchanged 哈希未变 → Pass，App 不被改动。
func TestPreflight_HashUnchanged(t *testing.T) {
	dir := t.TempDir()
	l, s := newTestLauncher(t, filepath.Join(dir, "t.db"))
	_, _, hash := seedApp(t, s, dir, "start.bat", "echo hello\n")

	res, err := l.Preflight("app-1", "")
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Outcome != PreflightPass {
		t.Fatalf("outcome=%v, want Pass", res.Outcome)
	}
	if res.ConfigUpdated {
		t.Fatalf("ConfigUpdated should be false on Pass")
	}
	got, _ := s.GetApp("app-1")
	if got.ScriptHash != hash {
		t.Fatalf("hash changed unexpectedly: %q vs %q", got.ScriptHash, hash)
	}
}

// TestPreflight_SafeSync 哈希变化但仅 info/warn → Synced，派生字段更新，用户字段保留。
func TestPreflight_SafeSync(t *testing.T) {
	dir := t.TempDir()
	l, s := newTestLauncher(t, filepath.Join(dir, "t.db"))
	appID, scriptPath, _ := seedApp(t, s, dir, "start.bat", "echo hello\n")

	// 改脚本：加一个 warn（setx）和一个端口提示（3000），无 danger
	newScript := "echo changed\nsetx FOO bar\nset PORT=3000\n"
	writeScript(t, scriptPath, newScript)

	res, err := l.Preflight(appID, "")
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Outcome != PreflightSynced {
		t.Fatalf("outcome=%v, want Synced", res.Outcome)
	}
	if !res.ConfigUpdated {
		t.Fatalf("ConfigUpdated should be true on Synced")
	}
	got, _ := s.GetApp(appID)
	wantHash := hashOfFile(t, scriptPath)
	if got.ScriptHash != wantHash {
		t.Fatalf("hash not synced: %q vs %q", got.ScriptHash, wantHash)
	}
	// 派生字段：portHints 应包含 3000
	if !containsInt(got.PortHints, 3000) {
		t.Fatalf("portHints not synced: %v", got.PortHints)
	}
	// 用户字段保留
	if got.Name != "用户起的名" {
		t.Fatalf("name not preserved: %q", got.Name)
	}
	if got.HealthURL != "http://localhost:9999/health" {
		t.Fatalf("healthUrl not preserved: %q", got.HealthURL)
	}
	if got.SortOrder != 7 {
		t.Fatalf("sortOrder not preserved: %d", got.SortOrder)
	}
	if !containsStr(got.Tags, "用户标签") {
		t.Fatalf("tags not preserved: %v", got.Tags)
	}
	if got.GroupID == nil || *got.GroupID != "my-group" {
		t.Fatalf("groupId not preserved: %v", got.GroupID)
	}
}

// TestPreflight_DangerBlocks 哈希变化且含 danger → Confirm，不写库，返回 candidate。
func TestPreflight_DangerBlocks(t *testing.T) {
	dir := t.TempDir()
	l, s := newTestLauncher(t, filepath.Join(dir, "t.db"))
	appID, scriptPath, originalHash := seedApp(t, s, dir, "start.bat", "echo hello\n")

	// 改脚本：加 danger（reg add）
	writeScript(t, scriptPath, "echo changed\nreg add HKLM\\Software\\X\n")

	res, err := l.Preflight(appID, "")
	if err != nil {
		t.Fatalf("prefflight: %v", err)
	}
	if res.Outcome != PreflightConfirm {
		t.Fatalf("outcome=%v, want Confirm", res.Outcome)
	}
	if res.Candidate == nil {
		t.Fatalf("Candidate should be set on Confirm")
	}
	// 关键：danger 不写库 —— hash 应仍是原值
	got, _ := s.GetApp(appID)
	if got.ScriptHash != originalHash {
		t.Fatalf("danger should not persist: hash=%q, want %q", got.ScriptHash, originalHash)
	}
}

// TestPreflight_ConfirmHashMatch 用户回带 confirmedScriptHash 与当前文件哈希一致 → 通过并同步。
func TestPreflight_ConfirmHashMatch(t *testing.T) {
	dir := t.TempDir()
	l, s := newTestLauncher(t, filepath.Join(dir, "t.db"))
	appID, scriptPath, _ := seedApp(t, s, dir, "start.bat", "echo hello\n")

	// 改脚本到 danger
	writeScript(t, scriptPath, "echo changed\nreg add HKLM\\Software\\X\n")
	currentHash := hashOfFile(t, scriptPath)

	// 第一次：未带 hash → Confirm
	res, err := l.Preflight(appID, "")
	if err != nil || res.Outcome != PreflightConfirm {
		t.Fatalf("first preflight: err=%v outcome=%v", err, res.Outcome)
	}

	// 第二次：用户回带匹配 hash → 通过，库被同步，ConfirmedHash 也更新
	res, err = l.Preflight(appID, currentHash)
	if err != nil {
		t.Fatalf("preflight with hash: %v", err)
	}
	if res.Outcome != PreflightPass {
		t.Fatalf("outcome=%v, want Pass (confirmed)", res.Outcome)
	}
	if !res.ConfigUpdated {
		t.Fatalf("ConfigUpdated should be true after confirm")
	}
	got, _ := s.GetApp(appID)
	if got.ScriptHash != currentHash {
		t.Fatalf("hash not synced after confirm: %q", got.ScriptHash)
	}
	if got.ConfirmedHash != currentHash {
		t.Fatalf("ConfirmedHash not updated: %q", got.ConfirmedHash)
	}
	if !got.Confirmed {
		t.Fatalf("Confirmed should be true after confirm")
	}
}

// TestPreflight_ConfirmHashStale 用户回带的 hash 已过时（确认期间脚本又变）→ 再次 Confirm。
func TestPreflight_ConfirmHashStale(t *testing.T) {
	dir := t.TempDir()
	l, s := newTestLauncher(t, filepath.Join(dir, "t.db"))
	appID, scriptPath, _ := seedApp(t, s, dir, "start.bat", "echo hello\n")

	// 第一次变更到 danger
	writeScript(t, scriptPath, "echo v1\nreg add HKLM\\Software\\X\n")
	firstHash := hashOfFile(t, scriptPath)

	// 用户基于 firstHash 确认，但确认前脚本又变了（仍是 danger）
	writeScript(t, scriptPath, "echo v2\nreg add HKLM\\Software\\Y\n")
	secondHash := hashOfFile(t, scriptPath)

	res, err := l.Preflight(appID, firstHash)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if res.Outcome != PreflightConfirm {
		t.Fatalf("outcome=%v, want Confirm (stale hash)", res.Outcome)
	}
	if res.Candidate == nil || res.Candidate.ScriptHash != secondHash {
		t.Fatalf("candidate should carry latest hash %q, got %+v", secondHash, res.Candidate)
	}
}

// TestPreflight_MissingFile 文件不存在 → error，保留旧配置。
func TestPreflight_MissingFile(t *testing.T) {
	dir := t.TempDir()
	l, s := newTestLauncher(t, filepath.Join(dir, "t.db"))
	appID, scriptPath, originalHash := seedApp(t, s, dir, "start.bat", "echo hello\n")

	// 删除脚本文件
	if err := os.Remove(scriptPath); err != nil {
		t.Fatal(err)
	}

	_, err := l.Preflight(appID, "")
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
	// 旧配置保留
	got, _ := s.GetApp(appID)
	if got.ScriptHash != originalHash {
		t.Fatalf("config should be preserved on missing file: %q", got.ScriptHash)
	}
}

// TestPreflight_AppNotFound 不存在的 app → error。
func TestPreflight_AppNotFound(t *testing.T) {
	dir := t.TempDir()
	l, _ := newTestLauncher(t, filepath.Join(dir, "t.db"))

	_, err := l.Preflight("nope", "")
	if err == nil {
		t.Fatalf("expected error for missing app")
	}
}

// TestPreflight_ManualServiceRolePreserved 自动同步派生字段时，手动标注的服务角色保留。
func TestPreflight_ManualServiceRolePreserved(t *testing.T) {
	dir := t.TempDir()
	l, s := newTestLauncher(t, filepath.Join(dir, "t.db"))
	appID, scriptPath, _ := seedApp(t, s, dir, "start.bat", "echo hello\n")

	// 放一个手动标注的服务（端口 3000，role=database, manual）
	if err := s.UpsertService(&store.AppService{
		ID: "svc-1", AppID: appID, AppRunID: "old-run", Port: 3000,
		URL: "http://localhost:3000", Health: "healthy",
		Role: store.RoleDatabase, RoleSource: store.RoleSourceManual,
	}); err != nil {
		t.Fatal(err)
	}

	// 改脚本（仅 warn，触发 sync）
	writeScript(t, scriptPath, "echo changed\nsetx FOO bar\nset PORT=3000\n")
	if _, err := l.Preflight(appID, ""); err != nil {
		t.Fatalf("preflight: %v", err)
	}

	got, _ := s.GetService("svc-1")
	if got == nil {
		t.Fatalf("manual service should not be deleted on sync")
	}
	if got.Role != store.RoleDatabase || got.RoleSource != store.RoleSourceManual {
		t.Fatalf("manual role not preserved: %q/%q", got.Role, got.RoleSource)
	}
}

// ----- 小工具 -----

// hashOfFile 算文件 sha256，失败 t.Fatal。
func hashOfFile(t *testing.T, path string) string {
	t.Helper()
	h, err := security.HashFile(path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return h
}

func containsInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
