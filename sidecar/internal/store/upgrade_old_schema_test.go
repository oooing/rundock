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
