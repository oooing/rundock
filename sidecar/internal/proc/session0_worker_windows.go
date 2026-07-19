//go:build windows

package proc

import (
	"context"
	"fmt"
	"io"
	"sync"
)

type runnerFrameWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (w *runnerFrameWriter) write(frame runnerFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeRunnerFrame(w.w, frame)
}

func runSession0Worker(ctx context.Context, input io.Reader, output io.Writer) int {
	start, err := readRunnerFrame(input)
	if err != nil {
		_ = writeRunnerFrame(output, runnerFrame{Type: "error", Error: err.Error()})
		return 1
	}
	if start.Type != "start" || start.Spec == nil {
		_ = writeRunnerFrame(output, runnerFrame{Type: "error", Error: "worker expected start frame"})
		return 1
	}

	writer := &runnerFrameWriter{w: output}
	started := make(chan struct{})
	spec := start.Spec
	handle, err := Start(ctx, spec, func(line string) {
		<-started
		_ = writer.write(runnerFrame{Type: "output", Data: []byte(line + "\n")})
	}, func(line string) {
		<-started
		_ = writer.write(runnerFrame{Type: "output", Data: []byte(line + "\n")})
	})
	if err != nil {
		_ = writer.write(runnerFrame{Type: "error", Error: err.Error()})
		close(started)
		return 1
	}
	if err := writer.write(runnerFrame{Type: "started", PID: handle.PID()}); err != nil {
		close(started)
		_ = handle.Terminate()
		return 1
	}
	close(started)

	go handleWorkerControl(input, handle)
	code, waitErr := handle.Wait()
	_ = handle.Close()
	if waitErr != nil {
		_ = writer.write(runnerFrame{Type: "error", Error: fmt.Sprintf("wait for command: %v", waitErr)})
		return 1
	}
	if err := writer.write(runnerFrame{Type: "exit", Code: code}); err != nil {
		return 1
	}
	return 0
}

func handleWorkerControl(input io.Reader, handle *Handle) {
	for {
		frame, err := readRunnerFrame(input)
		if err != nil {
			return
		}
		switch frame.Type {
		case "ctrl-c":
			_ = handle.GracefulStop()
		case "terminate":
			_ = handle.Terminate()
			return
		}
	}
}
