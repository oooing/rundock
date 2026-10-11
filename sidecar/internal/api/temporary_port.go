package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/launcher-sidecar/internal/portoverride"
	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

type temporaryPortOffer struct {
	Supported         bool   `json:"supported"`
	Reason            string `json:"reason,omitempty"`
	OriginalPort      int    `json:"originalPort,omitempty"`
	Port              int    `json:"port,omitempty"`
	ConfirmationToken string `json:"confirmationToken,omitempty"`
	ExpiresAt         string `json:"expiresAt,omitempty"`
}

type temporaryPortPlan struct {
	AppID, Origin, Fingerprint string
	Port                       int
	Expires                    time.Time
}

func temporaryFingerprint(a *store.App, hash string, d *portoverride.Definition) string {
	return recovery.Fingerprint(struct {
		Config, Hash string
		Definition   *portoverride.Definition
	}{launchFingerprint(a), hash, d})
}

// Bind to ask the OS for a candidate. It cannot stay reserved while Vite binds;
// confirmation rechecks it, and --strictPort makes any final race fail closed.
func availableTemporaryPort(d *portoverride.Definition) (int, error) {
	for i := 0; i < 16; i++ {
		listener, err := net.Listen("tcp4", net.JoinHostPort(d.Host, "0"))
		if err != nil {
			return 0, err
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		if port == d.Port || port < 1024 {
			continue
		}
		denied, err := recovery.ProbeBindings([]recovery.PortBinding{{Port: port, Network: "tcp4", Host: d.Host}})
		if err == nil && len(denied) == 0 {
			return port, nil
		}
	}
	return 0, fmt.Errorf("暂时找不到可绑定的空闲端口")
}

// Called under startupMu. No close/stop action belongs to this path.
func (s *Server) offerTemporaryPort(r *http.Request, id string, state string) *temporaryPortOffer {
	offer := &temporaryPortOffer{}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		offer.Reason = "项目配置无法读取"
		return offer
	}
	d, err := portoverride.Inspect(a)
	if err != nil {
		offer.Reason = err.Error()
		return offer
	}
	offer.Supported, offer.OriginalPort = true, d.Port
	if state != "conflict" && state != "reserved" {
		offer.Reason = "当前无需临时更换端口"
		return offer
	}
	hash, err := hashOf(a.EntryScript)
	if err != nil || hash != a.ScriptHash {
		offer.Reason = "启动脚本已变化，请先核对启动配置；临时启动不会自动改写原配置"
		return offer
	}
	if s.temporaryPortPlans == nil {
		s.temporaryPortPlans = map[string]temporaryPortPlan{}
	}
	for token, plan := range s.temporaryPortPlans {
		if plan.AppID == id || !plan.Expires.After(time.Now()) {
			delete(s.temporaryPortPlans, token)
		}
	}
	if len(s.temporaryPortPlans) >= 128 {
		offer.Reason = "临时端口请求过多，请稍后重试"
		return offer
	}
	port, err := availableTemporaryPort(d)
	if err != nil {
		offer.Reason = "暂时找不到可绑定的空闲端口；未关闭任何程序"
		return offer
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		offer.Reason = "无法生成临时启动确认，请稍后重试"
		return offer
	}
	token, expires := hex.EncodeToString(random[:]), time.Now().Add(60*time.Second)
	s.temporaryPortPlans[token] = temporaryPortPlan{AppID: id, Origin: r.Header.Get("Origin"), Fingerprint: temporaryFingerprint(a, hash, d), Port: port, Expires: expires}
	offer.Port, offer.ConfirmationToken, offer.ExpiresAt = port, token, expires.UTC().Format(time.RFC3339)
	return offer
}

func (s *Server) handleTemporaryPortStart(w http.ResponseWriter, r *http.Request, id string) {
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
		writeError(w, 400, "请先查看临时端口方案")
		return
	}
	plan, ok := s.temporaryPortPlans[body.ConfirmationToken]
	delete(s.temporaryPortPlans, body.ConfirmationToken)
	if !ok || plan.AppID != id || plan.Origin != r.Header.Get("Origin") || !plan.Expires.After(time.Now()) {
		writeError(w, 409, "临时端口方案已失效，请重新查看；未关闭任何程序")
		return
	}
	a, err := s.Store.GetApp(id)
	if err != nil || a == nil {
		writeError(w, 409, "项目配置无法读取；未关闭任何程序")
		return
	}
	d, err := portoverride.Inspect(a)
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	hash, err := hashOf(a.EntryScript)
	if err != nil || hash != a.ScriptHash || plan.Fingerprint != temporaryFingerprint(a, hash, d) {
		writeError(w, 409, "启动配置或端口适配声明已变化，请重新查看；原配置未修改")
		return
	}
	// Require fresh ownership evidence, without terminating any old owner.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	result, _, _, err := s.portEvidence(ctx, id)
	cancel()
	if err != nil || result.State == "unknown" || result.State == "running" || s.Manager.Registry.IsRunning(id) {
		writeError(w, 409, "无法确认项目可以启动，请重新查看；未关闭任何程序")
		return
	}
	denied, err := recovery.ProbeBindings([]recovery.PortBinding{{Port: plan.Port, Network: "tcp4", Host: d.Host}})
	if err != nil || len(denied) > 0 {
		writeError(w, 409, "临时端口已不可用，请重新选择；未关闭任何程序")
		return
	}
	if err = s.Launcher.StartTemporary(context.Background(), id, plan.Port); err != nil {
		writeError(w, 400, "临时启动失败；未关闭任何占用程序："+err.Error())
		return
	}
	writeJSON(w, 200, s.startResponse(id, outcomePass, "started"))
}
