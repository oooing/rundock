# 服务角色自动识别 + 手动修正 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为每个 `AppService`(项目下监听端口)自动识别角色(frontend/backend/database/unknown),识别错误时用户可手动修正。

**Architecture:** 新增纯函数识别模块 `probe/classify.go`(无副作用,可单测),信号优先级=DB端口(高)>HTTP响应头(高)>Title/Content-Type(中)>日志特征(低)。`AppService` 加 `role`/`roleSource` 两字段,用 `ensureSchema`(Go 层 PRAGMA 检测 + ALTER ADD COLUMN,绕开 SQLite ADD COLUMN 不幂等的问题)加列。在 `launcher.discoverServices`(发现时初判+异步 probe 升级)和 `recheckAndAggregate`(健康复查时升级)接线。新增 2 个 REST 端点(PATCH 改角色 / POST 重新识别)+ 1 个 WS 事件。前端 `AppCard.vue` 服务行加角色图标 + 点击切换菜单。

**Tech Stack:** Go 1.23 (sidecar, modernc.org/sqlite) / Vue 3 + Pinia + TypeScript (frontend)

**Spec:** `docs/superpowers/specs/2026-06-28-service-role-detection-design.md`

---

## 文件结构

| 文件 | 责任 | 动作 |
|---|---|---|
| `code/sidecar/internal/probe/classify.go` | 角色→置信度纯函数判定 | **新建** |
| `code/sidecar/internal/probe/classify_test.go` | classify 表驱动单测 | **新建** |
| `code/sidecar/internal/store/store.go` | `Open` 中加 `ensureSchema()` 调用 + 新增 `ensureSchema` 方法 | **修改** |
| `code/sidecar/internal/store/models.go` | `AppService` 加 `Role`/`RoleSource` 字段 + CRUD SQL 更新 + 新增 2 个 role 方法 | **修改** |
| `code/sidecar/internal/launcher/launcher.go` | `discoverServices`/`recheckAndAggregate` 接线 classify | **修改** |
| `code/sidecar/internal/api/handlers_ops.go` | 新增 `handleServiceRole`/`handleServiceReidentify` | **修改** |
| `code/sidecar/internal/api/handlers_core.go` | `handleAppDetail` 路由分发 services 子路径 | **修改** |
| `code/src/types/index.ts` | `AppService` 加 `role`/`roleSource` | **修改** |
| `code/src/api/http.ts` | 加 `setServiceRole`/`reidentifyService` | **修改** |
| `code/src/stores/apps.ts` | 加 `setServiceRole` action + WS `app:services` 已支持 | **修改** |
| `code/src/components/AppCard.vue` | 服务行加角色图标 + 点击切换菜单 | **修改** |

---

## Task 1: classify 纯函数 + 单测(核心)

**Files:**
- Create: `code/sidecar/internal/probe/classify.go`
- Test: `code/sidecar/internal/probe/classify_test.go`

- [ ] **Step 1: 写失败测试 `classify_test.go`**

```go
package probe

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		in   ClassifyInput
		wantRole Role
		wantConf Confidence
	}{
		// --- DB 端口(高,短路,优先于一切)---
		{"db port 5432", ClassifyInput{Port: 5432}, RoleDatabase, ConfHigh},
		{"db port 6379 redis", ClassifyInput{Port: 6379}, RoleDatabase, ConfHigh},
		{"db port 3306 mysql", ClassifyInput{Port: 3306}, RoleDatabase, ConfHigh},
		{"db port 27017 mongo", ClassifyInput{Port: 27017}, RoleDatabase, ConfHigh},
		{"db port overrides vite header", ClassifyInput{Port: 5432, Headers: map[string]string{"server": "vite"}}, RoleDatabase, ConfHigh},

		// --- 响应头(高)---
		{"header vite", ClassifyInput{Headers: map[string]string{"server": "vite"}}, RoleFrontend, ConfHigh},
		{"header x-powered-by express", ClassifyInput{Headers: map[string]string{"x-powered-by": "Express"}}, RoleBackend, ConfHigh},
		{"header server uvicorn", ClassifyInput{Headers: map[string]string{"server": "uvicorn"}}, RoleBackend, ConfHigh},
		{"header next.js -> frontend", ClassifyInput{Headers: map[string]string{"x-powered-by": "Next.js"}}, RoleFrontend, ConfHigh},

		// --- Title/Content-Type(中)---
		{"title vite+react no header", ClassifyInput{Title: "Vite + React"}, RoleFrontend, ConfMedium},
		{"content-type json no header", ClassifyInput{BodyCT: "application/json"}, RoleBackend, ConfMedium},

		// --- 日志(低)---
		{"log vite version", ClassifyInput{LogHints: []string{"VITE v5.0.0 ready in 312 ms"}}, RoleFrontend, ConfLow},
		{"log uvicorn running", ClassifyInput{LogHints: []string{"INFO:     Uvicorn running on http://127.0.0.1:8000"}}, RoleBackend, ConfLow},

		// --- 都不命中 ---
		{"empty input", ClassifyInput{}, RoleUnknown, ConfNone},
		{"random port no signals", ClassifyInput{Port: 12345}, RoleUnknown, ConfNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotRole, gotConf := Classify(c.in)
			if gotRole != c.wantRole {
				t.Errorf("role = %q, want %q", gotRole, c.wantRole)
			}
			if gotConf != c.wantConf {
				t.Errorf("conf = %v, want %v", gotConf, c.wantConf)
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd code/sidecar && go test ./internal/probe/ -run TestClassify -v`
Expected: 编译失败 `undefined: Role, ClassifyInput, Classify, Confidence ...`

- [ ] **Step 3: 实现 `classify.go`**

```go
package probe

import "strings"

// Role 服务角色。
type Role string

const (
	RoleUnknown  Role = "unknown"
	RoleFrontend Role = "frontend"
	RoleBackend  Role = "backend"
	RoleDatabase Role = "database"
)

// Confidence 识别置信度。高的覆盖低的。
type Confidence int

const (
	ConfNone   Confidence = iota // 无信号
	ConfLow                      // 日志特征
	ConfMedium                   // title / content-type
	ConfHigh                     // 响应头 / DB 端口
)

// ClassifyInput 识别输入。所有字段可选(零值即无该信号)。
type ClassifyInput struct {
	Port     int               // 监听端口
	Headers  map[string]string // HTTP 响应头(键已小写),可空
	Title    string            // HTML <title>,可空
	BodyCT   string            // Content-Type,可空
	LogHints []string          // 命中框架特征的日志片段,可空
}

// 标准 DB 端口(高置信,直接判 database)。
var dbPorts = map[int]bool{
	5432: true,  // PostgreSQL
	3306: true,  // MySQL
	6379: true,  // Redis
	27017: true, // MongoDB
	1433: true,  // SQL Server
	8529: true,  // Dgraph
	9092: true,  // Kafka
	9200: true,  // Elasticsearch
	11211: true, // Memcached
}

// 响应头/Title 关键词表。小写匹配。
var frontendHeaderKW = []string{"vite", "webpack", "next", "nuxt"}
var backendHeaderKW = []string{"express", "fastapi", "uvicorn", "gunicorn", "php", "kestrel", "django", "flask", "gin", "koa"}
var frontendTitleKW = []string{"vite", "react app", "vue", "angular", "nuxt"}

// 日志特征正则片段(用 Contains 子串,大小写不敏感)。
var frontendLogKW = []string{"vite v", "webpack compiled", "ready in", "local: http"}
var backendLogKW = []string{"uvicorn running", "started server on", "listening on", "django version", "flask", "gin-debug"}

// Classify 纯函数:按置信度从高到低短路返回。
func Classify(in ClassifyInput) (Role, Confidence) {
	// 1. DB 端口(高)
	if in.Port > 0 && dbPorts[in.Port] {
		return RoleDatabase, ConfHigh
	}
	// 2. 响应头(高)
	if role, ok := matchHeaders(in.Headers); ok {
		return role, ConfHigh
	}
	// 3. Title / Content-Type(中)
	if role, ok := matchTitleOrCT(in.Title, in.BodyCT); ok {
		return role, ConfMedium
	}
	// 4. 日志(低)
	if role, ok := matchLogs(in.LogHints); ok {
		return role, ConfLow
	}
	return RoleUnknown, ConfNone
}

func matchHeaders(h map[string]string) (Role, bool) {
	if h == nil {
		return RoleUnknown, false
	}
	// 合并 server 与 x-powered-by 两处文本
	fields := []string{h["server"], h["x-powered-by"]}
	combined := strings.ToLower(strings.Join(fields, " "))
	if containsAny(combined, frontendHeaderKW) {
		return RoleFrontend, true
	}
	if containsAny(combined, backendHeaderKW) {
		return RoleBackend, true
	}
	return RoleUnknown, false
}

func matchTitleOrCT(title, ct string) (Role, bool) {
	lt := strings.ToLower(title)
	if containsAny(lt, frontendTitleKW) {
		return RoleFrontend, true
	}
	// 根路径返回 application/json 强烈提示 API
	if strings.Contains(strings.ToLower(ct), "application/json") {
		return RoleBackend, true
	}
	return RoleUnknown, false
}

func matchLogs(logs []string) (Role, bool) {
	if len(logs) == 0 {
		return RoleUnknown, false
	}
	combined := strings.ToLower(strings.Join(logs, " "))
	if containsAny(combined, frontendLogKW) {
		return RoleFrontend, true
	}
	if containsAny(combined, backendLogKW) {
		return RoleBackend, true
	}
	return RoleUnknown, false
}

// containsAny 任意子串命中即返回 true。
func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 运行测试,确认通过**

Run: `cd code/sidecar && go test ./internal/probe/ -run TestClassify -v`
Expected: 所有子测试 PASS

- [ ] **Step 5: 提交**

```bash
cd code/sidecar
git add internal/probe/classify.go internal/probe/classify_test.go
git commit -m "feat(probe): add classify() for service role detection (frontend/backend/database/unknown)"
```

---

## Task 2: store schema 演进 — ensureSchema + 模型字段

**Files:**
- Modify: `code/sidecar/internal/store/store.go`(加 `ensureSchema`,在 `Open` 中调用)
- Modify: `code/sidecar/internal/store/models.go`(`AppService` 加字段 + CRUD SQL + 2 个 role 方法)
- Test: `code/sidecar/internal/store/ensure_schema_test.go`(新建)

- [ ] **Step 1: 写失败测试 `ensure_schema_test.go`**

```go
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
		Role: string(RoleDatabase), RoleSource: "auto",
	}
	if err := s2.UpsertService(svc); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s2.GetService("svc-1")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Role != "database" || got.RoleSource != "auto" {
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
	svc := &AppService{ID: "s1", AppID: "a", AppRunID: "r", Port: 3000, Role: "unknown", RoleSource: "auto"}
	_ = s.UpsertService(svc)

	// IfAuto 升级:应成功
	if err := s.UpdateServiceRoleIfAuto("s1", "backend"); err != nil {
		t.Fatalf("UpdateServiceRoleIfAuto: %v", err)
	}
	g, _ := s.GetService("s1")
	if g.Role != "backend" || g.RoleSource != "auto" {
		t.Errorf("after IfAuto: %q/%q", g.Role, g.RoleSource)
	}

	// 手动锁定
	if err := s.SetServiceRole("s1", "frontend"); err != nil {
		t.Fatalf("SetServiceRole: %v", err)
	}
	g, _ = s.GetService("s1")
	if g.Role != "frontend" || g.RoleSource != "manual" {
		t.Errorf("after Set: %q/%q", g.Role, g.RoleSource)
	}

	// 锁定后 IfAuto 不应再改
	_ = s.UpdateServiceRoleIfAuto("s1", "database")
	g, _ = s.GetService("s1")
	if g.Role != "frontend" {
		t.Errorf("manual locked, IfAuto should be no-op, got %q", g.Role)
	}
}
```

> 注:测试引用了 `RoleDatabase`(Go 常量,值 `"database"`)与 `GetService`/`UpdateServiceRoleIfAuto`/`SetServiceRole`——都在本 Task 的 Step 3 实现。Step 2 先看到编译失败是预期的。

- [ ] **Step 2: 运行测试,确认失败**

Run: `cd code/sidecar && go test ./internal/store/ -run TestEnsureSchema -v`
Expected: 编译失败(`ensureSchema undefined` / `GetService undefined` / `RoleDatabase undefined` in store pkg)

- [ ] **Step 3: 修改 `store.go` — 加 `ensureSchema` + 在 `Open` 调用**

在 `store.go` 的 `Open` 函数中,`s.migrate()` 调用之后、`return s, nil` 之前插入:

```go
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := s.ensureSchema(); err != nil {        // 新增
		db.Close()                                    // 新增
		return nil, fmt.Errorf("ensure schema: %w", err) // 新增
	}                                                // 新增
	return s, nil
```

在 `store.go` 文件末尾新增 `ensureSchema` 方法(放在 `Close` 方法之后):

```go
// ensureSchema 处理 ALTER TABLE ADD COLUMN 的非幂等列(role/role_source)。
// SQLite 的 ADD COLUMN 列已存在时报 "duplicate column name",不能用 CREATE IF NOT EXISTS,
// 故用 PRAGMA table_info 先检测列是否已存在,缺哪列补哪列。每次 Open 调用一次,安全。
func (s *Store) ensureSchema() error {
	cols, err := s.tableColumns("app_services")
	if err != nil {
		return err
	}
	wanted := map[string]string{
		"role":        "TEXT NOT NULL DEFAULT 'unknown'",
		"role_source": "TEXT NOT NULL DEFAULT 'auto'",
	}
	for col, def := range wanted {
		if !cols[col] {
			if _, err := s.db.Exec("ALTER TABLE app_services ADD COLUMN " + col + " " + def); err != nil {
				return err
			}
		}
	}
	return nil
}

// tableColumns 返回某表已有的列名集合(键大写以便大小写不敏感比较)。
func (s *Store) tableColumns(table string) (map[string]bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToUpper(name)] = true
	}
	return cols, rows.Err()
}
```

同时给 `store.go` 的 import 块补 `database/sql`(若未有):确认 import 含 `"database/sql"`。当前 store.go 未导入 `database/sql`,需新增。在 import 块加入。

- [ ] **Step 4: 修改 `models.go` — `AppService` 加字段**

找到 `models.go` 中的 `AppService` 结构体(约 76-85 行),在 `DetectedAt` 之后加两字段:

```go
type AppService struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	AppRunID    string `json:"appRunId"`
	Port        int    `json:"port"`
	URL         string `json:"url"`
	Health      string `json:"health"`
	LastChecked string `json:"lastChecked"`
	DetectedAt  string `json:"detectedAt"`
	Role        string `json:"role"`       // 新增: frontend|backend|database|unknown
	RoleSource  string `json:"roleSource"` // 新增: auto|manual
}
```

- [ ] **Step 5: 修改 `models.go` — 加 role 常量 + 改 CRUD SQL**

在 `AppService` 结构体定义之后,新增 role 常量(供其它包引用,避免魔法字符串):

```go
// AppService 的 role 取值常量。
const (
	RoleFrontend = "frontend"
	RoleBackend  = "backend"
	RoleDatabase = "database"
	RoleUnknown  = "unknown"
)
// role_source 取值
const (
	RoleSourceAuto   = "auto"
	RoleSourceManual = "manual"
)
```

**改 `UpsertService`**(约 90-96 行):列列表与 VALUES 加 `role, role_source`。注意 `ON CONFLICT DO UPDATE` 只更新 health/last_checked/url,**不覆盖 role**(role 的更新走专门的 Set/Update 方法,避免 upsert 抹掉手动标注):

```go
func (s *Store) UpsertService(svc *AppService) error {
	_, err := s.db.Exec(`INSERT INTO app_services (id,app_id,app_run_id,port,url,health,last_checked,detected_at,role,role_source)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET health=excluded.health, last_checked=excluded.last_checked, url=excluded.url`,
		svc.ID, svc.AppID, svc.AppRunID, svc.Port, svc.URL, svc.Health,
		nullableStringEmpty(svc.LastChecked), svc.DetectedAt,
		defaultStr(svc.Role, RoleUnknown), defaultStr(svc.RoleSource, RoleSourceAuto))
	return err
}
```

> `defaultStr` 已在 api 包,store 包没有。在 models.go 末尾加一个本包私有 helper(避免跨包):

```go
// strDefault 空串返回 def(本包私有,不与 api 包的 defaultStr 冲突)。
func strDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
```
然后 UpsertService 里用 `strDefault(svc.Role, RoleUnknown)` / `strDefault(svc.RoleSource, RoleSourceAuto)`。

**改 `ListServicesByApp` / `ListServicesByRun` 的 SELECT**(约 114、125 行):列列表加 `role, role_source`,放在 `detected_at` 之后:

```go
rows, err := s.db.Query(`SELECT id,app_id,app_run_id,port,url,health,last_checked,detected_at,role,role_source
		FROM app_services WHERE app_id=? ORDER BY port ASC`, appID)
```
`ListServicesByRun` 同理(`WHERE app_run_id=?`)。

**改 `scanServices`**(约 135-149 行):加两个 Scan 目标字段:

```go
func scanServices(rows *sql.Rows) ([]*AppService, error) {
	var out []*AppService
	for rows.Next() {
		svc := &AppService{}
		var lastChecked sql.NullString
		if err := rows.Scan(&svc.ID, &svc.AppID, &svc.AppRunID, &svc.Port, &svc.URL, &svc.Health,
			&lastChecked, &svc.DetectedAt, &svc.Role, &svc.RoleSource); err != nil {
			return nil, err
		}
		if lastChecked.Valid {
			svc.LastChecked = lastChecked.String
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}
```

- [ ] **Step 6: 修改 `models.go` — 新增 3 个方法**

在 `scanServices` 之后(或 `DeleteServicesByApp` 之前)新增:

```go
// GetService 按 ID 查询单个服务。
func (s *Store) GetService(id string) (*AppService, error) {
	row := s.db.QueryRow(`SELECT id,app_id,app_run_id,port,url,health,last_checked,detected_at,role,role_source
		FROM app_services WHERE id=?`, id)
	svc := &AppService{}
	var lastChecked sql.NullString
	if err := row.Scan(&svc.ID, &svc.AppID, &svc.AppRunID, &svc.Port, &svc.URL, &svc.Health,
		&lastChecked, &svc.DetectedAt, &svc.Role, &svc.RoleSource); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if lastChecked.Valid {
		svc.LastChecked = lastChecked.String
	}
	return svc, nil
}

// SetServiceRole 手动设置角色:置 role 并锁定 role_source=manual(自动识别不再覆盖)。
func (s *Store) SetServiceRole(id, role string) error {
	_, err := s.db.Exec(`UPDATE app_services SET role=?, role_source=? WHERE id=?`,
		role, RoleSourceManual, id)
	return err
}

// UpdateServiceRoleIfAuto 仅当 role_source='auto' 时更新 role(自动识别升级用)。
func (s *Store) UpdateServiceRoleIfAuto(id, role string) error {
	_, err := s.db.Exec(`UPDATE app_services SET role=? WHERE id=? AND role_source='auto'`, role, id)
	return err
}

// ResetServiceRoleToAuto 强制重新识别:重置 role_source=auto(供 reidentify 端点用)。
// role 本身由调用方随后重新 classify 写入。
func (s *Store) ResetServiceRoleToAuto(id string) error {
	_, err := s.db.Exec(`UPDATE app_services SET role_source=? WHERE id=?`, RoleSourceAuto, id)
	return err
}
```

- [ ] **Step 7: 运行测试,确认通过**

Run: `cd code/sidecar && go test ./internal/store/ -v`
Expected: 所有测试 PASS(包括 `TestEnsureSchemaIdempotent` 和 `TestRoleUpdateMethods`)

- [ ] **Step 8: 全量编译,确认无回归**

Run: `cd code/sidecar && go build ./...`
Expected: 无报错

- [ ] **Step 9: 提交**

```bash
cd code/sidecar
git add internal/store/store.go internal/store/models.go internal/store/ensure_schema_test.go
git commit -m "feat(store): add role/roleSource to AppService via ensureSchema (idempotent ALTER), role update methods"
```

---

## Task 3: launcher 接线 — 发现时识别 + 健康复查时升级

**Files:**
- Modify: `code/sidecar/internal/launcher/launcher.go`(`discoverServices` 约 208-271、`recheckAndAggregate` 约 274+)

> 本 Task 不加新单测(launcher 依赖真实进程/端口,集成测试成本高;classify 的纯逻辑已由 Task 1 覆盖)。改为:Step 末用 `go build` + `go vet` 守门。

- [ ] **Step 1: 读懂现有接入点**

阅读 `launcher.go` 的 `discoverServices`(208-271)与 `recheckAndAggregate`(274 起至函数结束)。确认:
- `discoverServices` 在创建 `svc` 后调用 `UpsertService`——这是初判 role 的注入点。
- `recheckAndAggregate` 对每个 service 调用 `probeService(svc.URL)`——这是用响应头升级 role 的注入点。
- `probeService` 当前只返回 `bool`。需要让它返回 `*probe.HealthResult` 以拿到 Server/Title 头。先检查 `probeService` 实现。

Run: `cd code/sidecar && grep -n "func.*probeService" internal/launcher/*.go`

- [ ] **Step 2: 让 `probeService` 返回 `*probe.HealthResult`**

找到 `probeService` 定义(用 Step 1 的 grep 定位)。当前签名形如 `func (l *Launcher) probeService(url string) bool`,内部调用 `probe.CheckHealth`。改为返回 `*probe.HealthResult`,调用方按 `r != nil && r.Reachable` 判断:

```go
// probeService 探测一个 URL 是否可达,返回健康检查结果(含响应头/Title,供角色识别复用)。
func (l *Launcher) probeService(url string) *probe.HealthResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return probe.CheckHealth(ctx, url)
}
```

> 改完需更新所有 `probeService` 调用点:`ok := l.probeService(...)` → `r := l.probeService(...); ok := r != nil && r.Reachable`。在 `recheckAndAggregate` 里用 grep 找全部调用点逐一改。

Run: `cd code/sidecar && grep -n "probeService" internal/launcher/*.go` 逐一更新调用点。

确认 import 含 `"github.com/launcher-sidecar/internal/probe"`(launcher 包应已 import,因 `discoverServices` 用了 `probe.SnapshotListeners`)。补 `context`/`time` import 若缺。

- [ ] **Step 3: `discoverServices` 注入初判 role**

在 `discoverServices` 创建 `svc` 之后、`UpsertService` 之前(约 260 行附近),插入初判。`rs.candidateURLs` 是日志里出现的 URL,但 classify 需要"日志文本片段"。最简单且够用:用端口 + 日志 URL 列表(端口能判 DB,日志 URL 现阶段不直接喂 classify,留空)。

定位 `svc := &store.AppService{...}` 块(252-260 行),在 `DetectedAt: ...` 字段之后、闭合 `}` 之前加 role 字段,并在结构体之后立即初判:

```go
		url := probe.JoinHostPort("localhost", p.Port)
		// 初判角色:只用端口(DB 端口高置信) + 日志特征(若有)。
		_, conf := probe.Classify(probe.ClassifyInput{
			Port:     p.Port,
			LogHints: collectLogHints(l, appID),
		})
		role, _ := probe.Classify(probe.ClassifyInput{
			Port:     p.Port,
			LogHints: collectLogHints(l, appID),
		})
		svc := &store.AppService{
			ID:         app.NewID(),
			AppID:      appID,
			AppRunID:   rt.RunID,
			Port:       p.Port,
			URL:        url,
			Health:     "unknown",
			DetectedAt: time.Now().UTC().Format(time.RFC3339),
			Role:       string(role),
			RoleSource: store.RoleSourceAuto,
		}
		_ = l.Store.UpsertService(svc)
```

> 简化:上面两次 Classify 是冗余,实际只需一次拿 role+conf。写成:

```go
		role, conf := probe.Classify(probe.ClassifyInput{
			Port:     p.Port,
			LogHints: collectLogHints(l, appID),
		})
```
然后 `Role: string(role)`。`conf` 用于决定是否异步 probe 升级:conf < ConfHigh 时升级。

在 `UpsertService` 之后加异步 probe 升级(仅当置信度不足 High,即非 DB 端口、无响应头——但发现阶段还没做 HTTP 探测,所以 conf 基本是 Low 或 None,值得升级):

```go
		_ = l.Store.UpsertService(svc)
		_ = l.Store.InsertPort(rt.RunID, p.Port, "tcp")
		if l.Hub != nil {
			l.Hub.BroadcastURL(appID, url, []int{p.Port})
		}
		// 异步用 HTTP 响应头升级 role(仅当当前不是高置信)。
		if conf < probe.ConfHigh {
			go l.refineRoleWithProbe(appID, svc.ID, url)
		}
```

- [ ] **Step 4: 新增 helper `collectLogHints` 与 `refineRoleWithProbe`**

在 `launcher.go`(可放 `discover_helpers.go` 同包)新增:

```go
// collectLogHints 从 runState 收集近期日志片段(供 classify 的低置信日志信号)。
// 简化实现:把 candidateURLs 拼起来(URL 里有时含框架名,如 vite);正式日志全文代价高,留 TODO。
func collectLogHints(l *Launcher, appID string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	rs, ok := l.runs[appID]
	if !ok {
		return nil
	}
	// candidateURLs 可能含 "http://localhost:5173" 等;转为大写关键词供匹配意义不大,
	// 但保留接口:未来可扩展为读取 runState 上的日志缓冲。
	_ = rs
	return nil
}

// refineRoleWithProbe 用 HTTP 响应头/Title 重新 classify,仅在 role_source=auto 时升级。
func (l *Launcher) refineRoleWithProbe(appID, serviceID, url string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hr := probe.CheckHealth(ctx, url)
	if hr == nil {
		return
	}
	headers := map[string]string{}
	if hr.Server != "" {
		headers["server"] = hr.Server
	}
	role, conf := probe.Classify(probe.ClassifyInput{
		Headers: headers,
		Title:   hr.Title,
		BodyCT:  "", // HealthResult 当前未存 Content-Type,留空
	})
	if conf < probe.ConfMedium {
		return // 响应头/title 都没命中,不升级(保持 unknown 或原日志判定)
	}
	_ = l.Store.UpdateServiceRoleIfAuto(serviceID, string(role))
	// 广播服务状态刷新前端(复用 app:services)
	if svcs, err := l.Store.ListServicesByRun(latestRunID(l, appID)); err == nil && l.Hub != nil {
		l.Hub.BroadcastServices(appID, latestRunID(l, appID), svcs)
	}
}

// latestRunID 取某 app 当前 runID(用于广播)。
func latestRunID(l *Launcher, appID string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rs, ok := l.runs[appID]; ok {
		return rs.RunID
	}
	return ""
}
```

> **注意 `runState` 字段名**:上面用了 `rs.RunID`、`rs.candidateURLs`。先用 grep 确认 runState 结构体字段名:
> Run: `cd code/sidecar && grep -n "candidateURLs\|RunID\|type runState" internal/launcher/*.go`
> 按实际字段名调整。`discoverServices` 里已用 `rt.RunID`(来自 `*app.Runtime`)和 `rs.candidateURLs`,说明 runState 有 `candidateURLs` 字段;但 `rs.RunID` 是否存在需确认——若 runState 无 RunID 字段,改用传入的 `rt.RunID` 参数(refineRoleWithProbe 多加一个 runID 参数)。

- [ ] **Step 5: `recheckAndAggregate` 在健康复查成功时顺带升级 role**

定位 `recheckAndAggregate` 中调用 `probeService` 的位置(约 288 行 `ok := l.probeService(svc.URL)`)。改为拿到 `*HealthResult`,对 auto 且当前 role 为 unknown/低置信的服务用响应头升级:

```go
		hr := l.probeService(svc.URL)
		ok := hr != nil && hr.Reachable
		if ok {
			healthy++
			// 顺带:用响应头升级 auto 服务的角色(若当前还是 unknown)。
			if svc.RoleSource == store.RoleSourceAuto && svc.Role == store.RoleUnknown {
				headers := map[string]string{}
				if hr != nil && hr.Server != "" {
					headers["server"] = hr.Server
				}
				if role, conf := probe.Classify(probe.ClassifyInput{Headers: headers, Title: hr.Title}); conf >= probe.ConfMedium {
					_ = l.Store.UpdateServiceRoleIfAuto(svc.ID, string(role))
				}
			}
		} else {
			// ...原 unhealthy 逻辑
		}
```
> 只在 `svc.Role == unknown` 时升级,避免覆盖已有的日志判定(日志判定也是 auto,但已有值不该被同等置信度反复改写)。

- [ ] **Step 6: 编译 + vet 守门**

Run: `cd code/sidecar && go build ./... && go vet ./internal/launcher/`
Expected: 无报错。若有"unused import / undefined field",按 grep 结果修正字段名。

- [ ] **Step 7: 提交**

```bash
cd code/sidecar
git add internal/launcher/launcher.go internal/launcher/discover_helpers.go
git commit -m "feat(launcher): classify service role on discovery + upgrade via probe headers on recheck"
```

---

## Task 4: REST API — 手动改角色 + 重新识别

**Files:**
- Modify: `code/sidecar/internal/api/handlers_core.go`(`handleAppDetail` 路由分发,约 100-128)
- Modify: `code/sidecar/internal/api/handlers_ops.go`(新增 2 个 handler)

- [ ] **Step 1: 路由分发 `services/{sid}/role` 与 `services/{sid}/reidentify`**

当前 `handleAppDetail`(handlers_core.go:100-128)用 `pathTail` 拿到 `id` 与 `rest`,rest 是单段(如 `start`)。新端点 `services/{sid}/role` 是多段。改 switch,在 `rest` 以 `services/` 开头时二次切分:

定位 `switch rest {`(约 110 行),在 `default` 之前加 services 分支:

```go
	switch rest {
	case "start":
		s.handleStart(w, r, id)
	case "stop":
		s.handleStop(w, r, id)
	case "restart":
		s.handleRestart(w, r, id)
	case "logs":
		s.handleLogs(w, r, id)
	case "open-url":
		s.handleOpenURL(w, r, id)
	case "open-dir":
		s.handleOpenDir(w, r, id)
	case "ports":
		s.handlePorts(w, r, id)
	default:
		if sid, sub := pathTail("services/", rest); sid != "" {
			// rest 形如 "services/{sid}/role" -> pathTail("services/", rest) 得 sid + "role"
			switch sub {
			case "role":
				s.handleServiceRole(w, r, id, sid)
			case "reidentify":
				s.handleServiceReidentify(w, r, id, sid)
			default:
				writeError(w, http.StatusNotFound, "unknown service subpath: "+sub)
			}
			return
		}
		writeError(w, http.StatusNotFound, "unknown subpath: "+rest)
	}
```

> 注意:`pathTail("services/", rest)` 要求 rest 以 `services/` 开头。当前 rest 已 strip 了前导 `/`(pathTail 内部 TrimPrefix "/"),所以 rest = `services/{sid}/role`。`pathTail("services/", rest)` 会先 TrimPrefix `services/` 得 `{sid}/role`,再切分得 sid=`{sid}`, sub=`role`。正确。

- [ ] **Step 2: 新增 `handleServiceRole` 与 `handleServiceReidentify`**

在 `handlers_ops.go` 末尾(`openExternal` 函数之前)新增:

```go
// PATCH /api/apps/{id}/services/{sid}/role { role: "frontend" }
// 手动设置服务角色,锁定为 manual(自动识别不再覆盖)。
func (s *Server) handleServiceRole(w http.ResponseWriter, r *http.Request, appID, sid string) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	switch body.Role {
	case store.RoleFrontend, store.RoleBackend, store.RoleDatabase, store.RoleUnknown:
		// ok
	default:
		writeError(w, http.StatusBadRequest, "invalid role: "+body.Role)
		return
	}
	svc, err := s.Store.GetService(sid)
	if err != nil || svc == nil {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	if err := s.Store.SetServiceRole(sid, body.Role); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 广播刷新前端
	s.broadcastServices(appID)
	writeJSON(w, http.StatusOK, map[string]string{"role": body.Role, "roleSource": store.RoleSourceManual})
}

// POST /api/apps/{id}/services/{sid}/reidentify
// 强制重新识别:重置为 auto,用端口+HTTP 响应头重新 classify。
func (s *Server) handleServiceReidentify(w http.ResponseWriter, r *http.Request, appID, sid string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	svc, err := s.Store.GetService(sid)
	if err != nil || svc == nil {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	if err := s.Store.ResetServiceRoleToAuto(sid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 同步重新探测(简单起见同步做,不异步)
	headers := map[string]string{}
	if hr := probeHealth(svc.URL); hr != nil && hr.Server != "" {
		headers["server"] = hr.Server
	}
	role, _ := probe.Classify(probe.ClassifyInput{
		Port:    svc.Port,
		Headers: headers,
		Title:   titleFromHealth(svc.URL),
	})
	_ = s.Store.UpdateServiceRoleIfAuto(sid, string(role))
	s.broadcastServices(appID)
	writeJSON(w, http.StatusOK, map[string]string{"role": string(role), "roleSource": store.RoleSourceAuto})
}

// broadcastServices 广播某 app 的最新 services 列表(复用 app:services)。
func (s *Server) broadcastServices(appID string) {
	svcs, err := s.Store.ListServicesByApp(appID)
	if err != nil || s.Hub == nil {
		return
	}
	run, _ := s.Store.GetLatestRun(appID)
	runID := ""
	if run != nil {
		runID = run.ID
	}
	s.Hub.BroadcastServices(appID, runID, svcs)
}
```

> `probeHealth` / `titleFromHealth` 需 import probe 包并在 handler 里调用 `probe.CheckHealth`。简化:直接用 `probe.CheckHealth` 一次,Server/Title 都从同一结果取。重写 reidentify 内的探测段为:

```go
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hr := probe.CheckHealth(ctx, svc.URL)
	headers := map[string]string{}
	var title string
	if hr != nil {
		if hr.Server != "" {
			headers["server"] = hr.Server
		}
		title = hr.Title
	}
	role, _ := probe.Classify(probe.ClassifyInput{Port: svc.Port, Headers: headers, Title: title})
```
删除 `probeHealth`/`titleFromHealth` 这两个未定义的辅助函数引用。

- [ ] **Step 3: 给 handlers_ops.go 补 import**

确认 import 块含:
```go
	"context"
	"github.com/launcher-sidecar/internal/probe"
```
当前 handlers_ops.go import 了 `context`、`store`,**未** import `probe`。新增 `"github.com/launcher-sidecar/internal/probe"`。

- [ ] **Step 4: 编译**

Run: `cd code/sidecar && go build ./...`
Expected: 无报错

- [ ] **Step 5: 手测端点(可选,若无运行环境则跳过,留集成测)**

若无方便起的后端运行环境,此步可跳过,留给前端联调时一并验。编译通过即可进入下一 Task。

- [ ] **Step 6: 提交**

```bash
cd code/sidecar
git add internal/api/handlers_core.go internal/api/handlers_ops.go
git commit -m "feat(api): PATCH /services/{sid}/role (manual) + POST /services/{sid}/reidentify"
```

---

## Task 5: 前端类型 + API 客户端

**Files:**
- Modify: `code/src/types/index.ts`(`AppService` 接口)
- Modify: `code/src/api/http.ts`(加 2 个方法)

- [ ] **Step 1: `types/index.ts` 加字段 + role 类型**

找到 `AppService` interface(约文件末尾的 `/** 项目下的一个服务 */`),加 role/roleSource:

```ts
/** 服务角色 */
export type ServiceRole = 'frontend' | 'backend' | 'database' | 'unknown'

/** 项目下的一个服务（对应一个监听端口） */
export interface AppService {
  id: string
  appId: string
  appRunId: string
  port: number
  url: string
  health: 'healthy' | 'unhealthy' | 'unknown'
  lastChecked: string
  detectedAt: string
  role: ServiceRole       // 新增
  roleSource: 'auto' | 'manual' // 新增
}
```

- [ ] **Step 2: `api/http.ts` 加 2 个方法**

在 `http.ts` 的 `api` 对象里(ports 方法之后,分组之前)加:

```ts
  // 服务角色
  setServiceRole: (appId: string, serviceId: string, role: import('@/types').ServiceRole) =>
    req<{ role: string; roleSource: string }>(`/api/apps/${appId}/services/${serviceId}/role`, {
      method: 'PATCH',
      body: JSON.stringify({ role }),
    }),
  reidentifyService: (appId: string, serviceId: string) =>
    req<{ role: string; roleSource: string }>(`/api/apps/${appId}/services/${serviceId}/reidentify`, {
      method: 'POST',
    }),
```

> 注:`import('@/types').ServiceRole` 是内联类型导入,避免动顶部 import 块。若 lint 报错,改为顶部 `import` 加 `ServiceRole`。

- [ ] **Step 3: 类型检查**

Run: `cd code/src && npx vue-tsc --noEmit`
Expected: 无报错(若项目用 `npm run type-check` 则用那个)

- [ ] **Step 4: 提交**

```bash
cd code/src
git add types/index.ts api/http.ts
git commit -m "feat(web): add ServiceRole type + setServiceRole/reidentifyService api"
```

---

## Task 6: store action + WS 处理(已支持,补 action)

**Files:**
- Modify: `code/src/stores/apps.ts`

- [ ] **Step 1: 加 `setServiceRole` 与 `reidentifyService` action**

> WS `app:services` 消息已在现有 `handleWS` 里处理(替换 services 数组),role 变更后后端会广播 `app:services`,前端自动刷新,无需额外 WS 代码。

在 `apps.ts` 的 `update` action 之后(约 `patch` 函数前)加:

```ts
  /** 手动设置服务角色(锁定为 manual) */
  async function setServiceRole(appId: string, serviceId: string, role: import('@/types').ServiceRole) {
    await api.setServiceRole(appId, serviceId, role)
    // 后端会广播 app:services,本地也乐观更新避免等待
    const a = apps.value.find((x) => x.id === appId)
    if (a && a.services) {
      a.services = a.services.map((s) =>
        s.id === serviceId ? { ...s, role, roleSource: 'manual' as const } : s
      )
    }
  }

  /** 强制重新识别某服务角色(重置为 auto) */
  async function reidentifyService(appId: string, serviceId: string) {
    await api.reidentifyService(appId, serviceId)
    // 后端重新探测后广播 app:services,本地刷新由 WS 推动即可,这里不乐观更新(role 未知)
  }
```

在 `return { ... }` 里追加导出:

```ts
  return {
    apps, loading, error, liveLogs,
    load, importRaw, createFromCandidate, start, stop, restart, remove, rename, openURL, openDir, update,
    setServiceRole, reidentifyService,  // 新增
    patch, bindWS, clearLiveLogs,
  }
```

- [ ] **Step 2: 类型检查**

Run: `cd code/src && npx vue-tsc --noEmit`
Expected: 无报错

- [ ] **Step 3: 提交**

```bash
cd code/src
git add stores/apps.ts
git commit -m "feat(web): add setServiceRole/reidentifyService store actions"
```

---

## Task 7: AppCard 服务行 — 角色图标 + 点击切换菜单

**Files:**
- Modify: `code/src/components/AppCard.vue`(template 约 104-111 + script + style)

- [ ] **Step 1: script setup 加角色映射 + 切换逻辑**

在 `AppCard.vue` 的 `<script setup>` 中(找到现有 props/emits 定义处),加角色配置与切换方法。先看现有 script 头部结构:

Run: `cd code/src && grep -n "<script setup\|const props\|defineEmits\|const emit\|import" components/AppCard.vue | head`

在 `<script setup>` 内(现有 imports 之后)加:

```ts
import type { ServiceRole } from '@/types'

// 角色 → 图标/颜色/标签
const ROLE_META: Record<ServiceRole, { icon: string; color: string; label: string }> = {
  frontend: { icon: '🌐', color: '#3b82f6', label: '前端' },
  backend: { icon: '⚙️', color: '#8b5cf6', label: '后端' },
  database: { icon: '🗄️', color: '#f59e0b', label: '数据库' },
  unknown: { icon: '❓', color: '#9ca3af', label: '未识别' },
}

// 当前展开切换菜单的 service id(null=无)
const roleMenuOpen = ref<string | null>(null)

function toggleRoleMenu(svcId: string) {
  roleMenuOpen.value = roleMenuOpen.value === svcId ? null : svcId
}
async function pickRole(appId: string, svcId: string, role: ServiceRole) {
  roleMenuOpen.value = null
  await appsStore.setServiceRole(appId, svcId, role)
}
async function reidentify(appId: string, svcId: string) {
  roleMenuOpen.value = null
  await appsStore.reidentifyService(appId, svcId)
}
```

> 需确认 `appsStore` 在 setup 内的获取方式。若 AppCard 现有 script 未引入 store,则用 `props` + `emit` 由父组件调用 store 更稳妥(避免在叶子组件直接耦合 store)。**先看 AppCard 是否已用 store**:
> Run: `cd code/src && grep -n "useAppsStore\|appsStore\|defineProps\|defineEmits" components/AppCard.vue`
> - 若已用 store:直接调 `appsStore.setServiceRole`。
> - 若未用(纯 props/emit 模式):改为 emit 事件 `('set-role', appId, svcId, role)` 与 `('reidentify', appId, svcId)`,由父组件(App 列表页)转发到 store。**推荐 emit 模式**(与现有 `emit('stop', ...)` 风格一致)。采用 emit:

```ts
const emit = defineEmits<{
  (e: 'stop' | 'start' | 'restart' | 'open-url' | 'open-dir' | 'remove', id: string): void
  (e: 'set-role', appId: string, serviceId: string, role: ServiceRole): void   // 新增
  (e: 'reidentify', appId: string, serviceId: string): void                     // 新增
}>()

function pickRole(svcId: string, role: ServiceRole) {
  roleMenuOpen.value = null
  emit('set-role', props.a.id, svcId, role)
}
function reidentify(svcId: string) {
  roleMenuOpen.value = null
  emit('reidentify', props.a.id, svcId)
}
```
(确保 `ref` 已 import 自 vue;`props.a` 是现有 props 名,按实际调整。)

- [ ] **Step 2: 改 template 服务行**

定位 `AppCard.vue` template 中(约 104-111 行):

```html
      <div v-if="a.services && a.services.length" class="services">
        <div v-for="svc in a.services" :key="svc.id" class="svc-row">
          <span class="svc-dot" :class="svc.health" :title="healthText(svc.health)"></span>
          <span class="svc-port mono">:{{ svc.port }}</span>
          <a class="svc-url mono" :title="svc.url" @click.prevent="openServiceUrl(svc.url)">{{ svc.url }}</a>
        </div>
      </div>
```

替换为(加角色图标按钮 + 切换菜单):

```html
      <div v-if="a.services && a.services.length" class="services">
        <div v-for="svc in a.services" :key="svc.id" class="svc-row">
          <!-- 角色图标(可点击切换) -->
          <div class="role-wrap">
            <button
              class="role-btn"
              :class="{ locked: svc.roleSource === 'manual' }"
              :style="{ color: ROLE_META[svc.role || 'unknown'].color }"
              :title="ROLE_META[svc.role || 'unknown'].label + (svc.roleSource === 'manual' ? '（已锁定）' : '') + ' — 点击切换'"
              @click.stop="toggleRoleMenu(svc.id)"
            >{{ ROLE_META[svc.role || 'unknown'].icon }}</button>
            <!-- 切换菜单 -->
            <div v-if="roleMenuOpen === svc.id" class="role-menu" @click.stop>
              <button v-for="r in (['frontend','backend','database','unknown'] as ServiceRole[])" :key="r"
                :style="{ color: ROLE_META[r].color }"
                @click="pickRole(svc.id, r)">
                {{ ROLE_META[r].icon }} {{ ROLE_META[r].label }}
              </button>
              <button class="reidentify" @click="reidentify(svc.id)">🔄 重新识别</button>
            </div>
          </div>
          <span class="svc-dot" :class="svc.health" :title="healthText(svc.health)"></span>
          <span class="svc-port mono">:{{ svc.port }}</span>
          <a class="svc-url mono" :title="svc.url" @click.prevent="openServiceUrl(svc.url)">{{ svc.url }}</a>
        </div>
      </div>
```

- [ ] **Step 3: 加 CSS(找到 `<style>` 块的 `.services` 段,约 233 行附近,追加)**

```css
.role-wrap { position: relative; display: inline-flex; }
.role-btn {
  background: none; border: 1px solid transparent; border-radius: 4px;
  padding: 0 2px; font-size: 13px; cursor: pointer; line-height: 1;
}
.role-btn.locked { border-color: currentColor; border-style: dashed; }
.role-menu {
  position: absolute; left: 0; top: 100%; z-index: 10;
  background: var(--bg, #fff); border: 1px solid #ddd; border-radius: 6px;
  box-shadow: 0 4px 12px rgba(0,0,0,.12); padding: 4px; display: flex; flex-direction: column;
  min-width: 120px;
}
.role-menu button {
  background: none; border: none; text-align: left; padding: 4px 8px;
  cursor: pointer; font-size: 13px; border-radius: 4px;
}
.role-menu button:hover { background: rgba(0,0,0,.06); }
.role-menu .reidentify { color: #6b7280; border-top: 1px solid #eee; margin-top: 2px; padding-top: 6px; }
```

- [ ] **Step 4: 父组件接线 emit**

找到渲染 `AppCard` 的父组件(列表页):

Run: `cd code/src && grep -rn "<AppCard\|@stop\|@restart" --include="*.vue" .`

在父组件的 `<AppCard ...>` 上加:
```html
  @set-role="onSetRole"
  @reidentify="onReidentify"
```
在父组件 script 加(用 store):
```ts
import { useAppsStore } from '@/stores/apps'
const appsStore = useAppsStore()
function onSetRole(appId: string, serviceId: string, role: ServiceRole) {
  appsStore.setServiceRole(appId, serviceId, role)
}
function onReidentify(appId: string, serviceId: string) {
  appsStore.reidentifyService(appId, serviceId)
}
```
import `ServiceRole` 类型。

- [ ] **Step 5: 关闭菜单的点击外部处理**

菜单打开时点外部应关闭。在 `AppCard.vue` script 加全局点击监听(onMounted/onUnmounted)或用更简单的 `@click.stop` 阻止冒泡 + 一个透明遮罩。简单方案:template 在菜单同级加透明遮罩:

```html
<div v-if="roleMenuOpen" class="role-backdrop" @click="roleMenuOpen = null"></div>
```
CSS:`.role-backdrop { position: fixed; inset: 0; z-index: 5; }`(放 `.role-menu` z-index 之下)

- [ ] **Step 6: 类型检查 + 构建**

Run: `cd code/src && npx vue-tsc --noEmit && npm run build`
Expected: 无报错

- [ ] **Step 7: 提交**

```bash
cd code/src
git add components/AppCard.vue <父组件路径>.vue
git commit -m "feat(web): service row shows role icon + click-to-switch menu + reidentify"
```

---

## Task 8: 端到端手测 + 收尾

**Files:** 无代码改动,验证为主

- [ ] **Step 1: 启动 sidecar + 前端 dev**

Run(分别两个终端):
```
cd code/sidecar && go run ./cmd/launcher-sidecar   # 或实际 main 包路径,用 go run .
cd code/src && npm run dev
```

- [ ] **Step 2: 导入一个已知多服务项目(前端 Vite + 后端 + DB)**

若有现成测试项目:启动它,观察 services 列表:
- Vite 端口(5173)→ 🌐 前端(响应头 `Server: vite` 命中,或日志 `vite v5`)
- 后端 API 端口(如 8000)→ ⚙️ 后端(响应头/JSON)
- 若有 DB 端口(5432 等)→ 🗄️ 数据库(端口命中)

- [ ] **Step 3: 测手动切换**

点某服务图标 → 菜单 → 选另一角色 → 图标变化 + 出现锁定(虚线)标记 → 重启项目后角色保持(manual)。

- [ ] **Step 4: 测重新识别**

对 manual 锁定的服务点"重新识别" → 角色回到 auto + 重新探测结果。

- [ ] **Step 5: 测老库兼容**

用一个未升级的旧 db 文件启动,确认 `ensureSchema` 自动补列、不报错、已有服务显示 unknown 然后逐步被识别。

- [ ] **Step 6: 全量测试回归**

Run: `cd code/sidecar && go test ./...`
Expected: 全 PASS(含原有 probe/store/logbus/security 测试 + 新增 classify/ensure_schema 测试)

- [ ] **Step 7: 最终提交(若有手测中发现的小修)**

```bash
git add -A && git commit -m "test: e2e verified service role detection"
```

---

## 自检(写完计划后对照 spec 复核)

**1. Spec 覆盖:**
- ✅ 角色 4 类 → Task 1 classify + Task 5 类型
- ✅ 信号优先级(DB>头>Title>日志)→ Task 1 Classify 短路顺序
- ✅ 数据模型 role/roleSource → Task 2
- ✅ ensureSchema(方案 A)→ Task 2 Step 3
- ✅ discoverServices 初判 + probe 升级 → Task 3 Step 3-4
- ✅ recheckAndAggregate 升级 → Task 3 Step 5
- ✅ PATCH /role(manual)→ Task 4 Step 2
- ✅ POST /reidentify → Task 4 Step 2
- ✅ WS app:services(复用)→ Task 6 说明(已支持)
- ✅ 前端图标+菜单+锁定+重新识别 → Task 7
- ✅ 兼容性(老库)→ Task 8 Step 5
- ✅ classify 单测用例 → Task 1 Step 1(含 DB 端口短路、各置信度)

**2. 占位符扫描:** 无 TBD/TODO 占位(Task 3 有一个 `collectLogHints` 留接口返回 nil 的简化,已在注释标明理由,非占位)。

**3. 类型一致性:**
- `Role` 常量字符串值 `"frontend"/"backend"/"database"/"unknown"`:Go sidecar(`store.RoleFrontend` 等)、TS(`ServiceRole`)、classify(`probe.RoleFrontend`)三处一致 ✅
- `roleSource` 值 `"auto"/"manual"`:store 常量 + TS 字面量一致 ✅
- `SetServiceRole`/`UpdateServiceRoleIfAuto`/`ResetServiceRoleToAuto`/`GetService`:Task 2 定义、Task 4 调用,签名一致 ✅
- `probe.Classify`/`ClassifyInput`/`Confidence`/`ConfHigh`/`ConfMedium`:Task 1 定义、Task 3/4 调用一致 ✅

**注(实现者需按实际确认的点,已在对应 Step 标注 grep 命令):**
- `runState` 结构体字段名(`RunID`/`candidateURLs`)—— Task 3 Step 4 给了 grep 命令
- `AppCard.vue` 的 props 名(`a` 还是 `app`)与 emit 列表 —— Task 7 Step 1 给了 grep 命令
- 父组件路径 —— Task 7 Step 4 给了 grep 命令
