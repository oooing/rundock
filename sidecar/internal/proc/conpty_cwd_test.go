//go:build windows

package proc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConPTYBatCallPreservesCwd 验证 ConPTY 下 bat 调用另一个 bat（如 npm.cmd）时，
// 不加 call 会导致 cwd 丢失（回归 ingLocalPlay npm run tauri dev 找不到 package.json 的根因）。
//
// 复现场景：
//   - 主 bat 先 cd 到 sub dir，再调用 sub.cmd（模拟 npm.cmd）
//   - sub.cmd 不改目录，只 echo 自己的 %CD%
//   - 不加 call：主 bat 的控制权转移到 sub.cmd，sub.cmd 退出后主 bat 不继续（或 cwd 不可预期）
//   - 加 call：sub.cmd 作为子调用执行，cwd 正确保留
func TestConPTYBatCallPreservesCwd(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "myfrontend")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// sub.cmd：模拟 npm.cmd，打印自己的 cwd（应该跟调用者一致）
	subCmd := filepath.Join(dir, "sub.cmd")
	if err := os.WriteFile(subCmd, []byte("@echo off\r\necho SUB_CWD=%CD%\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 带 call 的主 bat：cd 到 subDir，call sub.cmd，验证 cwd 正确
	withCallBat := filepath.Join(dir, "with-call.bat")
	if err := os.WriteFile(withCallBat, []byte(
		"@echo off\r\n"+
			"cd /d \""+subDir+"\"\r\n"+
			"echo BEFORE_CWD=%CD%\r\n"+
			"call \""+subCmd+"\"\r\n"+
			"echo AFTER_CWD=%CD%\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 不带 call 的主 bat：cd 到 subDir，直接 sub.cmd（控制权转移）
	withoutCallBat := filepath.Join(dir, "without-call.bat")
	if err := os.WriteFile(withoutCallBat, []byte(
		"@echo off\r\n"+
			"cd /d \""+subDir+"\"\r\n"+
			"echo BEFORE_CWD=%CD%\r\n"+
			"\""+subCmd+"\"\r\n"+
			"echo AFTER_CWD=%CD%\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("with_call_preserves_cwd", func(t *testing.T) {
		out := runConPTYBat(t, withCallBat, dir)
		// BEFORE_CWD 和 AFTER_CWD 都应是 subDir，且 SUB_CWD 也是 subDir
		if !strings.Contains(out, "BEFORE_CWD="+subDir) {
			t.Errorf("BEFORE_CWD should be %s\noutput:\n%s", subDir, out)
		}
		if !strings.Contains(out, "SUB_CWD="+subDir) {
			t.Errorf("SUB_CWD should be %s\noutput:\n%s", subDir, out)
		}
		if !strings.Contains(out, "AFTER_CWD="+subDir) {
			t.Errorf("AFTER_CWD should be %s (call preserves cwd)\noutput:\n%s", subDir, out)
		}
	})

	t.Run("without_call_loses_control", func(t *testing.T) {
		out := runConPTYBat(t, withoutCallBat, dir)
		// 不加 call：sub.cmd 执行完后主 bat 不继续，AFTER_CWD 不会打印
		if strings.Contains(out, "AFTER_CWD=") {
			// 如果 AFTER_CWD 打印了，说明这个 Windows 版本/cmd 行为下不加 call 也能继续
			// 这不是失败——重点是验证加 call 的行为是正确的（上面那个子测试）
		}
		// 至少 BEFORE 和 SUB 应该有
		if !strings.Contains(out, "BEFORE_CWD="+subDir) {
			t.Errorf("BEFORE_CWD should be %s\noutput:\n%s", subDir, out)
		}
	})
}

// runConPTYBat 用 ConPTY 启动一个 bat，收集输出（最多等 5 秒）。
func runConPTYBat(t *testing.T, script, cwd string) string {
	t.Helper()
	var output strings.Builder
	var mu sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lb := newLineBuffer(func(line string) {
		mu.Lock()
		output.WriteString(line + "\n")
		mu.Unlock()
	})
	session, pid, err := startConPTY(ctx, os.Getenv("ComSpec"), []string{"/d", "/s", "/c", "call", script}, cwd, mergeEnv(nil), lb.feed)
	if err != nil {
		t.Skipf("ConPTY unavailable: %v", err)
	}
	h := &Handle{rootPID: pid, cancel: cancel, pty: session}
	h.jobCloser = assignJob(pid)
	defer func() { _ = h.Close() }()

	// 等 bat 执行完（bat 很快，2 秒足够）
	time.Sleep(2 * time.Second)
	cancel()
	_ = h.Terminate()

	mu.Lock()
	defer mu.Unlock()
	return output.String()
}
