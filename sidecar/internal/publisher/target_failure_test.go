package publisher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/store"
)

func TestTargetCommandFailureExplainsRequiredAssets(t *testing.T) {
	output := "[RUNDOCK_REQUIRED_ASSET_MISSING] assets/brand-mark.png assets/splash.png\nsecret-value-must-not-appear"
	for _, step := range []string{"check", "build"} {
		t.Run(step, func(t *testing.T) {
			code, message := targetCommandFailure("Android", step, output, errors.New("exit status 1"))
			want := "Android 的“" + stepLabel(step) + "”图标或启动图缺失或无效。请补齐并纳入发布，移除对应忽略规则后重试。"
			if code != "build_required_asset_missing" || message != want {
				t.Fatalf("required asset error is not safe and actionable: %s %q", code, message)
			}
		})
	}
}

func TestTargetCommandFailureRequiredAssetMarkerIsExplicit(t *testing.T) {
	for _, output := range []string{
		"[android.dangerous]: withAndroidDangerousBaseMod: ENOENT: no such file or directory, open 'assets/brand-mark.png'",
		"androiddangerous ENOENT assets/splash.png",
		"Missing required app icon: assets/brand-mark.png (ignored by .gitignore)",
		"RUNDOCK_REQUIRED_ASSET_MISSING assets/brand-mark.png",
		"[RUNDOCK_REQUIRED_ASSET_MISSING_EXTRA] assets/brand-mark.png",
	} {
		code, _ := targetCommandFailure("Android", "build", output, errors.New("exit status 1"))
		if code != "target_step_failed" {
			t.Fatalf("output without the stable marker was misclassified: %q => %s", output, code)
		}
	}
}

func TestTargetCommandFailureRequiredAssetPreservesContextErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{"cancelled", context.Canceled, "target_step_cancelled"},
		{"timeout", context.DeadlineExceeded, "target_step_timeout"},
		{"wrapped cancellation", fmt.Errorf("command: %w", context.Canceled), "target_step_cancelled"},
		{"wrapped timeout", fmt.Errorf("command: %w", context.DeadlineExceeded), "target_step_timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, message := targetCommandFailure("Android", "build", "[RUNDOCK_REQUIRED_ASSET_MISSING] secret-value-must-not-appear", test.err)
			_, want := targetCommandFailure("Android", "build", "", test.err)
			if code != test.code || message != want {
				t.Fatalf("context error lost precedence: %s %q", code, message)
			}
		})
	}
}

type requiredAssetTargetRunner struct {
	err   error
	calls int
}

func (r *requiredAssetTargetRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, error) {
	if len(args) > 0 && args[len(args)-1] == "build-web" {
		r.calls++
		return "[RUNDOCK_REQUIRED_ASSET_MISSING] assets/brand-mark.png\nsecret-value-must-not-appear", r.err
	}
	return "ok", nil
}

func TestTargetRequiredAssetMarkerOnlyFailsFailedCommands(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"failed", errors.New("exit status 1")},
		{"succeeded", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// An empty disposable repository is sufficient; no commit, remote or push.
			repo := t.TempDir()
			runGit(t, repo, "init", "-b", "main")
			db, err := store.Open(filepath.Join(t.TempDir(), "launcher.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.CreateApp(&store.App{ID: "app1", Name: "fixture", EntryScript: filepath.Join(repo, "start.bat"), Cwd: repo, AdapterType: "batch", LastStatus: "stopped"}); err != nil {
				t.Fatal(err)
			}
			svc := New(db)
			t.Cleanup(svc.Close)
			runner := &requiredAssetTargetRunner{err: test.err}
			svc.targetRunner = runner
			selection := store.ReleaseTargetSelection{TargetID: "web", Build: true}
			run := createTargetExecutionRun(t, svc, repo, "required-asset", selection)
			err = svc.executeTargetPhase(context.Background(), run, executorPlan(validExecutorTarget(), selection), false)
			if runner.calls != 1 {
				t.Fatalf("build command ran %d times, want 1", runner.calls)
			}
			targets, readErr := db.ReleaseTargetRuns(run.ID)
			if readErr != nil || len(targets) != 1 {
				t.Fatalf("target state: %+v, %v", targets, readErr)
			}
			state := targets[0]
			if test.err == nil {
				if err != nil || state.Status != "succeeded" || !state.BuildDone || state.ErrorCode != "" || state.ErrorMessage != "" {
					t.Fatalf("successful command was treated as failed: %+v, %v", state, err)
				}
				return
			}
			var failure *targetExecutionError
			if !errors.As(err, &failure) || failure.Code != "build_required_asset_missing" {
				t.Fatalf("failure = %#v", err)
			}
			if state.Status != "failed" || state.BuildDone || state.ErrorCode != failure.Code || state.ErrorMessage != failure.Message {
				t.Fatalf("asset failure not persisted: %+v", state)
			}
			if strings.Contains(state.ErrorMessage, "brand-mark.png") || strings.Contains(state.ErrorMessage, "secret-value") {
				t.Fatalf("raw command output leaked into primary message: %q", state.ErrorMessage)
			}
		})
	}
}

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
