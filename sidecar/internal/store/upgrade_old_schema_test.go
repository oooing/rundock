package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// 验证老库兼容：手动建一个缺 role/role_source 列的 app_services，再 Open 升级。
func TestUpgradeFromOldSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "oldapp.db")

	// 先用裸 sql 建"旧版"表（模拟升级前的库）
	odb, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = odb.Exec(`CREATE TABLE app_services (
		id TEXT PRIMARY KEY, app_id TEXT NOT NULL, app_run_id TEXT NOT NULL,
		port INTEGER NOT NULL, url TEXT NOT NULL DEFAULT '',
		health TEXT NOT NULL DEFAULT 'unknown', last_checked TEXT,
		detected_at TEXT NOT NULL DEFAULT (datetime('now')))`)
	if err != nil {
		t.Fatal(err)
	}
	// 插一条旧数据（无 role）
	_, err = odb.Exec(`INSERT INTO app_services (id,app_id,app_run_id,port,url,health)
		VALUES ('old1','app1','run1',5173,'http://localhost:5173','healthy')`)
	if err != nil {
		t.Fatal(err)
	}
	odb.Close()

	// 现在用 store.Open 升级（migrate 幂等 + ensureSchema 补列）
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open on old DB should succeed via ensureSchema, got: %v", err)
	}
	defer s.Close()

	// 老数据应能读回，且 role 默认为 unknown / role_source 默认为 auto
	got, err := s.GetService("old1")
	if err != nil || got == nil {
		t.Fatalf("read old service: %v %v", got, err)
	}
	if got.Role != RoleUnknown {
		t.Errorf("old service role should default to unknown, got %q", got.Role)
	}
	if got.RoleSource != RoleSourceAuto {
		t.Errorf("old service roleSource should default to auto, got %q", got.RoleSource)
	}
}

// 验证老库兼容：手动建一个缺 card_color 列的 apps 表，再 Open 升级。
func TestUpgradeAddsAppCardColor(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "oldapps.db")

	// 先用裸 sql 建"旧版" apps 表（不含 card_color）
	odb, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = odb.Exec(`CREATE TABLE apps (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, entry_script TEXT NOT NULL, cwd TEXT NOT NULL,
		adapter_type TEXT NOT NULL DEFAULT 'batch', cmd TEXT NOT NULL DEFAULT '',
		args_json TEXT NOT NULL DEFAULT '[]', env_json TEXT NOT NULL DEFAULT '{}',
		tags_json TEXT NOT NULL DEFAULT '[]', group_id TEXT, port_hints_json TEXT NOT NULL DEFAULT '[]',
		health_url TEXT NOT NULL DEFAULT '', script_hash TEXT NOT NULL DEFAULT '',
		confirmed INTEGER NOT NULL DEFAULT 0, confirmed_hash TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')), last_started_at TEXT,
		last_url TEXT, last_status TEXT NOT NULL DEFAULT 'stopped', sort_order INTEGER NOT NULL DEFAULT 0)`)
	if err != nil {
		t.Fatal(err)
	}
	// 插一条旧数据
	_, err = odb.Exec(`INSERT INTO apps (id,name,entry_script,cwd) VALUES ('app1','old app','C:\\x.bat','C:\\')`)
	if err != nil {
		t.Fatal(err)
	}
	odb.Close()

	// 现在用 store.Open 升级（migrate 幂等 + ensureSchema 补 card_color 列）
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open on old DB should succeed via ensureSchema, got: %v", err)
	}
	defer s.Close()

	// 读回旧数据，card_color 应为空串
	got, err := s.GetApp("app1")
	if err != nil {
		t.Fatalf("GetApp(app1): %v", err)
	}
	if got == nil {
		t.Fatal("GetApp(app1) returned nil")
	}
	if got.CardColor != "" {
		t.Errorf("old app cardColor should default to empty, got %q", got.CardColor)
	}

	// 设置 card_color 后 UpdateApp，再次读回应一致
	got.CardColor = "#123456"
	if err := s.UpdateApp(got); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	again, err := s.GetApp("app1")
	if err != nil || again == nil {
		t.Fatalf("GetApp(app1) again: %v %v", again, err)
	}
	if again.CardColor != "#123456" {
		t.Errorf("after update, cardColor = %q, want #123456", again.CardColor)
	}
}
