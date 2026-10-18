package publisher

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func TestTargetCommandFailureExplainsDiskFull(t *testing.T) {
	for _, output := range []string{
		"error: failed to write to C:\\Users\\example\\Temp\\rd-native-build\\full.rmeta: 磁盘空间不足 (os error 112)",
		"error: failed to write metadata: No space left on device (os error 28)",
		"npm ERR! code ENOSPC", "ENOSPC: no space left on device, write",
	} {
		code, message := targetCommandFailure("PC 客户端", "build", output+"\nsecret-value-must-not-appear", errors.New("exit status 1"))
		if code != "build_disk_full" || !strings.Contains(message, "磁盘空间不足") || !strings.Contains(message, "临时目录") || strings.Contains(message, "secret-value") {
			t.Fatalf("unhelpful or unsafe error: %s %s", code, message)
		}
	}
	for _, test := range []struct {
		err  error
		code string
	}{
		{errors.New("exit status 1"), "target_step_failed"},
		{context.Canceled, "target_step_cancelled"},
		{context.DeadlineExceeded, "target_step_timeout"},
	} {
		code, _ := targetCommandFailure("PC", "build", "compile error: missing symbol", test.err)
		if code != test.code {
			t.Fatalf("code = %s, want %s", code, test.code)
		}
	}
}

type diskFullTargetRunner struct{}

func TestTargetCommandFailureExplainsVerificationTools(t *testing.T) {
	for _, output := range []string{
		"无法将 Get-F\r\nileHash 项识别为命令\n(Get-FileHash:String) CommandNotFoundException\nsecret-must-not-appear",
		"[RUNDOCK_SIGNATURE_TOOL_MISSING] secret-must-not-appear",
	} {
		code, message := targetCommandFailure("Windows 安装包", "build", output, errors.New("exit status 1"))
		if code != "build_verification_tool_missing" || !strings.Contains(message, "重新构建") || strings.Contains(message, "secret-must-not-appear") {
			t.Fatalf("unhelpful or unsafe verification error: %s %s", code, message)
		}
	}
	code, _ := targetCommandFailure("Windows", "build", "Get-FileHash completed; later compilation failed", errors.New("exit status 1"))
	if code != "target_step_failed" {
		t.Fatalf("successful hash command misclassified: %s", code)
	}
}

func (diskFullTargetRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, error) {
	if args[len(args)-1] == "build-web" {
		return "error: failed to write metadata (os error 112)", errors.New("exit status 1")
	}
	return "ok", nil
}

func TestTargetDiskFullPersistsActionableError(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	svc.targetRunner = diskFullTargetRunner{}
	selection := store.ReleaseTargetSelection{TargetID: "web", Build: true}
	run := createTargetExecutionRun(t, svc, repo, "disk-full", selection)
	err := svc.executeTargetPhase(context.Background(), run, executorPlan(validExecutorTarget(), selection), false)
	var failure *targetExecutionError
	if !errors.As(err, &failure) || failure.Code != "build_disk_full" {
		t.Fatalf("failure = %#v", err)
	}
	targets, err := svc.store.ReleaseTargetRuns(run.ID)
	if err != nil || len(targets) != 1 || targets[0].ErrorCode != failure.Code || targets[0].ErrorMessage != failure.Message || targets[0].BuildDone {
		t.Fatalf("disk error not persisted: %+v, %v", targets, err)
	}
}

func TestLongBuildLogRetainsFinalFailure(t *testing.T) {
	b := &buildOutputBuffer{max: maxCheckLogBytes}
	start, end := "BUILD START\n", "\nerror: failed to write metadata (os error 112)\n"
	b.Write([]byte(start))
	b.Write([]byte(strings.Repeat("compiling dependency\n", maxCheckLogBytes)))
	b.Write([]byte(end))
	output := b.String()
	if len(b.head)+len(b.tail) > maxCheckLogBytes || !strings.HasPrefix(output, start) || !strings.HasSuffix(output, end) {
		t.Fatal("build log lost its boundaries or exceeded its budget")
	}
	code, _ := targetCommandFailure("PC", "build", output, errors.New("exit status 1"))
	logged := truncateTargetOutput(output)
	if code != "build_disk_full" || !strings.HasPrefix(logged, start) || !strings.HasSuffix(logged, end) {
		t.Fatal("final error was lost before classification or persistence")
	}
	short := &buildOutputBuffer{max: 128}
	for _, chunk := range []string{"hello", " world", "\n"} {
		short.Write([]byte(chunk))
	}
	if short.String() != "hello world\n" {
		t.Fatalf("short output changed: %q", short.String())
	}
}
