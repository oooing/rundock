// launcher 包的预检（preflight）逻辑：在真正启动/重启前，校验入口脚本的哈希，
// 并据此自动同步脚本派生字段或要求用户对危险变更进行确认。
//
// 决策矩阵（见 plan）：
//   - 文件不存在 / 扫描失败        → 阻止操作，保留旧配置，返回错误
//   - 哈希未变                      → 直接放行
//   - 哈希变化但仅 info/warn        → 自动同步派生字段（cwd/adapter/cmd/args/env/portHints/scriptHash）
//   - 哈希变化且含 danger           → 阻止操作，不写库，要求用户确认
//   - 用户已携带 confirmedScriptHash → 重新校验哈希是否仍匹配；
//                                      期间脚本再次变化则再次返回需确认
//
// 用户字段（name / groupId / tags / healthUrl / sortOrder / 手动服务角色）一律保留，
// 仅覆盖脚本派生字段。ConfirmedHash 仅在用户成功确认 danger 后写入。
package launcher

import (
	"fmt"

	"github.com/launcher-sidecar/internal/importer"
	"github.com/launcher-sidecar/internal/security"
	"github.com/launcher-sidecar/internal/store"
)

// PreflightOutcome 预检的结论。
type PreflightOutcome int

const (
	// PreflightPass 哈希未变或已用 confirmedScriptHash 通过：可直接启动。
	PreflightPass PreflightOutcome = iota
	// PreflightSynced 哈希变化但仅 info/warn：已自动同步派生字段并落库。
	PreflightSynced
	// PreflightConfirm 哈希变化且含 danger：需用户确认，不写库。
	PreflightConfirm
)

// PreflightResult 预检结果。
type PreflightResult struct {
	Outcome      PreflightOutcome
	Candidate    *importer.Candidate // PreflightConfirm 时带最新候选（含 findings）供前端展示
	ConfigUpdated bool               // PreflightSynced=true 时为 true（前端据此刷新）
	App          *store.App          // 同步后的最新 App（便于上层拼装 appView）
}

// Preflight 在 Start/Restart 前执行脚本哈希与风险校验。
//   - appID：目标应用
//   - confirmedScriptHash：前端"已确认危险"后回带的哈希；为空表示未确认
//
// 调用方约定：danger 阻止时返回 (result, nil)，由调用方决定如何把 Candidate 推给前端；
// 真正的 IO/解析错误（文件不存在、扫描失败）以 error 返回。
func (l *Launcher) Preflight(appID, confirmedScriptHash string) (*PreflightResult, error) {
	a, err := l.Store.GetApp(appID)
	if err != nil {
		return nil, fmt.Errorf("get app: %w", err)
	}
	if a == nil {
		return nil, fmt.Errorf("app not found: %s", appID)
	}

	// 重新扫描脚本，得到最新候选（含新哈希）
	cand, err := importer.Import(a.EntryScript, l.Registry)
	if err != nil {
		// 文件不存在 / 解析失败：阻止操作，保留旧配置
		return nil, fmt.Errorf("脚本预检失败：%w", err)
	}

	newHash := cand.ScriptHash
	// 哈希缺失视为脚本不可读（importer 内部已应返回 error，这里兜底）
	if newHash == "" {
		return nil, fmt.Errorf("无法读取脚本内容（哈希为空）：%s", a.EntryScript)
	}

	// 1) 用户回带 confirmedScriptHash：重新校验是否仍匹配
	if confirmedScriptHash != "" {
		if confirmedScriptHash == newHash {
			// 哈希匹配：把 danger 视为"已确认"，更新 ConfirmedHash 并放行
			// （派生字段也可能变了，但用户已基于最新 Candidate 确认，故同步）
			if err := l.applyDerived(a, cand, newHash, true); err != nil {
				return nil, err
			}
			return &PreflightResult{Outcome: PreflightPass, App: a, ConfigUpdated: true}, nil
		}
		// 期间脚本再次变化：再次要求确认（带最新 Candidate）
		return &PreflightResult{Outcome: PreflightConfirm, Candidate: cand, App: a}, nil
	}

	// 2) 哈希未变：直接放行
	if newHash == a.ScriptHash {
		return &PreflightResult{Outcome: PreflightPass, App: a}, nil
	}

	// 3) 哈希变化：按风险等级分流
	if security.HasBlocking(cand.Findings) {
		// danger：不写库，要求确认
		return &PreflightResult{Outcome: PreflightConfirm, Candidate: cand, App: a}, nil
	}

	// 4) info/warn：自动同步派生字段
	if err := l.applyDerived(a, cand, newHash, false); err != nil {
		return nil, err
	}
	return &PreflightResult{Outcome: PreflightSynced, App: a, ConfigUpdated: true}, nil
}

// applyDerived 把候选的脚本派生字段覆盖到 App，并落库。
//   - 派生字段：cwd / adapterType / cmd / args / env / portHints / scriptHash
//   - 保留字段：name / groupId / tags / healthUrl / sortOrder / ConfirmedHash（除非 confirmDanger）
//
// confirmDanger=true 表示用户已确认 danger，此时把 ConfirmedHash 也置为新哈希。
func (l *Launcher) applyDerived(a *store.App, cand *importer.Candidate, newHash string, confirmDanger bool) error {
	// adapter.PrepareOutput 里 cmd 可能为空（首次未 prepare 成功）；
	// 这里只覆盖非空派生字段，避免把已有 cmd 抹掉。
	if cand.Cwd != "" {
		a.Cwd = cand.Cwd
	}
	if cand.AdapterType != "" {
		a.AdapterType = cand.AdapterType
	}
	// cmd/args 总是一起覆盖（Prepare 的产物）；为空则保留旧值
	if cand.Cmd != "" {
		a.Cmd = cand.Cmd
		a.Args = cand.Args
	}
	if cand.Env != nil {
		a.Env = cand.Env
	}
	if cand.PortHints != nil {
		a.PortHints = cand.PortHints
	}
	a.ScriptHash = newHash
	if confirmDanger {
		a.ConfirmedHash = newHash
		a.Confirmed = true
	}
	return l.Store.UpdateApp(a)
}
