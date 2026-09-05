package api

import (
	"encoding/json"
	"net/http"

	"github.com/launcher-sidecar/internal/launcher"
	"github.com/launcher-sidecar/internal/store"
)

// preflightOutcome 是 runPreflight 对调用方（handleStart/handleRestart）的简明结论。
type preflightOutcome int

const (
	outcomeAbort  preflightOutcome = -1 // 已写响应（错误或 409），调用方应直接 return
	outcomePass   preflightOutcome = 0  // 哈希未变或已用 confirmedScriptHash 通过，无需特殊提示
	outcomeSynced preflightOutcome = 1  // 自动同步了派生字段，前端需刷新并提示
)

// scriptConfirmationCode 是返回 409 时的错误码（前端据此识别"需要确认"）。
const scriptConfirmationCode = "script_confirmation_required"

// runPreflight 执行启动/重启前的脚本预检，并按结果写响应。
//   - 文件/扫描失败、danger 未确认 → 写响应并返回 outcomeAbort
//   - 需要确认 → 写 409 { code, candidate } 并返回 outcomeAbort
//   - 同步/通过 → 不写响应，返回 outcomeSynced / outcomePass
//
// 调用方在 outcome != outcomeAbort 时继续执行 Launcher.Start/Restart。
func (s *Server) runPreflight(w http.ResponseWriter, id, confirmedScriptHash string) (preflightOutcome, error) {
	res, err := s.Launcher.Preflight(id, confirmedScriptHash)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return outcomeAbort, err
	}
	switch res.Outcome {
	case launcher.PreflightConfirm:
		// 需要用户确认：返回最新候选（含 findings），不写库
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":      scriptConfirmationCode,
			"candidate": res.Candidate,
		})
		return outcomeAbort, nil
	case launcher.PreflightSynced:
		return outcomeSynced, nil
	case launcher.PreflightPass:
		if res.ConfigUpdated {
			return outcomeSynced, nil
		}
		return outcomePass, nil
	}
	// 不应到达
	writeError(w, http.StatusInternalServerError, "unknown preflight outcome")
	return outcomeAbort, nil
}

// startResponse 拼装启动/重启成功响应。
//   - outcomeSynced → configUpdated=true，附最新 appView 供前端刷新
//   - 其它 → 经典的 { started: true } / { restarted: true }（保持向后兼容）
//
// action 由调用方决定（"started" / "restarted"），仅决定哪个布尔键为 true。
func (s *Server) startResponse(id string, outcome preflightOutcome, action string) map[string]any {
	base := map[string]any{
		"started":       action == "started",
		"restarted":     action == "restarted",
		"configUpdated": false,
	}
	if outcome == outcomeSynced {
		// 同步后回读最新 App，拼成 appView
		if a, err := s.Store.GetApp(id); err == nil && a != nil {
			base["configUpdated"] = true
			base["app"] = appView(a, s)
		}
	}
	return base
}

// readJSONOptional 与 readJSON 类似，但允许空 body / 未知字段。
// 启动/重启接口的 body 是可选的（不带 body 也算合法）。
func readJSONOptional(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	// 没有 Content-Length 视为无 body
	if r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	// 兼容老前端：未知字段（如 confirmedScriptHash 未传时其它字段）忽略
	return dec.Decode(v)
}

// 占位引用，避免 store 包未使用（本文件目前未直接用 store）。
var _ = (*store.App)(nil)
