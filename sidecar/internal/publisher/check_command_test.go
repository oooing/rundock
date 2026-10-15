package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMissingCheckToolIdentifiesExecutableNotArguments(t *testing.T) {
	name := "rundock_intentionally_missing_check_tool_9281"
	for _, command := range []string{name + " --private-value=do-not-echo", `"` + name + `" --private-value=do-not-echo`} {
		status, log, reason := runProfileCommand(context.Background(), t.TempDir(), releaseconfig.CheckProfile{Command: command})
		if status != CheckUnverified || !strings.Contains(reason, name) || !strings.Contains(reason, "PATH") || strings.Contains(reason+log, "do-not-echo") {
			t.Fatalf("status=%s reason=%s log=%s", status, reason, log)
		}
	}
	for _, command := range []string{"", "echo hello", "./missing-local-tool", `..\missing-local-tool`, "${SHELL} command"} {
		if got := missingDirectCheckTool(command); got != "" {
			t.Fatalf("shell or relative command was resolved against the wrong cwd: %q", got)
		}
	}
}

func TestPwshAvailabilityUsesLauncherEnvironment(t *testing.T) {
	// Models Windows-launched RunDock: the agent's private runtime is not on PATH.
	toolPath := t.TempDir()
	if runtime.GOOS == "windows" {
		toolPath += string(os.PathListSeparator) + filepath.Join(os.Getenv("SystemRoot"), "System32")
	}
	t.Setenv("PATH", toolPath)
	if _, err := exec.LookPath("pwsh"); err == nil {
		t.Skip("pwsh is resolved independently of PATH")
	}
	status, log, reason := runProfileCommand(context.Background(), t.TempDir(), releaseconfig.CheckProfile{Command: "pwsh -NoProfile -File check.ps1"})
	if status != CheckUnverified || !strings.Contains(reason, "PowerShell 7") || !strings.Contains(reason, "pwsh") {
		t.Fatalf("status=%s reason=%s log=%s", status, reason, log)
	}
}

func TestCheckShellDecidesLocalScriptsAndFallbacks(t *testing.T) {
	root := t.TempDir()
	command := "rundock_missing_optional_tool_9871 || echo fallback-success"
	status, log, reason := runProfileCommand(context.Background(), root, releaseconfig.CheckProfile{Command: command, TimeoutSeconds: 10})
	if status != CheckPassed || !strings.Contains(log, "fallback-success") {
		t.Fatalf("fallback rejected: %s %s %s", status, reason, log)
	}
	if runtime.GOOS == "windows" {
		writeTestFile(t, filepath.Join(root, "rundock-local-check.cmd"), "@echo local-success\r\n@exit /b 0\r\n")
		status, log, reason = runProfileCommand(context.Background(), root, releaseconfig.CheckProfile{Command: "rundock-local-check.cmd", TimeoutSeconds: 10})
		if status != CheckPassed || !strings.Contains(log, "local-success") {
			t.Fatalf("local script rejected: %s %s %s", status, reason, log)
		}
		writeTestFile(t, filepath.Join(root, "rundock-local-fail.cmd"), "@exit /b 23\r\n")
		status, _, reason = runProfileCommand(context.Background(), root, releaseconfig.CheckProfile{Command: "rundock-local-fail.cmd", TimeoutSeconds: 10})
		if status != CheckFailed || !strings.Contains(reason, "23") {
			t.Fatalf("local failure mislabeled missing: %s %s", status, reason)
		}
	}
	status, _, reason = runProfileCommand(context.Background(), root, releaseconfig.CheckProfile{Command: "rundock_missing_optional_tool_9871 || exit 23", TimeoutSeconds: 10})
	if status != CheckFailed || !strings.Contains(reason, "23") {
		t.Fatalf("fallback failure mislabeled missing: %s %s", status, reason)
	}
	status, _, reason = runProfileCommand(context.Background(), root, releaseconfig.CheckProfile{Command: "exit 23", TimeoutSeconds: 10})
	if status != CheckFailed || !strings.Contains(reason, "23") {
		t.Fatalf("exit reason lost: %s %s", status, reason)
	}
}

func TestCheckCommandQuotedExitAndOutput(t *testing.T) {
	for _, tc := range []struct{ command, status, log string }{
		{`node -e "console.log('real output');process.exit(1)"`, CheckFailed, "real output"},
		{`node -e "console.log('real success')"`, CheckPassed, "real success"},
		{`rundock_intentionally_missing_check_tool_9281`, CheckUnverified, ""},
	} {
		t.Run(tc.status, func(t *testing.T) {
			got, log, reason := runProfileCommand(context.Background(), t.TempDir(), releaseconfig.CheckProfile{Command: tc.command, TimeoutSeconds: 10})
			if got != tc.status || !strings.Contains(log, tc.log) {
				t.Fatalf("got %s reason %s log %s", got, reason, log)
			}
		})
	}
}
func TestCheckCommandTimeoutAndCancel(t *testing.T) {
	command := `node -e "setInterval(()=>{},1000)"`
	status, _, _ := runProfileCommand(context.Background(), t.TempDir(), releaseconfig.CheckProfile{Command: command, TimeoutSeconds: 1})
	if status != CheckFailed {
		t.Fatalf("timeout: %s", status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	started := time.Now()
	status, _, _ = runProfileCommand(ctx, t.TempDir(), releaseconfig.CheckProfile{Command: command, TimeoutSeconds: 10})
	if status != CheckCancelled || time.Since(started) > 3*time.Second {
		t.Fatalf("cancel: %s after %s", status, time.Since(started))
	}
}

func TestCheckLogRedactsCredentials(t *testing.T) {
	token := "ghp_" + strings.Repeat("aB3cD4", 6)
	value := "A7mQ9vB2nL5pR8sT6xY3zW1c"
	got := redactSensitiveLog("request " + token + "\nAPI_TOKEN=" + value + "\nordinary failure")
	if strings.Contains(got, token) || strings.Contains(got, value) || !strings.Contains(got, "ordinary failure") {
		t.Fatal("check log did not redact credentials correctly")
	}
}
