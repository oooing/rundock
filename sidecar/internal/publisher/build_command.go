package publisher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Build processes get a process-tree lifetime and never inherit GitHub upload
// credentials. Project-specific signing remains in the explicitly trusted script.
func runBuildCommand(ctx context.Context, dir, command string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, args := checkShell(command)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	for _, entry := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
		if key == "GH_TOKEN" || key == "GITHUB_TOKEN" || key == "GH_ENTERPRISE_TOKEN" || key == "GITHUB_ENTERPRISE_TOKEN" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GH_PROMPT_DISABLED=1")
	prepareCheckProcess(cmd)
	out := &limitedBuffer{max: maxCheckLogBytes}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	closeJob, err := attachCheckProcess(cmd)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return "", fmt.Errorf("无法建立构建进程取消保护")
	}
	defer closeJob()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return redactSensitiveLog(out.String()), err
	case <-ctx.Done():
		closeJob()
		cmd.Process.Kill()
		<-done
		return redactSensitiveLog(out.String()), ctx.Err()
	}
}

func runTargetCommand(ctx context.Context, runner commandRunner, dir, command string) (string, error) {
	if _, ok := runner.(execRunner); ok {
		return runBuildCommand(ctx, dir, command)
	}
	return runCheckCommand(ctx, runner, dir, command)
}
