//go:build windows

package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRunInternalModeDispatchesSession0Worker(t *testing.T) {
	var input bytes.Buffer
	if err := writeRunnerFrame(&input, runnerFrame{
		Type: "start",
		Spec: &PreparedCommand{Cmd: "cmd.exe", Args: []string{"/d", "/c", "exit 0"}},
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer

	handled, code := runInternalMode(context.Background(), []string{"--session0-worker"}, &input, &output)
	if !handled || code != 0 {
		t.Fatalf("handled/code = %v/%d", handled, code)
	}
	started, err := readRunnerFrame(&output)
	if err != nil || started.Type != "started" {
		t.Fatalf("missing started frame: %#v, %v", started, err)
	}
}

func TestRunnerServiceExecutableLivesOutsideAppInstall(t *testing.T) {
	path, err := runnerServiceExecutable()
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join("ProjectsStartManager", "runner", "launcher-runner.exe")
	if !strings.HasSuffix(path, wantSuffix) {
		t.Fatalf("runner path = %q, want suffix %q", path, wantSuffix)
	}
}

func TestFilesHaveSameContent(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.exe")
	b := filepath.Join(dir, "b.exe")
	if err := os.WriteFile(a, []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	equal, err := filesHaveSameContent(a, b)
	if err != nil || !equal {
		t.Fatalf("equal files = %v, %v", equal, err)
	}
	if err := os.WriteFile(b, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	equal, err = filesHaveSameContent(a, b)
	if err != nil || equal {
		t.Fatalf("different files = %v, %v", equal, err)
	}
}

func TestQueryRunnerServiceWorksWithoutElevation(t *testing.T) {
	_, _, err := queryRunnerService()
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		t.Skip("runner service is not installed")
	}
	if err != nil {
		t.Fatalf("read-only service query failed: %v", err)
	}
}

func TestInstalledRunnerServiceReportsQuickExit(t *testing.T) {
	pipe, err := openRunnerPipe()
	if err != nil {
		t.Skipf("runner service is unavailable: %v", err)
	}
	var output strings.Builder
	session, pid, err := startServiceSession(pipe, &PreparedCommand{
		Cmd:  "cmd.exe",
		Args: []string{"/d", "/c", "echo session0-exit-probe"},
	}, func(data []byte) { output.Write(data) })
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()

	done := make(chan error, 1)
	go func() {
		code, err := session.wait()
		if err == nil && code != 0 {
			err = fmt.Errorf("exit code = %d", code)
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		alive := false
		if process, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)); openErr == nil {
			defer windows.CloseHandle(process)
			var exitCode uint32
			alive = windows.GetExitCodeProcess(process, &exitCode) == nil && exitCode == 259
		}
		t.Fatalf("runner did not report exit; pid=%d alive=%v output=%q", pid, alive, output.String())
	}
}
