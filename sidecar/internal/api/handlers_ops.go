package api

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/store"
)

// ----- 请求/响应体 -----

type createAppBody struct {
	Name        string             `json:"name"`
	EntryScript string             `json:"entryScript"`
	Cwd         string             `json:"cwd"`
	AdapterType string             `json:"adapterType"`
	Cmd         string             `json:"cmd"`
	Args        []string           `json:"args"`
	Env         map[string]string  `json:"env"`
	Tags        []string           `json:"tags"`
	GroupID     *string            `json:"groupId"`
	PortHints   []int              `json:"portHints"`
	HealthURL   string             `json:"healthUrl"`
	ScriptHash  string             `json:"scriptHash"`
	SortOrder   int                `json:"sortOrder"`
	CardColor   string             `json:"cardColor"`
}

type updateAppBody struct {
	Name        *string            `json:"name"`
	EntryScript *string            `json:"entryScript"`
	Cwd         *string            `json:"cwd"`
	AdapterType *string            `json:"adapterType"`
	Cmd         *string            `json:"cmd"`
	Args        *[]string          `json:"args"`
	Env         *map[string]string `json:"env"`
	Tags        *[]string          `json:"tags"`
	GroupID     *string            `json:"groupId"`
	PortHints   *[]int             `json:"portHints"`
	HealthURL   *string            `json:"healthUrl"`
	CardColor   *string            `json:"cardColor"`
}

func applyUpdate(a *store.App, b *updateAppBody) {
	if b.Name != nil {
		a.Name = *b.Name
	}
	if b.Cwd != nil {
		a.Cwd = *b.Cwd
	}
	if b.AdapterType != nil {
		a.AdapterType = *b.AdapterType
	}
	if b.Cmd != nil {
		a.Cmd = *b.Cmd
	}
	if b.Args != nil {
		a.Args = *b.Args
	}
	if b.Env != nil {
		a.Env = *b.Env
	}
	if b.Tags != nil {
		a.Tags = *b.Tags
	}
	if b.GroupID != nil {
		a.GroupID = b.GroupID
	}
	if b.PortHints != nil {
		a.PortHints = *b.PortHints
	}
	if b.HealthURL != nil {
		a.HealthURL = *b.HealthURL
	}
	if b.CardColor != nil {
		a.CardColor = *b.CardColor
	}
}

// appView 把 App 与其运行态拼成前端需要的视图。
func appView(a *store.App, s *Server) map[string]any {
	row := map[string]any{
		"id":          a.ID,
		"name":        a.Name,
		"entryScript": a.EntryScript,
		"cwd":         a.Cwd,
		"adapterType": a.AdapterType,
		"cmd":         a.Cmd,
		"args":        a.Args,
		"env":         a.Env,
		"tags":        a.Tags,
		"portHints":   a.PortHints,
		"healthUrl":   a.HealthURL,
		"scriptHash":  a.ScriptHash,
		"confirmed":   a.Confirmed,
		"createdAt":   a.CreatedAt,
		"lastStartedAt": a.LastStartedAt,
		"lastUrl":     a.LastURL,
		"sortOrder":   a.SortOrder,
		"cardColor":   a.CardColor,
	}
	if a.GroupID != nil {
		row["groupId"] = *a.GroupID
	}
	// 运行态优先于缓存状态
	status := a.LastStatus
	runID := ""
	pid := 0
	if rt, ok := s.Manager.Registry.Get(a.ID); ok {
		status = rt.GetStatus()
		runID = rt.RunID
		pid = rt.PID
	}
	row["status"] = status
	row["runId"] = runID
	row["pid"] = pid
	// 多服务列表（项目下所有发现的端口服务）
	services, _ := s.Store.ListServicesByApp(a.ID)
	if services == nil {
		services = []*store.AppService{}
	}
	row["services"] = services
	return row
}

// ----- 操作 -----

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ConfirmedScriptHash string `json:"confirmedScriptHash"`
	}
	// body 可选：未传 body 时忽略
	_ = readJSONOptional(r, &body)

	// 启动前预检：校验脚本哈希、必要时同步派生字段或要求确认
	outcome, err := s.runPreflight(w, id, body.ConfirmedScriptHash)
	if err != nil || outcome == outcomeAbort {
		return // runPreflight 已写响应
	}
	if err := s.Launcher.Start(context.Background(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.startResponse(id, outcome, "started"))
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.Launcher.Stop(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

// POST /api/apps/stop-all 停止所有正在运行的项目（退出前清理用）。
func (s *Server) handleStopAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	stopped := s.Launcher.StopAll()
	writeJSON(w, http.StatusOK, map[string]int{"stopped": stopped})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ConfirmedScriptHash string `json:"confirmedScriptHash"`
	}
	_ = readJSONOptional(r, &body)

	// 重启预检：必须在停止旧进程之前完成（确认/同步不通过就不动旧进程）。
	outcome, err := s.runPreflight(w, id, body.ConfirmedScriptHash)
	if err != nil || outcome == outcomeAbort {
		return
	}
	if err := s.Launcher.Restart(context.Background(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.startResponse(id, outcome, "restarted"))
}

// GET /api/apps/{id}/logs?since=<id>&limit=<n>&keyword=<kw>
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// 取最近 run
	run, err := s.Store.GetLatestRun(id)
	if err != nil || run == nil {
		writeJSON(w, http.StatusOK, map[string]any{"logs": []any{}, "runId": ""})
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit := 500
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	keyword := r.URL.Query().Get("keyword")
	var logs []*store.LogEntry
	if keyword != "" {
		logs, err = s.Store.SearchLogs(run.ID, keyword, limit)
	} else {
		logs, err = s.Store.RecentLogs(run.ID, since, limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []*store.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "runId": run.ID})
}

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	run, _ := s.Store.GetLatestRun(id)
	if run == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	ports, _ := s.Store.ListPorts(run.ID)
	if ports == nil {
		ports = []*store.PortEntry{}
	}
	writeJSON(w, http.StatusOK, ports)
}

// POST /api/apps/{id}/open-url { url? } 用系统默认浏览器打开（优先 app.last_url 或 body.url）。
func (s *Server) handleOpenURL(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = readJSON(r, &body)
	url := body.URL
	if url == "" {
		url = a.LastURL
	}
	if url == "" {
		writeError(w, http.StatusBadRequest, "no url available")
		return
	}
	if err := openExternal(url); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"opened": url})
}

// POST /api/apps/{id}/open-dir 用资源管理器打开工作目录。
func (s *Server) handleOpenDir(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	dir := a.Cwd
	if dir == "" {
		dir = filepath.Dir(a.EntryScript)
	}
	if err := openExternal(dir); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"opened": dir})
}

// PATCH /api/apps/{id}/services/{sid}/role { role: "frontend" }
// 手动设置服务角色，锁定为 manual（自动识别不再覆盖）。
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
	if err != nil || svc == nil || svc.AppID != appID {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	if err := s.Store.SetServiceRole(sid, body.Role); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.broadcastServices(appID)
	writeJSON(w, http.StatusOK, map[string]string{"role": body.Role, "roleSource": store.RoleSourceManual})
}

// POST /api/apps/{id}/services/{sid}/reidentify
// 强制重新识别：重置为 auto，用端口 + HTTP 响应头重新 classify。
func (s *Server) handleServiceReidentify(w http.ResponseWriter, r *http.Request, appID, sid string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	svc, err := s.Store.GetService(sid)
	if err != nil || svc == nil || svc.AppID != appID {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	if err := s.Store.ResetServiceRoleToAuto(sid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 同步重新探测：端口 + 响应头 + Title/CT。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hr := probe.CheckRoot(ctx, svc.URL)
	headers := map[string]string{}
	var title string
	var body string
	if hr != nil {
		if hr.Server != "" {
			headers["Server"] = hr.Server
		}
		if hr.PoweredBy != "" {
			headers["X-Powered-By"] = hr.PoweredBy
		}
		title = hr.Title
		body = hr.Body
	}
	logs, _ := s.Store.SearchLogs(svc.AppRunID, strconv.Itoa(svc.Port), 20)
	logHints := make([]string, 0, len(logs))
	for _, entry := range logs {
		logHints = append(logHints, entry.Text)
	}
	var declaredRole probe.Role
	if a, _ := s.Store.GetApp(appID); a != nil {
		declaredRole = probe.DeclaredRoles(a.EntryScript)[svc.Port]
	}
	role, _ := probe.Classify(probe.ClassifyInput{
		Port:         svc.Port,
		DeclaredRole: declaredRole,
		Headers:      headers,
		Title:        title,
		BodyCT:       contentTypeOf(hr),
		Body:         body,
		LogHints:     logHints,
	})
	if _, err := s.Store.UpdateServiceRoleIfAuto(sid, string(role)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.broadcastServices(appID)
	writeJSON(w, http.StatusOK, map[string]string{"role": string(role), "roleSource": store.RoleSourceAuto})
}

// contentTypeOf 取 HealthResult 的 Content-Type，nil 安全。
func contentTypeOf(hr *probe.HealthResult) string {
	if hr == nil {
		return ""
	}
	return hr.ContentType
}

// broadcastServices 广播某 app 的最新 services 列表（复用 app:services）。
func (s *Server) broadcastServices(appID string) {
	if s.Hub == nil {
		return
	}
	svcs, err := s.Store.ListServicesByApp(appID)
	if err != nil {
		return
	}
	run, _ := s.Store.GetLatestRun(appID)
	runID := ""
	if run != nil {
		runID = run.ID
	}
	s.Hub.BroadcastServices(appID, runID, svcs)
}

// openExternal 跨平台打开 URL 或目录。
func openExternal(target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.Command("open", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}

// ----- 字符串/切片默认值工具 -----

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func defaultArgs(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

func defaultEnv(e map[string]string) map[string]string {
	if e == nil {
		return map[string]string{}
	}
	return e
}

func defaultTags(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}

func dirOf(p string) string {
	if p == "" {
		return ""
	}
	d := filepath.Dir(p)
	if d == "" || d == "." {
		return ""
	}
	return d
}

func hashOf(path string) (string, error) {
	return hashFile(path)
}

// 占位：app 包别名引用，避免未使用
var _ = app.StatusStopped
var _ = strings.TrimSpace
