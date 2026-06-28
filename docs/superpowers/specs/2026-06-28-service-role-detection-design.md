# 服务角色自动识别 + 手动修正

**日期**: 2026-06-28
**状态**: 已确认,待实现
**关联**: 基于现有 `AppService` 多服务模型(`002_services.sql`),为其增加角色分类能力

## 1. 背景与目标

当前 `AppService`(项目下每个监听端口对应一条服务记录)只有 `port/url/health`,**无法区分该服务是前端、后端还是数据库**。类型注释里写的设计意图("前端/后端/DB 各一个端口")从未在代码层面落地。

**目标**:让每个服务自动带一个角色标签(`frontend`/`backend`/`database`/`unknown`),识别错误时用户可手动修正。

**非目标**:
- 不做更细粒度分类(如"缓存""消息队列"单独成类),过度细分误判率上升
- 不识别具体框架版本(如"Vite 5"),只到角色层
- 不为数据库做协议级握手探测(只靠端口 + HTTP 不可达判定),避免引入二进制协议解析

## 2. 角色定义

| 角色 | 值 | 图标 | 颜色 |
|---|---|---|---|
| 前端 | `frontend` | 🌐 | 浅蓝 |
| 后端 | `backend` | ⚙️ | 浅紫 |
| 数据库 | `database` | 🗄️ | 琥珀 |
| 未识别 | `unknown` | ❓ | 灰 |

`unknown` 是初始/兜底值,不是错误状态——它表示"信号不足,等更多数据或用户指定"。

## 3. 识别信号与流程

### 3.1 信号源(按置信度从高到低)

| 级别 | 信号 | 来源 | 判定 |
|---|---|---|---|
| 高 | 端口命中标准 DB 端口 | 端口快照(已有) | → `database` |
| 高 | HTTP 响应头特征 | `probe.HealthResult.Server`(已抓未用) | 见下表 |
| 中 | HTTP body 特征 | `probe.HealthResult.Title`(已抓未用) + Content-Type | 见下表 |
| 低 | 日志框架特征 | `logbus` 事件文本 | 见下表 |

**响应头特征映射:**
- `frontend`: `Server` 或 `X-Powered-By` 含 `vite`/`webpack`/`next`/`nuxt`
- `backend`: `Server` 或 `X-Powered-By` 含 `express`/`fastapi`/`uvicorn`/`gunicorn`/`php`/`kestrel`;或 `Content-Type: application/json`(根路径返回 JSON 强烈提示是 API)
- `database`: 标准端口命中(见下),与响应头无关

**Title 特征映射:**
- `frontend`: HTML `<title>` 含 `vite`/`react app`/`vue`/`angular`/`nuxt`,或 `<div id="app">`/`<div id="root">` 特征
- 仅作 backend 的辅助反证:返回 JSON 无 title,不判 frontend

**日志特征映射:**
- `frontend`: 日志含 `vite v\d`/`webpack compiled`/`ready in \d+ms`/`Local:\s*http`
- `backend`: 日志含 `uvicorn running`/`started server on`/`listening on`/`django version`/`flask`

**标准 DB 端口集合:**
- `5432`(PostgreSQL)、`3306`(MySQL)、`6379`(Redis)、`27017`(MongoDB)、`1433`(SQL Server)、`8529`(Dgraph)、`9092`(Kafka)、`9200`(Elasticsearch)、`11211`(Memcached)

### 3.2 判定流程

```
新端口被发现
   │
   ├─ ① 端口 ∈ DB端口集? ──是──► database(高置信,定)
   │
   ├─ ② HTTP 健康检查(已有 probe.CheckHealth)
   │      ├─ 响应头匹配 ──────► frontend/backend(高)
   │      └─ Title/Content-Type 匹配 ─► frontend/backend(中)
   │
   ├─ ③ 日志特征匹配 ─────► frontend/backend(低,可被后续高置信度覆盖)
   │
   └─ 都不命中 ──► unknown
```

### 3.3 覆盖规则

置信度低的让位给置信度高的。具体:
- `roleSource == "manual"`(用户手动指定):**永不覆盖**,只更新 health
- `roleSource == "auto"`:
  - 当前是高置信度 → 不被低置信度覆盖
  - 当前是低置信度(日志判的)→ 后续若拿到高置信度信号(响应头),则更新
  - 当前是 `unknown` → 任何信号都能填进去

## 4. 数据模型改动

### 4.1 新增 migration: `003_service_roles.sql`

```sql
-- 003_service_roles.sql: 服务角色识别
-- 为每个服务增加角色标签,支持自动识别 + 手动修正。

-- SQLite 的 ALTER TABLE ADD COLUMN 是幂等的:列已存在时报错。
-- 用一种"软幂等"写法:用 PRAGMA 检测列是否存在,不存在才加。
-- 但 SQL 层难直接写条件,这里依赖 modernc.org/sqlite 的事务特性:
-- 在 Go 层 store.Open 之后单独执行这两条,失败则认为已加(见 store.go 改动说明)。
-- 此文件保持与其它 migration 一致的纯 SQL 风格,采用"先查列再决定"不可行,
-- 故实际采用:store 层在 migrate 后单独跑 ensureColumns(见下)。

ALTER TABLE app_services ADD COLUMN role TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE app_services ADD COLUMN role_source TEXT NOT NULL DEFAULT 'auto';
```

> **实现注意**:SQLite `ALTER TABLE ADD COLUMN` 在列已存在时会报 `duplicate column name` 错误,不像 `CREATE TABLE IF NOT EXISTS` 那样幂等。现有 migration 框架是"所有脚本每次启动都执行"。因此本列的添加**不能放进 `003_service_roles.sql` 走通用的 `migrate()` 流程**(会重复执行报错)。
>
> **改为**:在 `store.go` 的 `Open` 中,`migrate()` 之后调用新增的 `ensureSchema()` 方法,该方法查询 `PRAGMA table_info(app_services)`,缺哪列就 `ALTER TABLE ADD COLUMN` 哪列。这是 SQLite 演进 schema 的标准做法,与现有迁移框架兼容。`003_service_roles.sql` 文件改为只放注释 + 索引(若需要),或干脆不建该文件,改为 `ensureSchema` 内联。**采纳后者**:`003_service_roles.sql` 不创建,改在 Go 层 `ensureSchema` 处理,见 4.2。

### 4.2 `store.go` 新增 `ensureSchema`

在 `Open` 中 `migrate()` 之后调用:
```go
if err := s.ensureSchema(); err != nil {
    db.Close()
    return nil, fmt.Errorf("ensure schema: %w", err)
}
```
`ensureSchema` 实现:读 `PRAGMA table_info(app_services)`,若缺 `role`/`role_source` 则 `ALTER TABLE ADD COLUMN`。

### 4.3 `models.go` 改动

`AppService` 结构体加两字段:
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
    Role        string `json:"role"`        // 新增: frontend|backend|database|unknown
    RoleSource  string `json:"roleSource"`  // 新增: auto|manual
}
```

受影响的 SQL 方法(全部加 `role, role_source` 列):
- `UpsertService`:INSERT 列表 + VALUES 加两列;`ON CONFLICT DO UPDATE` 增量更新 role(仅当原 `role_source='auto'` 且新置信度更高时,但此判断放在调用方 classify 层,store 只负责写入)
- `UpdateServiceHealth`:不动 role
- `ListServicesByApp` / `ListServicesByRun`:SELECT 加两列,`scanServices` 加两字段
- 新增 `SetServiceRole(id, role string)`:更新 role 且置 `role_source='manual'`
- 新增 `UpdateServiceRoleIfAuto(id, role string)`:仅当 `role_source='auto'` 时更新

## 5. 识别模块(新增)

**新文件**: `sidecar/internal/probe/classify.go`

纯函数,无副作用,便于单测:
```go
package probe

// Role 服务角色。
type Role string

const (
    RoleUnknown  Role = "unknown"
    RoleFrontend Role = "frontend"
    RoleBackend  Role = "backend"
    RoleDatabase Role = "database"
)

// ClassifyInput 识别输入。
type ClassifyInput struct {
    Port     int
    Headers  map[string]string  // HTTP 响应头(小写键),可空
    Title    string             // HTML title,可空
    BodyCT   string             // Content-Type,可空
    LogHints []string           // 相关日志片段(最近 N 行命中框架特征的),可空
}

// Classify 返回 (角色, 置信度)。纯函数。
func Classify(in ClassifyInput) (Role, Confidence) { ... }

type Confidence int
const (
    ConfNone Confidence = iota
    ConfLow    // 日志
    ConfMedium // title/content-type
    ConfHigh   // 响应头 / DB端口
)
```

**判定顺序**(短路,命中即返回):
1. `Port` ∈ DB端口集 → `(database, High)`
2. Headers 匹配 frontend/backend → `(x, High)`
3. Title/BodyCT 匹配 → `(x, Medium)`
4. LogHints 匹配 → `(x, Low)`
5. 都不命中 → `(unknown, None)`

## 6. 接线点改动

### 6.1 `launcher.go: discoverServices`(发现新端口时识别)

在 `svc` 创建后、`UpsertService` 前,加入识别:
```go
svc := &store.AppService{ ... }
// 新增:角色识别
role, conf := classifyService(p.Port, rs.logBlobs)  // 先用端口+日志初判
svc.Role = string(role)
svc.RoleSource = "auto"
_ = l.Store.UpsertService(svc)
// 新增:异步入健康检查,拿到响应头后可能升级 role(若当前置信度 < High)
go l.refineRoleWithProbe(appID, svc.ID, svc.URL, conf)
```

### 6.2 `launcher.go: recheckAndAggregate`(健康复查时顺带识别)

在 `probeService` 成功时,若返回了 `*HealthResult`,对 `role_source='auto'` 且当前置信度低的服务,用响应头重新 classify 并升级。

### 6.3 新增 API: 手动改角色

`handlers_ops.go` 加:
```
PATCH /api/apps/:id/services/:sid/role
Body: { "role": "frontend" }
```
调用 `store.SetServiceRole(sid, role)`(置 `role_source='manual'`),广播 WS `service:role` 事件刷新前端。

### 6.4 新增 API: 强制重新识别

```
POST /api/apps/:id/services/:sid/reidentify
```
无视 `role_source`,重新跑 classify(端口+probe 响应头),写入并置回 `auto`。用于用户想推翻自己的手动标注、回到自动。

## 7. 前端改动

### 7.1 `types/index.ts`
`AppService` 接口加 `role`/`roleSource` 两字段。

### 7.2 `components/AppCard.vue`
服务行布局:
```
[角色图标] ● :5173  http://localhost:5173
```
- 图标按 role 渲染(🌐/⚙️/🗄️/❓),带颜色
- `unknown` 的图标用浅灰 + 虚线边框,提示可点击
- 点击图标 → 弹出 4 选 1 菜单(frontend/backend/database/unknown),选中调 `setServiceRole`
- `roleSource=manual` 的图标右上角加一个小钉子 🔒 标记,表示已锁定

### 7.3 `stores/apps.ts`
新增 action `setServiceRole(appId, serviceId, role)`:调 PATCH API,本地更新对应 service 的 role/roleSource。

### 7.4 WS 事件
监听 `service:role` 事件,更新对应 service(用于多视图同步,如另一个面板也在看)。

## 8. 重新识别时机汇总

| 时机 | 识别? | 说明 |
|---|---|---|
| 发现新端口 | ✅ 初判(端口+日志) + 异步 probe 升级 | 主路径 |
| 健康复查成功 | ✅ 仅对 auto 且低置信度的服务升级 | 顺带,省请求 |
| 手动标注后 | ❌ | manual 锁定 |
| 调用 reidentify API | ✅ 强制 | 推翻 manual |

## 9. 测试策略

| 层 | 测试 | 工具 |
|---|---|---|
| `probe/classify.go` | 纯函数表驱动测试:各种 header/title/port/log 组合 → 期望 role+conf | `go test` |
| `store` | `ensureSchema` 幂等(跑两次不报错)、`SetServiceRole`/`UpdateServiceRoleIfAuto` 行为 | `go test` + 内存 sqlite |
| 集成 | 端到端:mock 一个 vite 服务(返回 `Server: vite`),启动后 service.role==frontend | 手测 + 可选 e2e |
| 前端 | 图标渲染、点击切换、manual 锁定标记 | 手测 |

**classify 单测用例(最少):**
- 端口 5432 → database/High
- Header `X-Powered-By: Express` → backend/High
- Header `Server: vite` → frontend/High
- Title "Vite + React" 无 header → frontend/Medium
- 日志 "vite v5" 无其它 → frontend/Low
- 全空 → unknown/None
- 端口 5432 + Header vite → database/High(DB 端口优先,短路)

## 10. 兼容性

- **老数据库**:无 role/role_source 列 → `ensureSchema` 自动补列,默认 `unknown`/`auto`,已有服务启动后会在下次健康复查时被识别
- **前端老版本**:忽略新字段无影响(增量字段)
- **API**:新增端点,不改老端点契约
- **WS**:新增事件类型,老前端不监听即忽略

## 11. 不做(YAGNI)

- 不做"学习/记忆":不会因为上次判对了就记住(每次按信号实时判,memory 价值低且增加复杂度)
- 不做角色聚合(如"项目有 2 前端 1 后端"的总览统计),先保证单服务正确
- 不做自定义角色(用户不能加"缓存"这类新类别)
- 不识别进程命令行(如 `redis-server`),信号太杂且 Windows 进程信息获取不可靠
