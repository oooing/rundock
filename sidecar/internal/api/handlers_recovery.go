package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/launcher-sidecar/internal/probe"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

func (s *Server) startupIssue(id string) (*recovery.Issue, []recovery.Process, error) {
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		return nil, nil, fmt.Errorf("app not found")
	}
	status := a.LastStatus
	if rt, ok := s.Manager.Registry.Get(id); ok {
		status = rt.GetStatus()
	}
	if status != "failed" {
		return nil, nil, nil
	}
	run, err := s.Store.GetLatestRun(id)
	if err != nil || run == nil {
		return nil, nil, err
	}
	logs, err := s.Store.SearchLogs(run.ID, "", 500)
	if err != nil {
		return nil, nil, err
	}
	lines := []string{}
	for _, l := range logs {
		if l.Stream != "event" {
			lines = append(lines, l.Text)
		}
	}
	ports := recovery.PortsFromLogs(lines)
	issue := &recovery.Issue{Code: "startup_failed", RunID: run.ID, Ports: ports, Conflicts: []recovery.Conflict{}}
	if len(ports) == 0 {
		return issue, nil, nil
	}
	issue.Code = "port_in_use"
	processes, err := recovery.Snapshot()
	if err != nil {
		issue.Reason = err.Error()
		return issue, nil, nil
	}
	byID := map[int]recovery.Process{}
	for _, p := range processes {
		byID[p.PID] = p
	}
	protected := map[int]bool{0: true, 4: true}
	// Never terminate the serving backend, its ancestors, or another managed application's process tree.
	for pid := os.Getpid(); pid > 0 && !protected[pid]; {
		protected[pid] = true
		pid = byID[pid].ParentPID
	}
	tree := map[int]bool{os.Getpid(): true}
	for _, rt := range s.Manager.Registry.All() {
		if rt.AppID != id && rt.PID > 4 {
			tree[rt.PID] = true
		}
	}
	for pass := 0; pass < len(processes); pass++ {
		changed := false
		for _, p := range processes {
			if tree[p.ParentPID] && !tree[p.PID] {
				tree[p.PID] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for pid := range tree {
		protected[pid] = true
	}
	peers, err := s.Store.ListApps()
	if err != nil {
		return nil, nil, err
	}
	belongs := func(p recovery.Process) bool {
		if protected[p.PID] || !recovery.Belongs(a.Cwd, p) {
			return false
		}
		for _, peer := range peers {
			if peer.ID != id && peer.Cwd != a.Cwd && recovery.Belongs(peer.Cwd, p) {
				return false
			}
		}
		return true
	}
	required := map[int]bool{}
	for _, p := range ports {
		required[p] = true
	}
	candidates := map[int]bool{}
	for p := range required {
		candidates[p] = true
	}
	for _, p := range a.PortHints {
		candidates[p] = true
	}
	for p := range probe.DeclaredRoles(a.EntryScript) {
		candidates[p] = true
	}
	targets := []recovery.Process{}
	seen := map[int]bool{}
	issue.CanRecover = true
	listeners := probe.SnapshotListeners()
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].Port == listeners[j].Port {
			return listeners[i].PID < listeners[j].PID
		}
		return listeners[i].Port < listeners[j].Port
	})
	seenConflict := map[string]bool{}
	for _, l := range listeners {
		if !candidates[l.Port] {
			continue
		}
		p := byID[l.PID]
		safe := belongs(p)
		if !required[l.Port] && !safe {
			continue
		}
		key := fmt.Sprintf("%d/%d", l.Port, l.PID)
		if seenConflict[key] {
			continue
		}
		seenConflict[key] = true
		issue.Conflicts = append(issue.Conflicts, recovery.Conflict{Port: l.Port, PID: l.PID, Name: filepath.Base(p.Executable), Safe: safe})
		if !safe {
			issue.CanRecover = false
			issue.Reason = "占用进程不属于本项目或受保护，请先手动关闭或修改端口"
			continue
		}
		if !seen[p.PID] {
			targets = append(targets, p)
			seen[p.PID] = true
		}
	}
	issue.Fingerprint = recovery.Fingerprint(struct {
		Run       string
		App       *store.App
		Conflicts []recovery.Conflict
		Processes []recovery.Process
	}{run.ID, a, issue.Conflicts, targets})
	return issue, targets, nil
}

func (s *Server) handleStartupIssue(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	issue, _, err := s.startupIssue(id)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, issue)
}

func (s *Server) handleRecoverPorts(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !s.startupMu.TryLock() {
		writeError(w, 409, "已有启停操作进行中，请稍后重试")
		return
	}
	defer s.startupMu.Unlock()
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if readJSON(r, &body) != nil || body.Fingerprint == "" {
		writeError(w, 400, "请先检查端口占用")
		return
	}
	issue, targets, err := s.startupIssue(id)
	if err != nil || issue == nil || issue.Code != "port_in_use" {
		writeError(w, 409, "项目状态已变化，请重新检查")
		return
	}
	if !issue.CanRecover {
		writeError(w, 409, issue.Reason)
		return
	}
	if issue.Fingerprint != body.Fingerprint {
		writeError(w, 409, "端口或项目配置已变化，请重新检查")
		return
	}
	// Normal script-risk preflight still applies, before any processes are touched.
	outcome, err := s.runPreflight(w, id, "")
	if err != nil || outcome == outcomeAbort {
		return
	}
	if outcome == outcomeSynced {
		writeError(w, 409, "脚本配置已更新，请重新检查端口后重试")
		return
	}
	for _, p := range targets {
		if err := recovery.Terminate(p); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.Store.InsertLog(issue.RunID, "event", "info", fmt.Sprintf("[端口恢复] 已结束本项目占用进程 PID=%d", p.PID))
	}
	ports := map[int]bool{}
	for _, p := range issue.Ports {
		ports[p] = true
	}
	for _, c := range issue.Conflicts {
		ports[c.Port] = true
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		busy := false
		for _, l := range probe.SnapshotListeners() {
			if ports[l.Port] {
				busy = true
			}
		}
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			writeError(w, 409, "端口仍被占用，未启动项目，请重新检查")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := s.Launcher.Start(context.Background(), id); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.startResponse(id, outcome, "started"))
}
