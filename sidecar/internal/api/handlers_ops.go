package api

import (
	"context"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/launcher-sidecar/internal/app"
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
	if err := s.Launcher.Start(context.Background(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"started": true})
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

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.Launcher.Restart(context.Background(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"restarted": true})
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
