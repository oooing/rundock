package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/store"
)

// GET|POST /api/groups ; /api/groups/{id} PATCH/DELETE
func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		gs, err := s.Store.ListGroups()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if gs == nil {
			gs = []*store.Group{}
		}
		writeJSON(w, http.StatusOK, gs)
	case http.MethodPost:
		var body struct {
			Name  string   `json:"name"`
			Color string   `json:"color"`
			Order []string `json:"order"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		g := &store.Group{ID: app.NewID(), Name: body.Name, Color: body.Color, Order: body.Order}
		if g.Order == nil {
			g.Order = []string{}
		}
		if err := s.Store.CreateGroup(g); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, g)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleGroupDetail(w http.ResponseWriter, r *http.Request) {
	id := pathIDAfter(r.URL.Path, "/api/groups/")
	if id == "" {
		writeError(w, http.StatusNotFound, "group id required")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var body struct {
			Name  *string   `json:"name"`
			Color *string   `json:"color"`
			Order *[]string `json:"order"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		gs, err := s.Store.ListGroups()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var g *store.Group
		for _, x := range gs {
			if x.ID == id {
				g = x
			}
		}
		if g == nil {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		if body.Name != nil {
			g.Name = *body.Name
		}
		if body.Color != nil {
			g.Color = *body.Color
		}
		if body.Order != nil {
			g.Order = *body.Order
		}
		if err := s.Store.UpdateGroup(g); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, g)
	case http.MethodDelete:
		if err := s.Store.DeleteGroup(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// GET|PATCH /api/settings
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		m, err := s.Store.AllSettings()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m)
	case http.MethodPatch:
		var body map[string]string
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		for k, v := range body {
			if err := s.Store.SetSetting(k, v); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]bool{"updated": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// GET /api/export -> 导出 apps + groups + settings 的 JSON 快照。
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	apps, _ := s.Store.ListApps()
	groups, _ := s.Store.ListGroups()
	settings, _ := s.Store.AllSettings()
	profiles, _ := s.Store.ListReleaseProfiles()
	if profiles == nil {
		profiles = []*store.ReleaseProfile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exportedAt":      time.Now().UTC().Format(time.RFC3339),
		"apps":            apps,
		"groups":          groups,
		"settings":        settings,
		"releaseProfiles": profiles,
	})
}

// POST /api/import-config -> 导入 JSON 快照（合并：按 id upsert）。
func (s *Server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var snap struct {
		Apps            []*store.App            `json:"apps"`
		Groups          []*store.Group          `json:"groups"`
		Settings        map[string]string       `json:"settings"`
		ReleaseProfiles []*store.ReleaseProfile `json:"releaseProfiles"`
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&snap); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	appCount, groupCount := 0, 0
	for _, a := range snap.Apps {
		if a.ID == "" {
			continue
		}
		// 若已存在则更新，否则创建
		exist, _ := s.Store.GetApp(a.ID)
		if exist != nil {
			_ = s.Store.UpdateApp(a)
		} else {
			_ = s.Store.CreateApp(a)
		}
		appCount++
	}
	for _, g := range snap.Groups {
		if g.ID == "" {
			continue
		}
		_ = s.Store.CreateGroup(g)
		groupCount++
	}
	for k, v := range snap.Settings {
		_ = s.Store.SetSetting(k, v)
	}
	for _, p := range snap.ReleaseProfiles {
		if p != nil && p.AppID != "" {
			if a, _ := s.Store.GetApp(p.AppID); a != nil {
				_ = s.Publisher.SaveProfile(p)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"apps": appCount, "groups": groupCount})
}

// pathIDAfter 取 /prefix/{id} 的 id（无子路径）。
func pathIDAfter(fullPath, prefix string) string {
	rest := fullPath
	if len(rest) >= len(prefix) {
		rest = rest[len(prefix):]
	}
	if rest == "" {
		return ""
	}
	return rest
}
