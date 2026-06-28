package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// ----- 实体类型（与 DB schema 对齐，JSON tag 与前端 TS 类型对齐）-----

// App 是一个被托管的启动单元。
type App struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	EntryScript   string   `json:"entryScript"`
	Cwd           string   `json:"cwd"`
	AdapterType   string   `json:"adapterType"`
	Cmd           string   `json:"cmd"`
	Args          []string `json:"args"`
	Env           map[string]string `json:"env"`
	Tags          []string `json:"tags"`
	GroupID       *string  `json:"groupId"`
	PortHints     []int    `json:"portHints"`
	HealthURL     string   `json:"healthUrl"`
	ScriptHash    string   `json:"scriptHash"`
	Confirmed     bool     `json:"confirmed"`
	ConfirmedHash string   `json:"confirmedHash"`
	CreatedAt     string   `json:"createdAt"`
	LastStartedAt *string  `json:"lastStartedAt"`
	LastURL       string   `json:"lastUrl"`
	LastStatus    string   `json:"lastStatus"`
	SortOrder     int      `json:"sortOrder"`
}

// Group 分组。
type Group struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color"`
	Order     []string `json:"order"`
	CreatedAt string   `json:"createdAt"`
}

// AppRun 运行实例。
type AppRun struct {
	ID        string  `json:"id"`
	AppID     string  `json:"appId"`
	PID       int     `json:"pid"`
	RootPID   int     `json:"rootPid"`
	Status    string  `json:"status"`
	StartedAt string  `json:"startedAt"`
	StoppedAt *string `json:"stoppedAt"`
	ExitCode  *int    `json:"exitCode"`
}

// LogEntry 一条日志。
type LogEntry struct {
	ID        int64  `json:"id"`
	AppRunID  string `json:"appRunId"`
	Ts        string `json:"ts"`
	Stream    string `json:"stream"`
	Level     string `json:"level"`
	Text      string `json:"text"`
}

// PortEntry 端口发现记录。
type PortEntry struct {
	ID         int64  `json:"id"`
	AppRunID   string `json:"appRunId"`
	Port       int    `json:"port"`
	Proto      string `json:"proto"`
	DetectedAt string `json:"detectedAt"`
}

// AppService 项目下的一个服务（对应一个监听端口）。
type AppService struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	AppRunID    string `json:"appRunId"`
	Port        int    `json:"port"`
	URL         string `json:"url"`
	Health      string `json:"health"`      // healthy/unhealthy/unknown
	LastChecked string `json:"lastChecked"`
	DetectedAt  string `json:"detectedAt"`
	Role        string `json:"role"`        // frontend|backend|database|unknown
	RoleSource  string `json:"roleSource"`  // auto|manual
}

// AppService 的 role 取值常量。
const (
	RoleFrontend = "frontend"
	RoleBackend  = "backend"
	RoleDatabase = "database"
	RoleUnknown  = "unknown"
)

// AppService 的 role_source 取值。
const (
	RoleSourceAuto   = "auto"
	RoleSourceManual = "manual"
)

// ----- AppService CRUD（多服务模型） -----

// UpsertService 插入或更新一个服务（按 app_run_id + port 去重）。
// ON CONFLICT 不覆盖 role/role_source —— 角色更新走 SetServiceRole/UpdateServiceRoleIfAuto,
// 避免 upsert 把手动标注(manual)抹掉。
func (s *Store) UpsertService(svc *AppService) error {
	_, err := s.db.Exec(`INSERT INTO app_services (id,app_id,app_run_id,port,url,health,last_checked,detected_at,role,role_source)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET health=excluded.health, last_checked=excluded.last_checked, url=excluded.url`,
		svc.ID, svc.AppID, svc.AppRunID, svc.Port, svc.URL, svc.Health,
		nullableStringEmpty(svc.LastChecked), svc.DetectedAt,
		strDefault(svc.Role, RoleUnknown), strDefault(svc.RoleSource, RoleSourceAuto))
	return err
}

// HasService 检查某 run 下某端口是否已记录。
func (s *Store) HasService(runID string, port int) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM app_services WHERE app_run_id=? AND port=?`, runID, port).Scan(&n)
	return n > 0
}

// UpdateServiceHealth 更新某服务的健康状态。
func (s *Store) UpdateServiceHealth(id, health, lastChecked string) error {
	_, err := s.db.Exec(`UPDATE app_services SET health=?, last_checked=? WHERE id=?`,
		health, nullableStringEmpty(lastChecked), id)
	return err
}

// ListServicesByApp 返回某项目下所有服务（按端口排序）。
func (s *Store) ListServicesByApp(appID string) ([]*AppService, error) {
	rows, err := s.db.Query(`SELECT id,app_id,app_run_id,port,url,health,last_checked,detected_at,role,role_source
		FROM app_services WHERE app_id=? ORDER BY port ASC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanServices(rows)
}

// ListServicesByRun 返回某次运行发现的所有服务。
func (s *Store) ListServicesByRun(runID string) ([]*AppService, error) {
	rows, err := s.db.Query(`SELECT id,app_id,app_run_id,port,url,health,last_checked,detected_at,role,role_source
		FROM app_services WHERE app_run_id=? ORDER BY port ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanServices(rows)
}

// scanServices 公共扫描逻辑：last_checked 可为 NULL，用 NullString 处理。
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

// SetServiceRole 手动设置角色:置 role 并锁定 role_source=manual（自动识别不再覆盖）。
func (s *Store) SetServiceRole(id, role string) error {
	_, err := s.db.Exec(`UPDATE app_services SET role=?, role_source=? WHERE id=?`,
		role, RoleSourceManual, id)
	return err
}

// UpdateServiceRoleIfAuto 仅当 role_source='auto' 时更新 role（自动识别升级用）。
// 返回 updated 表示是否实际更新了一行（用于决定是否广播：行被删除/已锁定/值未变时返回 false）。
func (s *Store) UpdateServiceRoleIfAuto(id, role string) (updated bool, err error) {
	res, err := s.db.Exec(`UPDATE app_services SET role=? WHERE id=? AND role_source='auto' AND role<>?`, role, id, role)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ResetServiceRoleToAuto 强制重新识别:重置 role_source=auto（供 reidentify 端点用）。
// role 本身由调用方随后重新 classify 写入。
func (s *Store) ResetServiceRoleToAuto(id string) error {
	_, err := s.db.Exec(`UPDATE app_services SET role_source=? WHERE id=?`, RoleSourceAuto, id)
	return err
}

// strDefault 空串返回 def（本包私有，不与 api 包的 defaultStr 冲突）。
func strDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// DeleteServicesByApp 删除某项目下所有服务（项目删除/重新启动时清理）。
func (s *Store) DeleteServicesByApp(appID string) error {
	_, err := s.db.Exec(`DELETE FROM app_services WHERE app_id=?`, appID)
	return err
}

// ----- App CRUD -----

// CreateApp 插入一个新 App。
func (s *Store) CreateApp(a *App) error {
	args, _ := json.Marshal(a.Args)
	env, _ := json.Marshal(a.Env)
	tags, _ := json.Marshal(a.Tags)
	hints := hintsJSON(a.PortHints)
	_, err := s.db.Exec(`INSERT INTO apps
		(id,name,entry_script,cwd,adapter_type,cmd,args_json,env_json,tags_json,group_id,port_hints_json,health_url,script_hash,confirmed,confirmed_hash,last_status,sort_order)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.EntryScript, a.Cwd, a.AdapterType, a.Cmd, string(args), string(env),
		string(tags), nullableString(a.GroupID), hints, a.HealthURL, a.ScriptHash, boolToInt(a.Confirmed),
		a.ConfirmedHash, a.LastStatus, a.SortOrder)
	return err
}

// GetApp 按 ID 查询。
func (s *Store) GetApp(id string) (*App, error) {
	row := s.db.QueryRow(`SELECT id,name,entry_script,cwd,adapter_type,cmd,args_json,env_json,tags_json,
		group_id,port_hints_json,health_url,script_hash,confirmed,confirmed_hash,created_at,
		last_started_at,last_url,last_status,sort_order FROM apps WHERE id=?`, id)
	return scanApp(row)
}

// ListApps 返回全部 App，按 sort_order, name 排序。
func (s *Store) ListApps() ([]*App, error) {
	rows, err := s.db.Query(`SELECT id,name,entry_script,cwd,adapter_type,cmd,args_json,env_json,tags_json,
		group_id,port_hints_json,health_url,script_hash,confirmed,confirmed_hash,created_at,
		last_started_at,last_url,last_status,sort_order FROM apps ORDER BY sort_order ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateApp 全字段更新。
func (s *Store) UpdateApp(a *App) error {
	args, _ := json.Marshal(a.Args)
	env, _ := json.Marshal(a.Env)
	tags, _ := json.Marshal(a.Tags)
	hints := hintsJSON(a.PortHints)
	_, err := s.db.Exec(`UPDATE apps SET name=?,entry_script=?,cwd=?,adapter_type=?,cmd=?,args_json=?,env_json=?,
		tags_json=?,group_id=?,port_hints_json=?,health_url=?,script_hash=?,confirmed=?,confirmed_hash=?,
		last_started_at=?,last_url=?,last_status=?,sort_order=? WHERE id=?`,
		a.Name, a.EntryScript, a.Cwd, a.AdapterType, a.Cmd, string(args), string(env), string(tags),
		nullableString(a.GroupID), hints, a.HealthURL, a.ScriptHash, boolToInt(a.Confirmed), a.ConfirmedHash,
		nullableString(a.LastStartedAt), nullableStringEmpty(a.LastURL), a.LastStatus, a.SortOrder, a.ID)
	return err
}

// TouchAppRuntime 更新运行态字段（启动时间/URL/状态）。
func (s *Store) TouchAppRuntime(id string, lastStartedAt, lastURL, lastStatus string) error {
	_, err := s.db.Exec(`UPDATE apps SET last_started_at=COALESCE(?,last_started_at),
		last_url=COALESCE(?,last_url), last_status=? WHERE id=?`,
		nullableStringEmpty(lastStartedAt), nullableStringEmpty(lastURL), lastStatus, id)
	return err
}

// DeleteApp 删除 App。
func (s *Store) DeleteApp(id string) error {
	_, err := s.db.Exec(`DELETE FROM apps WHERE id=?`, id)
	return err
}

// ----- AppRun -----

func (s *Store) CreateRun(r *AppRun) error {
	_, err := s.db.Exec(`INSERT INTO app_runs (id,app_id,pid,root_pid,status,started_at) VALUES (?,?,?,?,?,?)`,
		r.ID, r.AppID, r.PID, r.RootPID, r.Status, r.StartedAt)
	return err
}

func (s *Store) UpdateRunStatus(id, status string, exitCode *int) error {
	stopped := (*string)(nil)
	if status == "stopped" || status == "failed" {
		now := time.Now().UTC().Format(time.RFC3339)
		stopped = &now
	}
	_, err := s.db.Exec(`UPDATE app_runs SET status=?, stopped_at=COALESCE(?,stopped_at), exit_code=? WHERE id=?`,
		status, nullableStringPtr(stopped), nullableIntPtr(exitCode), id)
	return err
}

func (s *Store) GetLatestRun(appID string) (*AppRun, error) {
	row := s.db.QueryRow(`SELECT id,app_id,pid,root_pid,status,started_at,stopped_at,exit_code
		FROM app_runs WHERE app_id=? ORDER BY started_at DESC LIMIT 1`, appID)
	r := &AppRun{}
	var stopped sql.NullString
	var exitCode sql.NullInt64
	if err := row.Scan(&r.ID, &r.AppID, &r.PID, &r.RootPID, &r.Status, &r.StartedAt, &stopped, &exitCode); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if stopped.Valid {
		r.StoppedAt = &stopped.String
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		r.ExitCode = &v
	}
	return r, nil
}

// ----- Logs -----

func (s *Store) InsertLog(runID, stream, level, text string) error {
	_, err := s.db.Exec(`INSERT INTO logs (app_run_id,stream,level,text) VALUES (?,?,?,?)`,
		runID, stream, level, text)
	return err
}

// RecentLogs 返回自 sinceID 之后最多 limit 条日志。
func (s *Store) RecentLogs(runID string, sinceID int64, limit int) ([]*LogEntry, error) {
	rows, err := s.db.Query(`SELECT id,app_run_id,ts,stream,level,text FROM logs
		WHERE app_run_id=? AND id>? ORDER BY id ASC LIMIT ?`, runID, sinceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LogEntry
	for rows.Next() {
		e := &LogEntry{}
		if err := rows.Scan(&e.ID, &e.AppRunID, &e.Ts, &e.Stream, &e.Level, &e.Text); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SearchLogs 在某 run 内按关键词过滤（LIKE）。
func (s *Store) SearchLogs(runID, keyword string, limit int) ([]*LogEntry, error) {
	rows, err := s.db.Query(`SELECT id,app_run_id,ts,stream,level,text FROM logs
		WHERE app_run_id=? AND text LIKE ? ORDER BY id DESC LIMIT ?`,
		runID, "%"+keyword+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LogEntry
	for rows.Next() {
		e := &LogEntry{}
		if err := rows.Scan(&e.ID, &e.AppRunID, &e.Ts, &e.Stream, &e.Level, &e.Text); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ----- Ports -----

func (s *Store) InsertPort(runID string, port int, proto string) error {
	_, err := s.db.Exec(`INSERT INTO ports (app_run_id,port,proto) VALUES (?,?,?)`, runID, port, proto)
	return err
}

func (s *Store) ListPorts(runID string) ([]*PortEntry, error) {
	rows, err := s.db.Query(`SELECT id,app_run_id,port,proto,detected_at FROM ports WHERE app_run_id=? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PortEntry
	for rows.Next() {
		p := &PortEntry{}
		if err := rows.Scan(&p.ID, &p.AppRunID, &p.Port, &p.Proto, &p.DetectedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ----- Group -----

func (s *Store) CreateGroup(g *Group) error {
	order, _ := json.Marshal(g.Order)
	_, err := s.db.Exec(`INSERT INTO groups (id,name,color,order_json) VALUES (?,?,?,?)`,
		g.ID, g.Name, g.Color, string(order))
	return err
}

func (s *Store) ListGroups() ([]*Group, error) {
	rows, err := s.db.Query(`SELECT id,name,color,order_json,created_at FROM groups ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		g := &Group{}
		var orderJSON string
		if err := rows.Scan(&g.ID, &g.Name, &g.Color, &orderJSON, &g.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(orderJSON), &g.Order)
		if g.Order == nil {
			g.Order = []string{}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) UpdateGroup(g *Group) error {
	order, _ := json.Marshal(g.Order)
	_, err := s.db.Exec(`UPDATE groups SET name=?,color=?,order_json=? WHERE id=?`,
		g.Name, g.Color, string(order), g.ID)
	return err
}

func (s *Store) DeleteGroup(id string) error {
	// 先解除 App 引用，再删除
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE apps SET group_id=NULL WHERE group_id=?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM groups WHERE id=?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ----- Settings -----

func (s *Store) GetSetting(key, def string) string {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key,value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key,value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// ----- helpers -----

type scanner interface {
	Scan(dest ...any) error
}

func scanApp(sc scanner) (*App, error) {
	a := &App{}
	var argsJSON, envJSON, tagsJSON, hintsJSON string
	var groupID, lastStarted sql.NullString
	var lastURL sql.NullString
	var confirmed int
	if err := sc.Scan(&a.ID, &a.Name, &a.EntryScript, &a.Cwd, &a.AdapterType, &a.Cmd,
		&argsJSON, &envJSON, &tagsJSON, &groupID, &hintsJSON, &a.HealthURL, &a.ScriptHash,
		&confirmed, &a.ConfirmedHash, &a.CreatedAt, &lastStarted, &lastURL, &a.LastStatus, &a.SortOrder); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(argsJSON), &a.Args)
	_ = json.Unmarshal([]byte(envJSON), &a.Env)
	_ = json.Unmarshal([]byte(tagsJSON), &a.Tags)
	_ = json.Unmarshal([]byte(hintsJSON), &a.PortHints)
	if a.Args == nil {
		a.Args = []string{}
	}
	if a.Env == nil {
		a.Env = map[string]string{}
	}
	if a.Tags == nil {
		a.Tags = []string{}
	}
	if a.PortHints == nil {
		a.PortHints = []int{}
	}
	if groupID.Valid {
		g := groupID.String
		a.GroupID = &g
	}
	if lastStarted.Valid {
		s := lastStarted.String
		a.LastStartedAt = &s
	}
	if lastURL.Valid {
		a.LastURL = lastURL.String
	}
	a.Confirmed = confirmed != 0
	return a, nil
}

func hintsJSON(hints []int) string {
	if len(hints) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(hints)
	return string(b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullableStringEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableStringPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullableIntPtr(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}
