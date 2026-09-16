//go:build !windows

package publisher

import (
	"os/exec"
	"syscall"
)

func prepareCheckProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killCheckProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func attachCheckProcess(cmd *exec.Cmd) (func(), error) {
	return func() { killCheckProcessTree(cmd) }, nil
}
