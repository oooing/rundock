package publisher

import (
	"context"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"strings"
	"testing"
	"time"
)

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
