package api

import (
	"net/http"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/importer"
	"github.com/launcher-sidecar/internal/store"
)

// GET /api/health -> 健康检查（前端探活 + 判断 sidecar 已就绪）。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// POST /api/import { scriptPath } -> Candidate（候选配置 + 风险），只读不执行。
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ScriptPath string `json:"scriptPath"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if body.ScriptPath == "" {
		writeError(w, http.StatusBadRequest, "scriptPath required")
		return
	}
	cand, err := importer.Import(body.ScriptPath, s.Registry)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cand)
}

// GET /api/apps -> 全部 app 列表（含运行态）。
// POST /api/apps -> 创建 app（确认后）。
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		apps, err := s.Store.ListApps()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 附加运行态
		out := make([]map[string]any, 0, len(apps))
		for _, a := range apps {
			row := appView(a, s)
			out = append(out, row)
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var body createAppBody
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		a := &store.App{
			ID:          app.NewID(),
			Name:        body.Name,
			EntryScript: body.EntryScript,
			Cwd:         body.Cwd,
			AdapterType: defaultStr(body.AdapterType, "batch"),
			Cmd:         body.Cmd,
			Args:        defaultArgs(body.Args),
			Env:         defaultEnv(body.Env),
			Tags:        defaultTags(body.Tags),
			GroupID:     body.GroupID,
			PortHints:   body.PortHints,
			HealthURL:   body.HealthURL,
			ScriptHash:  body.ScriptHash,
			// 创建即视为已确认（用户在确认卡确认后才会 POST 创建）。
			Confirmed:     true,
			ConfirmedHash: body.ScriptHash,
			LastStatus:    app.StatusStopped,
			SortOrder:     body.SortOrder,
		}
		if a.Cwd == "" {
			a.Cwd = dirOf(a.EntryScript)
		}
		if err := s.Store.CreateApp(a); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, appView(a, s))

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// /api/apps/{id} 子路由：GET/PATCH/DELETE + start/stop/restart/open-url/open-dir/logs。
func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	id, rest := pathTail("/api/apps/", r.URL.Path)
	if id == "" {
		writeError(w, http.StatusNotFound, "app id required")
		return
	}
	if rest == "" {
		s.handleAppRoot(w, r, id)
		return
	}
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
		writeError(w, http.StatusNotFound, "unknown subpath: "+rest)
	}
}

func (s *Server) handleAppRoot(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		a, err := s.Store.GetApp(id)
		if err != nil || a == nil {
			writeError(w, http.StatusNotFound, "app not found")
			return
		}
		writeJSON(w, http.StatusOK, appView(a, s))

	case http.MethodPatch:
		var body updateAppBody
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		a, err := s.Store.GetApp(id)
		if err != nil || a == nil {
			writeError(w, http.StatusNotFound, "app not found")
			return
		}
		applyUpdate(a, &body)
		// 若脚本路径或内容变了，重新计算哈希并要求重新确认
		if body.EntryScript != nil && *body.EntryScript != "" {
			if h, err := hashOf(*body.EntryScript); err == nil {
				if a.ScriptHash != h {
					a.ScriptHash = h
					a.Confirmed = false
					a.ConfirmedHash = ""
				}
			}
		}
		if err := s.Store.UpdateApp(a); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, appView(a, s))

	case http.MethodDelete:
		// 运行中先停止
		if s.Manager.Registry.IsRunning(id) {
			_ = s.Launcher.Stop(id)
		}
		if err := s.Store.DeleteApp(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
