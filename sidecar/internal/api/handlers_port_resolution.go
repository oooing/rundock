package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/launcher-sidecar/internal/recovery"
)

func (s *Server) handlePortResolution(w http.ResponseWriter, r *http.Request, id string) {
	if !s.allowLocalOperation(w, r) {
		return
	}
	if r.Method != http.MethodPost {
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
	result, _, fingerprint, err := s.portEvidence(ctx, id)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if s.portPlans == nil {
		s.portPlans = map[string]portPlan{}
	}
	for token, p := range s.portPlans {
		if !p.Expires.After(time.Now()) || p.AppID == id {
			delete(s.portPlans, token)
		}
	}
	if result.CanResolve {
		if len(s.portPlans) >= 128 {
			writeError(w, 429, "端口确认请求过多，请稍后重试")
			return
		}
		var random [32]byte
		if _, err = rand.Read(random[:]); err != nil {
			writeError(w, 500, "无法生成端口确认，请重试")
			return
		}
		token := hex.EncodeToString(random[:])
		expires := time.Now().Add(60 * time.Second)
		s.portPlans[token] = portPlan{AppID: id, Origin: r.Header.Get("Origin"), Fingerprint: fingerprint, Expires: expires}
		result.ConfirmationToken, result.ExpiresAt = token, expires.UTC().Format(time.RFC3339)
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleResolvePorts(w http.ResponseWriter, r *http.Request, id string) {
	if !s.allowLocalOperation(w, r) {
		return
	}
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
		ConfirmationToken string `json:"confirmationToken"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if readJSON(r, &body) != nil || body.ConfirmationToken == "" {
		writeError(w, 400, "请先查看并确认占用程序")
		return
	}
	plan, ok := s.portPlans[body.ConfirmationToken]
	delete(s.portPlans, body.ConfirmationToken) // Single-use even when a later check rejects it.
	if !ok || plan.AppID != id || plan.Origin != r.Header.Get("Origin") || !plan.Expires.After(time.Now()) {
		writeError(w, 409, "确认已失效，请重新查看占用程序")
		return
	}
	// Script-risk validation precedes every stop. A changed derived config
	// invalidates the plan, rather than silently closing programs for a new script.
	outcome, err := s.runPreflight(w, id, "")
	if err != nil || outcome == outcomeAbort {
		return
	}
	if outcome == outcomeSynced {
		writeError(w, 409, "启动脚本配置已变化，请重新查看占用程序")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	result, targets, fingerprint, err := s.portEvidence(ctx, id)
	cancel()
	if err != nil || !result.CanResolve || plan.Fingerprint != fingerprint || !plan.Expires.After(time.Now()) {
		writeError(w, 409, "端口、程序或项目配置已变化，请重新查看；未关闭任何程序")
		return
	}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		writeError(w, 409, "项目配置无法读取，未关闭任何程序")
		return
	}
	launchConfig := launchFingerprint(a)
	expectedScript, err := hashOf(a.EntryScript)
	if err != nil {
		writeError(w, 409, "启动脚本无法读取，未关闭任何程序")
		return
	}
	for _, target := range targets {
		if target.ManagedID != "" {
			if err := s.validateManagedTarget(target); err != nil {
				writeError(w, 409, err.Error())
				return
			}
		}
		if target.ManagedID == "" {
			if err := recovery.CanCloseExternal(target.Process); err != nil {
				writeError(w, 409, err.Error())
				return
			}
		}
		if !recovery.SameProcess(target.Process) {
			writeError(w, 409, "占用程序已变化，请重新检查；未关闭任何程序")
			return
		}
	}
	// Exact owners were confirmed as a group. Managed runs use their normal stop
	// path; external listeners use identity-checked native handles, never taskkill /t.
	stopped := map[string]bool{}
	for _, target := range targets {
		if err := r.Context().Err(); err != nil {
			writeError(w, 409, "操作已取消，请重新检查端口")
			return
		}
		if target.ManagedID != "" {
			if stopped[target.ManagedID] {
				continue
			}
			if err = s.validateManagedTarget(target); err == nil {
				err = s.Launcher.Stop(target.ManagedID)
			}
			stopped[target.ManagedID] = true
		} else {
			err = recovery.TerminateExternal(target.Process)
		}
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
	}
	bindings := recovery.RequiredBindings(a)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if r.Context().Err() != nil {
			writeError(w, 409, "操作已取消，请重新检查端口")
			return
		}
		denied, bindErr := recovery.ProbeBindings(bindings)
		if bindErr == nil && len(denied) == 0 {
			break
		}
		if time.Now().After(deadline) {
			writeError(w, 409, "端口尚未释放，可能被程序自动重新占用；未启动项目，请重新检查")
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	// Changes may happen while a managed run is stopping. Revalidate launch
	// configuration again before spawning; no user project file is rewritten.
	currentHash, err := hashOf(a.EntryScript)
	if err != nil || currentHash != expectedScript {
		writeError(w, 409, "启动脚本已变化，未启动项目，请重新检查")
		return
	}
	latest, err := s.Store.GetApp(id)
	if err != nil || latest == nil || launchFingerprint(latest) != launchConfig {
		writeError(w, 409, "项目启动配置已变化，未启动项目，请重新检查")
		return
	}
	if err = s.Launcher.Start(context.Background(), id); err != nil {
		writeError(w, 400, fmt.Sprintf("占用程序已关闭，但项目启动失败：%s", err))
		return
	}
	writeJSON(w, 200, s.startResponse(id, outcome, "started"))
}
