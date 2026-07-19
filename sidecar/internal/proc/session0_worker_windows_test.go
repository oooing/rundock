//go:build windows

package proc

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunSession0WorkerRunsCommandAndStreamsFrames(t *testing.T) {
	var input bytes.Buffer
	if err := writeRunnerFrame(&input, runnerFrame{
		Type: "start",
		Spec: &PreparedCommand{
			Cmd:  "cmd.exe",
			Args: []string{"/d", "/s", "/c", "echo session-zero-worker"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	code := runSession0Worker(context.Background(), &input, &output)
	if code != 0 {
		t.Fatalf("worker exit code = %d", code)
	}

	started, err := readRunnerFrame(&output)
	if err != nil {
		t.Fatalf("read started frame: %v", err)
	}
	if started.Type != "started" || started.PID <= 0 {
		t.Fatalf("invalid started frame: %#v", started)
	}

	var text strings.Builder
	for {
		frame, err := readRunnerFrame(&output)
		if err != nil {
			t.Fatalf("read worker frame: %v", err)
		}
		switch frame.Type {
		case "output":
			text.Write(frame.Data)
		case "exit":
			if frame.Code != 0 {
				t.Fatalf("command exit code = %d", frame.Code)
			}
			if !strings.Contains(text.String(), "session-zero-worker") {
				t.Fatalf("missing command output: %q", text.String())
			}
			return
		default:
			t.Fatalf("unexpected frame: %#v", frame)
		}
	}
}
