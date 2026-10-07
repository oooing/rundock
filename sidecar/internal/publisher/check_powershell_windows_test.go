package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func TestSystemPowerShellCheckWithoutPwsh(t *testing.T) {
	system := filepath.Join(os.Getenv("SystemRoot"), "System32")
	t.Setenv("PATH", system+string(os.PathListSeparator)+filepath.Join(system, "WindowsPowerShell", "v1.0"))
	if tool := missingDirectCheckTool("pwsh -NoProfile -File check.ps1"); tool != "pwsh" {
		t.Fatalf("test PATH unexpectedly resolves pwsh: %q", tool)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "check with spaces.ps1")
	for _, tc := range []struct {
		script, status string
	}{
		{`if ($PSVersionTable.PSVersion.Major -ne 5) { exit 9 }; Write-Output 'system-ps5'; exit 0`, CheckPassed},
		{`Write-Output 'system-ps5'; exit 23`, CheckFailed},
	} {
		if err := os.WriteFile(file, []byte(tc.script), 0600); err != nil {
			t.Fatal(err)
		}
		status, log, reason := runProfileCommand(context.Background(), dir, releaseconfig.CheckProfile{
			Command: `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "check with spaces.ps1"`, TimeoutSeconds: 15,
		})
		if status != tc.status || !strings.Contains(log, "system-ps5") {
			t.Fatalf("status=%s reason=%s log=%s", status, reason, log)
		}
	}
}
