package store

import (
	"path/filepath"
	"testing"
)

// ensureSchema 必须幂等:对已有 role/role_source 列的库再跑一次不报错。
func TestEnsureSchemaIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// 第一次打开:migrate 建表 + ensureSchema 加 role/role_source 列
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()

	// 第二次打开:列已存在,ensureSchema 不应报错
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second open (idempotent): %v", err)
	}
	defer s2.Close()

	// 验证列确实存在:插入一条 service 并读回 role
	svc := &AppService{
		ID: "svc-1", AppID: "app-1", AppRunID: "run-1", Port: 5432,
		Role: RoleDatabase, RoleSource: RoleSourceAuto,
	}
	if err := s2.UpsertService(svc); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s2.GetService("svc-1")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Role != RoleDatabase || got.RoleSource != RoleSourceAuto {
		t.Errorf("role/source = %q/%q, want database/auto", got.Role, got.RoleSource)
	}
}

// SetServiceRole 必须置 role_source=manual,UpdateServiceRoleIfAuto 只改 auto 的。
func TestRoleUpdateMethods(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// auto 初始
	svc := &AppService{ID: "s1", AppID: "a", AppRunID: "r", Port: 3000, Role: RoleUnknown, RoleSource: RoleSourceAuto}
	if err := s.UpsertService(svc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// IfAuto 升级:应成功且 updated=true
	updated, err := s.UpdateServiceRoleIfAuto("s1", RoleBackend)
	if err != nil || !updated {
		t.Fatalf("UpdateServiceRoleIfAuto: updated=%v err=%v", updated, err)
	}
	g, _ := s.GetService("s1")
	if g.Role != RoleBackend || g.RoleSource != RoleSourceAuto {
		t.Errorf("after IfAuto: %q/%q", g.Role, g.RoleSource)
	}
	// 相同值再升级:应 updated=false(无变化)
	updated, _ = s.UpdateServiceRoleIfAuto("s1", RoleBackend)
	if updated {
		t.Errorf("no-op upgrade should report updated=false")
	}

	// 手动锁定
	if err := s.SetServiceRole("s1", RoleFrontend); err != nil {
		t.Fatalf("SetServiceRole: %v", err)
	}
	g, _ = s.GetService("s1")
	if g.Role != RoleFrontend || g.RoleSource != RoleSourceManual {
		t.Errorf("after Set: %q/%q", g.Role, g.RoleSource)
	}

	// 锁定后 IfAuto 不应再改(manual 守卫 => updated=false)
	updated, _ = s.UpdateServiceRoleIfAuto("s1", RoleDatabase)
	if updated {
		t.Errorf("manual locked, IfAuto should report updated=false")
	}
	g, _ = s.GetService("s1")
	if g.Role != RoleFrontend {
		t.Errorf("manual locked, role should stay frontend, got %q", g.Role)
	}
}
