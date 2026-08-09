//go:build windows

package proc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConPTYStopClosesOnce(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "wait.bat")
	if err := os.WriteFile(script, []byte("@echo off\r\n:loop\r\ntimeout /t 30 /nobreak >nul\r\ngoto loop\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, pid, err := startConPTY(ctx, os.Getenv("ComSpec"), []string{"/d", "/s", "/c", "call", script}, dir, mergeEnv(nil), nil)
	if err != nil {
		t.Skipf("ConPTY unavailable: %v", err)
	}
	h := &Handle{rootPID: pid, cancel: cancel, pty: session}
	h.jobCloser = assignJob(pid)

	waitDone := make(chan struct{})
	go func() {
		_, _ = h.Wait()
		_ = h.Close() // 与强制终止并发收尾，复现真实 watchExit 路径。
		close(waitDone)
	}()

	time.Sleep(200 * time.Millisecond)
	_ = h.GracefulStop()
	terminateDone := make(chan struct{})
	go func() {
		_ = h.Terminate()
		close(terminateDone)
	}()

	for _, item := range []struct {
		name string
		done <-chan struct{}
	}{{"terminate", terminateDone}, {"wait", waitDone}} {
		select {
		case <-item.done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not finish", item.name)
		}
	}
}

func TestStartWithConPTYRunsInCurrentUserSession(t *testing.T) {
	t.Setenv("PROJECTS_START_MANAGER_PERMISSION_TEST", "standard-user")

	var output strings.Builder
	var outputMu sync.Mutex
	handle, err := StartWithConPTY(context.Background(), &PreparedCommand{
		Cmd:  os.Getenv("ComSpec"),
		Args: []string{"/d", "/s", "/c", "echo %PROJECTS_START_MANAGER_PERMISSION_TEST%"},
	}, func(line string) {
		outputMu.Lock()
		output.WriteString(line)
		output.WriteByte('\n')
		outputMu.Unlock()
	})
	if err != nil {
		t.Fatalf("start current-user ConPTY: %v", err)
	}
	defer func() { _ = handle.Close() }()

	done := make(chan error, 1)
	go func() {
		code, waitErr := handle.Wait()
		if waitErr == nil && code != 0 {
			waitErr = fmt.Errorf("unexpected ConPTY exit code: %d", code)
		}
		done <- waitErr
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = handle.Terminate()
		t.Fatal("current-user ConPTY command did not exit")
	}

	outputMu.Lock()
	got := output.String()
	outputMu.Unlock()
	if !strings.Contains(got, "standard-user") {
		t.Fatalf("environment/output was not preserved: %q", got)
	}
}
