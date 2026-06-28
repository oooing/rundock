// Package proc 负责进程的启动、隐藏窗口托管、停止与进程树回收。
//
// 设计要点（来自调研报告）：
//   - 隐藏窗口：windowsHide / CREATE_NO_WINDOW，不使用 detached（detached 在 Windows 会新开控制台）。
//   - 进程树回收：每个 App run 创建独立 Job Object，设 KILL_ON_JOB_CLOSE，根进程 AssignProcessToObject，
//     子进程自动继承。sidecar 退出时 handle 关闭即全部回收。
//   - 停止分级：Ctrl-Break(grace) -> 等 grace period -> TerminateJobObject -> taskkill /t /f 兜底。
//
// Handle 是跨平台的进程句柄抽象；Job Object 仅在 Windows 生效（非 Windows 退化为普通进程组 kill）。
package proc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// PreparedCommand 描述一条准备好的、可直接执行的命令。
type PreparedCommand struct {
	Cmd  string            // 可执行程序，例如 cmd.exe
	Args []string          // 参数
	Cwd  string            // 工作目录
	Env  map[string]string // 注入的环境变量（与父进程环境合并）
}

// Handle 代表一个被托管的进程句柄。
type Handle struct {
	cmd       *exec.Cmd
	rootPID   int
	jobCloser func() error      // Windows: 关闭 Job Object 句柄；其他平台 nil
	cancel    context.CancelFunc
	pty       interface{}       // Windows ConPTY 会话（*conPTYSession），非 Windows 或普通模式为 nil
}

// IsConPTY 是否运行在 ConPTY 模式下。
func (h *Handle) IsConPTY() bool { return h.pty != nil }

// PID 返回根进程 PID。
func (h *Handle) PID() int { return h.rootPID }

// Close 释放 Job Object 句柄等资源（不会终止进程，仅释放句柄）。
func (h *Handle) Close() error {
	if h.pty != nil {
		// ConPTY 模式：关闭会话资源（不终止进程）
		closeConPTY(h.pty)
	}
	if h.jobCloser != nil {
		return h.jobCloser()
	}
	return nil
}

// Start 启动 PreparedCommand，隐藏窗口，把进程加入 Job Object，返回 Handle。
//
// onStdout/onStderr 收到的是按行切分后的输出（已去除换行），由 logbus 接管。
// 调用方负责在收到输出后自行做事件解析与落库。
func Start(ctx context.Context, pc *PreparedCommand, onStdout, onStderr func(line string)) (*Handle, error) {
	if pc == nil {
		return nil, fmt.Errorf("nil prepared command")
	}
	innerCtx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(innerCtx, pc.Cmd, pc.Args...)
	cmd.Dir = pc.Cwd
	cmd.Env = mergeEnv(pc.Env)
	configureHidden(cmd) // 平台相关：隐藏窗口 + CREATE_NO_WINDOW
	attachPipes(cmd, onStdout, onStderr)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start process: %w", err)
	}

	h := &Handle{
		cmd:     cmd,
		rootPID: cmd.Process.Pid,
		cancel:  cancel,
	}

	// Windows：把根进程加入 Job Object，确保整棵树可被统一回收。
	h.jobCloser = assignJob(h.rootPID)

	return h, nil
}

// Wait 等待进程退出，返回退出码。
func (h *Handle) Wait() (int, error) {
	if h.pty != nil {
		// ConPTY 模式
		code, err := waitConPTY(h.pty)
		h.cancel()
		return code, err
	}
	err := h.cmd.Wait()
	if h.cmd.ProcessState != nil {
		return h.cmd.ProcessState.ExitCode(), err
	}
	return -1, err
}

// GracefulStop 发送 Ctrl-Break（普通模式）或 Ctrl+C（ConPTY 模式），给应用自行清理的机会。
func (h *Handle) GracefulStop() error {
	if h.pty != nil {
		// ConPTY 模式：向 pty 输入写 Ctrl+C，等价于用户按 Ctrl+C
		return gracefulStopConPTY(h.pty)
	}
	return sendCtrlBreak(h)
}

// Terminate 强制终止进程树：ConPTY 模式用 TerminateProcess，普通模式用 taskkill /t /f。
func (h *Handle) Terminate() error {
	if h.pty != nil {
		// ConPTY 模式：先 TerminateProcess，再靠 taskkill 兜底清子进程
		_ = terminateConPTY(h.pty)
		return terminateTree(h)
	}
	return terminateTree(h)
}

// mergeEnv 把注入变量合并到父进程环境。注入变量覆盖同名父进程变量。
func mergeEnv(extra map[string]string) []string {
	merged := make(map[string]string, len(extra)+64)
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				merged[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}
