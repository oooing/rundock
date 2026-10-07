package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

type restartProcess struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}
type restartView struct {
	Kind              string           `json:"kind"`
	CanRestart        bool             `json:"canRestart"`
	Message           string           `json:"message"`
	Processes         []restartProcess `json:"processes"`
	ConfirmationToken string           `json:"confirmationToken,omitempty"`
	ExpiresAt         string           `json:"expiresAt,omitempty"`
}

// A complete, unique launch tree is required. Ambiguous, shared or detached
// processes fail closed instead of acquiring ownership from a listening port.
func restartTree(a *store.App, snap recovery.RuntimeSnapshot, self int) ([]recovery.Process, error) {
	byPID := map[int]recovery.Process{}
	var roots []recovery.Process
	for _, p := range snap.Processes {
		byPID[p.PID] = p
		if recovery.IsEntryProcess(p, a.EntryScript) {
			roots = append(roots, p)
		}
	}
	if len(roots) != 1 {
		return nil, fmt.Errorf("无法唯一确认项目启动进程，请先从原启动窗口退出；不会按端口强制关闭程序")
	}
	root := roots[0]
	var result []recovery.Process
	for _, p := range snap.Processes {
		if !rawDescendant(p.PID, root.PID, byPID) {
			continue
		}
		if !ancestryContains(p.PID, root.PID, byPID) || p.PID <= 4 || p.Created == "" || p.Executable == "" || ancestryContains(self, p.PID, byPID) {
			return nil, fmt.Errorf("进程身份不完整或涉及 RunDock 启动器，无法安全重启")
		}
		if recovery.IsConsoleHost(p) {
			continue
		}
		if p.PID != root.PID && !recovery.IsProjectWorker(p, a.Cwd) {
			return nil, fmt.Errorf("启动进程还包含无法确认归属的程序 %s，请从原启动位置处理；未关闭任何程序", filepath.Base(p.Executable))
		}
		result = append(result, p)
	}
	observation := recovery.InspectRuntime(a, nil, snap)
	if observation.State != "running" || len(observation.Conflicts) > 0 || len(observation.ReservedPorts) > 0 {
		return nil, fmt.Errorf("项目运行状态或端口归属已变化，请重新查看")
	}
	for _, service := range observation.Services {
		for _, listener := range snap.Listeners {
			if listener.Port == service.Port && !ancestryContains(listener.PID, root.PID, byPID) {
				return nil, fmt.Errorf("项目包含独立启动的服务，无法确认完整归属，请从原启动位置处理")
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := strconv.ParseUint(result[i].Created, 10, 64)
		right, _ := strconv.ParseUint(result[j].Created, 10, 64)
		if left == right {
			return result[i].PID < result[j].PID
		}
		return left < right
	})
	// The entry is always terminated first, even for identical creation timestamps.
	for i, p := range result {
		if p.PID == root.PID {
			result[0], result[i] = result[i], result[0]
			break
		}
	}
	return result, nil
}

func rawDescendant(pid, ancestor int, processes map[int]recovery.Process) bool {
	seen := map[int]bool{}
	for pid > 4 && !seen[pid] {
		if pid == ancestor {
			return true
		}
		seen[pid] = true
		p, ok := processes[pid]
		if !ok {
			return false
		}
		pid = p.ParentPID
	}
	return false
}

func (s *Server) RestartRequested() bool { return s.restartRequested.Load() }

func (s *Server) restartEvidence(ctx context.Context, id string) (restartView, []recovery.Process, string, error) {
	v := restartView{Kind: "external", Processes: []restartProcess{}}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		return v, nil, "", fmt.Errorf("项目不存在")
	}
	resume, err := s.Publisher.BeginRestart()
	if err != nil {
		v.Message = err.Error()
		return v, nil, "", nil
	}
	resume()
	if s.Manager.Registry.IsRunning(id) {
		v.Kind = "managed"
		v.Message = "项目已由 RunDock 托管，请使用正常重启"
		return v, nil, "", nil
	}
	if s.runtimeMonitor == nil {
		return v, nil, "", fmt.Errorf("运行状态检测尚未就绪")
	}
	if err = s.runtimeMonitor.refresh(ctx); err != nil {
		return v, nil, "", fmt.Errorf("无法读取当前进程信息：%w", err)
	}
	m := s.runtimeMonitor
	m.mu.RLock()
	snap := m.snapshot
	m.mu.RUnlock()
	var self, parent recovery.Process
	parentPID, _ := strconv.Atoi(os.Getenv("LAUNCHER_SUPERVISOR_PID"))
	for _, p := range snap.Processes {
		if p.PID == os.Getpid() {
			self = p
		}
		if p.PID == parentPID {
			parent = p
		}
	}
	cwd, _ := os.Getwd()
	var targets []recovery.Process
	if recovery.SameProject(a.Cwd, cwd) && recovery.Belongs(a.Cwd, self) {
		v.Kind = "self"
		if parentPID <= 4 || self.ParentPID != parentPID || parent.Created == "" || !recovery.SameProcess(parent) {
			v.Message = "当前 RunDock 由旧启动器启动，尚不支持安全自重启。请退出后用新版启动器打开一次；不会直接结束后台。"
			return v, nil, "", nil
		}
		for _, rt := range s.Manager.Registry.All() {
			if s.Manager.Registry.IsRunning(rt.AppID) {
				v.Message = "还有由 RunDock 托管的项目正在运行，请先停止它们再重启 RunDock，避免中断服务"
				return v, nil, "", nil
			}
		}
		targets = []recovery.Process{self, parent}
		v.Message = "将由独立启动器重启 RunDock 后台。页面会暂时断开，连接恢复后自动更新；不会启动新的发布任务。"
	} else {
		targets, err = restartTree(a, snap, os.Getpid())
		if err != nil {
			v.Message = err.Error()
			return v, nil, "", nil
		}
		// Never take over another managed project's process tree.
		for _, rt := range s.Manager.Registry.All() {
			if s.Manager.Registry.IsRunning(rt.AppID) {
				for _, p := range targets {
					if p.PID == rt.RootPID || p.PID == rt.PID {
						v.Message = "该进程属于另一个已托管项目，请从原项目卡片重启"
						return v, nil, "", nil
					}
				}
			}
		}
		_, closeHandles, e := recovery.OpenRestartGroup(targets)
		if e != nil {
			v.Message = e.Error()
			return v, nil, "", nil
		}
		closeHandles()
		v.Message = "已核实这些进程属于此项目。确认后将关闭它们，再按项目启动脚本重新启动并接管；未保存内容可能丢失。"
	}
	for _, p := range targets {
		v.Processes = append(v.Processes, restartProcess{PID: p.PID, Name: filepath.Base(p.Executable)})
	}
	hash, err := hashOf(a.EntryScript)
	if err != nil {
		return v, nil, "", fmt.Errorf("启动脚本无法读取，未停止任何程序")
	}
	v.CanRestart = true
	fingerprint := recovery.Fingerprint(struct {
		Config, Script, Kind string
		Processes            []recovery.Process
	}{launchFingerprint(a), hash, v.Kind, targets})
	return v, targets, fingerprint, nil
}

func (s *Server) handleRestartPlan(w http.ResponseWriter, r *http.Request, id string) {
	if !s.allowLocalOperation(w, r) {
		return
	}
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, 409, "已有启停操作进行中，请稍后重试")
		return
	}
	defer s.startupMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	v, _, fingerprint, err := s.restartEvidence(ctx, id)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if s.restartPlans == nil {
		s.restartPlans = map[string]portPlan{}
	}
	for token, p := range s.restartPlans {
		if !p.Expires.After(time.Now()) || p.AppID == id {
			delete(s.restartPlans, token)
		}
	}
	if v.CanRestart {
		if len(s.restartPlans) >= 128 {
			writeError(w, 429, "确认请求过多，请稍后重试")
			return
		}
		var secret [32]byte
		if _, err = rand.Read(secret[:]); err != nil {
			writeError(w, 500, "无法生成确认信息")
			return
		}
		token := hex.EncodeToString(secret[:])
		expires := time.Now().Add(60 * time.Second)
		s.restartPlans[token] = portPlan{AppID: id, Origin: r.Header.Get("Origin"), Fingerprint: fingerprint, Expires: expires}
		v.ConfirmationToken = token
		v.ExpiresAt = expires.UTC().Format(time.RFC3339)
	}
	writeJSON(w, 200, v)
}

func (s *Server) handleRestartConfirm(w http.ResponseWriter, r *http.Request, id string) {
	if !s.allowLocalOperation(w, r) {
		return
	}
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, 409, "已有启停操作进行中，请稍后重试")
		return
	}
	selfRestarting := false
	defer func() {
		if !selfRestarting {
			s.startupMu.Unlock()
		}
	}()
	var body struct {
		ConfirmationToken string `json:"confirmationToken"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if readJSON(r, &body) != nil {
		writeError(w, 400, "确认信息无效")
		return
	}
	plan, ok := s.restartPlans[body.ConfirmationToken]
	delete(s.restartPlans, body.ConfirmationToken)
	if !ok || plan.AppID != id || plan.Origin != r.Header.Get("Origin") || !plan.Expires.After(time.Now()) {
		writeError(w, 409, "确认已失效，请重新查看重启信息")
		return
	}
	outcome, err := s.runPreflight(w, id, "")
	if err != nil || outcome == outcomeAbort {
		return
	}
	if outcome == outcomeSynced {
		writeError(w, 409, "启动配置已变化，请重新确认；未停止任何程序")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	v, targets, fingerprint, err := s.restartEvidence(ctx, id)
	if err != nil || !v.CanRestart || fingerprint != plan.Fingerprint || !plan.Expires.After(time.Now()) {
		message := "进程、启动配置或任务状态已变化，请重新确认；未停止任何程序"
		if v.Message != "" && !v.CanRestart {
			message = v.Message
		}
		writeError(w, 409, message)
		return
	}
	resume, err := s.Publisher.BeginRestart()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	keepBlocked := false
	defer func() {
		if !keepBlocked {
			resume()
		}
	}()
	if v.Kind == "self" {
		if !s.closing.CompareAndSwap(false, true) {
			writeError(w, 409, "RunDock 正在退出")
			return
		}
		keepBlocked = true
		// Keep the startup gate closed until exit, including requests which passed
		// the HTTP closing check just before this handler acquired the gate.
		selfRestarting = true
		s.restartRequested.Store(true)
		writeJSON(w, 202, map[string]any{"restarting": true, "instanceId": s.instanceID})
		close(s.shutdownRequested)
		return
	}
	stop, closeHandles, err := recovery.OpenRestartGroup(targets)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	defer closeHandles()
	// Snapshot collection takes time. Recheck configuration after obtaining all
	// handles, before any destructive operation, as well as again before launch.
	current, e := s.Store.GetApp(id)
	if e != nil || current == nil {
		writeError(w, 409, "项目配置无法读取，未停止任何程序")
		return
	}
	currentHash, e := hashOf(current.EntryScript)
	if e != nil || recovery.Fingerprint(struct {
		Config, Script, Kind string
		Processes            []recovery.Process
	}{launchFingerprint(current), currentHash, v.Kind, targets}) != fingerprint {
		writeError(w, 409, "启动配置已变化，请重新确认；未停止任何程序")
		return
	}
	if r.Context().Err() != nil {
		writeError(w, 409, "请求已取消，未停止任何程序")
		return
	}
	if err = stop(); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	// Once stopping has begun, finish the requested restart even if the browser
	// disconnects. BeforeStart rechecks all ports and any respawned processes.
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		writeError(w, 409, "原进程已关闭，但无法读取启动配置")
		return
	}
	hash, err := hashOf(a.EntryScript)
	if err != nil || recovery.Fingerprint(struct {
		Config, Script, Kind string
		Processes            []recovery.Process
	}{launchFingerprint(a), hash, v.Kind, targets}) != fingerprint {
		writeError(w, 409, "原进程已关闭，但启动配置已变化；未启动新实例")
		return
	}
	if err = s.Launcher.Start(context.Background(), id); err != nil {
		writeError(w, 400, "原进程已关闭，但重新启动失败："+err.Error())
		return
	}
	writeJSON(w, 200, s.startResponse(id, outcome, "restarted"))
}
