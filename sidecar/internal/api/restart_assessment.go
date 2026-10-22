package api

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/launcher-sidecar/internal/recovery"
	"github.com/launcher-sidecar/internal/store"
)

type restartBlock struct {
	Code, Message string
	Steps         []string
}

func (e *restartBlock) Error() string { return e.Message }
func blockedRestart(code, message string, steps ...string) error {
	return &restartBlock{code, message, steps}
}

func applyRestartBlock(v *restartView, err error) {
	v.Message = err.Error()
	var block *restartBlock
	if errors.As(err, &block) {
		v.ReasonCode, v.NextSteps = block.Code, block.Steps
		return
	}
	v.ReasonCode = "permission_or_identity"
	v.NextSteps = []string{"请核对列出的进程和所属用户，在对应开发工具中停止项目后，点击“重新检查”。", "若原启动窗口已关闭，请在任务管理器的“详细信息”中核对 PID 和命令行；不要按端口或程序名批量结束。"}
}

func selectRestartRoot(a *store.App, snap recovery.RuntimeSnapshot, byPID map[int]recovery.Process) (recovery.Process, error) {
	var roots []recovery.Process
	for _, p := range snap.Processes {
		if recovery.IsEntryProcess(p, a.EntryScript) {
			roots = append(roots, p)
		}
	}
	if len(roots) == 1 {
		return roots[0], nil
	}
	if len(roots) > 1 {
		return recovery.Process{}, blockedRestart("multiple_launches", "发现多个启动实例，无法确定要重启哪一个。", "请在各自的终端或开发工具中停止多余实例，再点击“重新检查”。")
	}
	// Recover an orphaned worker only when its actual script lies in this
	// project. Never promote a PID solely because it listens on a project port.
	owned := recovery.OwnedProcesses(a, snap)
	parentAlive := false
	for _, p := range snap.Processes {
		if !owned[p.PID] || recovery.DirectWorkerEntry(p, a.Cwd) == "" {
			continue
		}
		parent, exists := byPID[p.ParentPID]
		if exists {
			born, childErr := strconv.ParseUint(p.Created, 10, 64)
			prior, parentErr := strconv.ParseUint(parent.Created, 10, 64)
			// Missing parent identity is ambiguous, not proof of an orphan.
			if childErr != nil || parentErr != nil || prior == 0 || born == 0 || prior <= born {
				parentAlive = true
				continue
			}
		}
		// A detached background task with no server is not a service to take over.
		serves := false
		for _, listener := range snap.Listeners {
			if ancestryContains(listener.PID, p.PID, byPID) {
				serves = true
			}
		}
		if serves {
			roots = append(roots, p)
		}
	}
	if len(roots) == 1 {
		return roots[0], nil
	}
	if len(roots) > 1 {
		return recovery.Process{}, blockedRestart("independent_services", "项目有多个独立服务，无法作为一次完整启动接管。", "请从原开发工具停止这些服务，再通过项目卡片统一启动。")
	}
	if parentAlive {
		return recovery.Process{}, blockedRestart("active_launcher", "服务仍由外部启动器运行，无法确认启动器是否还管理其他任务。", "请先在原终端或开发工具中停止项目，避免它自动拉起服务；随后点击“重新检查”。")
	}
	return recovery.Process{}, blockedRestart("unverified_entry", "没有找到可独立接管的启动进程。", "请核对卡片的启动文件与实际运行方式是否一致。", "若原启动窗口已关闭，请在任务管理器的“详细信息”中核对 PID 和命令行后退出项目，再点击“重新检查”。")
}

func checkOtherManagedTargets(targets []recovery.Process, snap recovery.RuntimeSnapshot, appID string) error {
	for _, p := range targets {
		for _, owner := range snap.ManagedRoots {
			if owner.AppID != appID && owner.PID == p.PID && owner.Created == p.Created {
				return blockedRestart("other_managed_project", fmt.Sprintf("PID %d 已由另一个项目托管。", p.PID), "请从原项目卡片停止或重启；这里不会重复接管。")
			}
		}
	}
	return nil
}
