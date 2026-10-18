package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"

	"github.com/launcher-sidecar/internal/app"
	"github.com/launcher-sidecar/internal/recovery"
)

type updateProject struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RunID         string `json:"runId"`
	ConfigHash    string `json:"configHash"`
	TemporaryPort int    `json:"temporaryPort"`
}
type desktopUpdatePlan struct {
	InstanceID  string          `json:"instanceId"`
	Fingerprint string          `json:"fingerprint"`
	Projects    []updateProject `json:"projects"`
}

func desktopOnly(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		writeError(w, 403, "仅允许本机桌面端操作更新")
		return false
	}
	return true
}
func (s *Server) desktopUpdatePlan() (desktopUpdatePlan, error) {
	p := desktopUpdatePlan{InstanceID: s.instanceID, Projects: []updateProject{}}
	for _, rt := range s.Manager.Registry.All() {
		status := rt.GetStatus()
		if status == app.StatusStarting || status == app.StatusStopping {
			return p, fmt.Errorf("项目正在启动或停止，请稍后安装")
		}
		if !s.Manager.Registry.IsRunning(rt.AppID) {
			continue
		}
		a, err := s.Store.GetApp(rt.AppID)
		if err != nil || a == nil {
			return p, fmt.Errorf("无法读取运行项目，请刷新后重试")
		}
		hash, err := hashOf(a.EntryScript)
		if err != nil {
			return p, fmt.Errorf("无法读取 %s 的启动文件，暂不能保证安装后恢复", a.Name)
		}
		project := updateProject{ID: a.ID, Name: a.Name, RunID: rt.RunID, ConfigHash: recovery.Fingerprint([]string{launchFingerprint(a), hash})}
		if rt.TemporaryPort != nil {
			project.TemporaryPort = rt.TemporaryPort.Port
		}
		p.Projects = append(p.Projects, project)
	}
	sort.Slice(p.Projects, func(i, j int) bool { return p.Projects[i].ID < p.Projects[j].ID })
	p.Fingerprint = recovery.Fingerprint(p)
	return p, nil
}

func (s *Server) handleDesktopUpdatePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	if !desktopOnly(w, r) {
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, 409, "已有启停操作，请稍后安装")
		return
	}
	defer s.startupMu.Unlock()
	resume, err := s.Publisher.BeginRestart()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	defer resume()
	p, err := s.desktopUpdatePlan()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handleDesktopUpdateStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !desktopOnly(w, r) {
		return
	}
	var input struct {
		Fingerprint   string `json:"fingerprint"`
		ConfirmedStop bool   `json:"confirmedStop"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
		writeError(w, 400, "更新确认无效")
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, 409, "已有启停操作，请重新确认")
		return
	}
	releaseStartup := true
	defer func() {
		if releaseStartup {
			s.startupMu.Unlock()
		}
	}()
	resume, err := s.Publisher.BeginRestart()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	releasePublisher := true
	defer func() {
		if releasePublisher {
			resume()
		}
	}()
	plan, err := s.desktopUpdatePlan()
	if err != nil || input.Fingerprint == "" || plan.Fingerprint != input.Fingerprint {
		writeError(w, 409, "运行项目已变化，请重新查看停止清单并确认；尚未停止项目")
		return
	}
	if !s.closing.CompareAndSwap(false, true) {
		writeError(w, 409, "后台正在退出")
		return
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	errors := []string{}
	for _, p := range plan.Projects {
		wg.Add(1)
		go func(p updateProject) {
			defer wg.Done()
			stop := s.Launcher.StopForUpdate
			if input.ConfirmedStop {
				stop = s.Launcher.StopForConfirmedUpdate
			}
			if err := stop(p.ID); err != nil {
				mu.Lock()
				errors = append(errors, p.Name+"："+err.Error())
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	if len(errors) > 0 {
		s.closing.Store(false)
		// The desktop persists the full snapshot before requesting shutdown and will
		// restore successfully stopped projects through the idempotent restore API.
		writeJSON(w, 409, map[string]any{"error": fmt.Sprintf("安装已取消：%v。将恢复已停止的项目。", errors), "restoreRequired": true})
		return
	}
	releaseStartup = false
	releasePublisher = false
	writeJSON(w, 200, map[string]any{"shuttingDown": true})
	close(s.shutdownRequested)
}

func (s *Server) handleDesktopUpdateRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !desktopOnly(w, r) {
		return
	}
	var p updateProject
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&p) != nil || p.ID == "" {
		writeError(w, 400, "恢复记录无效")
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, 409, "已有启停操作，请稍后恢复")
		return
	}
	defer s.startupMu.Unlock()
	if rt, ok := s.Manager.Registry.Get(p.ID); ok && s.Manager.Registry.IsRunning(p.ID) {
		if rt.GetStatus() == app.StatusDegraded {
			writeError(w, 409, "项目仍在运行，但服务状态异常；没有重复启动。请查看项目记录，必要时点击“重启”后再恢复。")
			return
		}
		if rt.GetStatus() == app.StatusStopping {
			writeError(w, 409, "项目尚未停止，暂不能恢复；请等待停止完成后重试。")
			return
		}
		writeJSON(w, 200, map[string]any{"status": rt.GetStatus()})
		return
	}
	a, err := s.Store.GetApp(p.ID)
	if err != nil || a == nil {
		writeError(w, 409, "项目已删除，未自动启动")
		return
	}
	hash, err := hashOf(a.EntryScript)
	if err != nil || p.ConfigHash != recovery.Fingerprint([]string{launchFingerprint(a), hash}) {
		writeError(w, 409, "启动配置或文件已变化，请检查后手动启动")
		return
	}
	start := func() error {
		if p.TemporaryPort != 0 {
			return s.Launcher.StartTemporary(context.Background(), p.ID, p.TemporaryPort)
		}
		return s.Launcher.Start(context.Background(), p.ID)
	}
	if err := start(); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "starting"})
}
