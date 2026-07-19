//go:build windows

package proc

import (
	"bytes"
	"net"
	"testing"
	"time"
)

type fakePTYSession struct {
	closed, stopped, terminated bool
	code                        int
}

func (s *fakePTYSession) close() error       { s.closed = true; return nil }
func (s *fakePTYSession) sendCtrlC() error   { s.stopped = true; return nil }
func (s *fakePTYSession) terminate() error   { s.terminated = true; return nil }
func (s *fakePTYSession) wait() (int, error) { return s.code, nil }

func TestStartServiceSessionStreamsOutputAndExitCode(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	serverErr := make(chan error, 1)
	go func() {
		frame, err := readRunnerFrame(server)
		if err != nil {
			serverErr <- err
			return
		}
		if frame.Type != "start" || frame.Spec == nil || frame.Spec.Cmd != "cmd.exe" {
			serverErr <- errUnexpectedFrame(frame)
			return
		}
		for _, reply := range []runnerFrame{
			{Type: "started", PID: 4321},
			{Type: "output", Data: []byte("ready\r\n")},
			{Type: "exit", Code: 7},
		} {
			if err := writeRunnerFrame(server, reply); err != nil {
				serverErr <- err
				return
			}
		}
		serverErr <- nil
	}()

	var output bytes.Buffer
	session, pid, err := startServiceSession(client, &PreparedCommand{Cmd: "cmd.exe"}, func(data []byte) {
		_, _ = output.Write(data)
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if pid != 4321 {
		t.Fatalf("pid = %d, want 4321", pid)
	}

	code, err := session.wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if output.String() != "ready\r\n" {
		t.Fatalf("output = %q", output.String())
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish")
	}
}

func TestConPTYHelpersDispatchThroughSessionInterface(t *testing.T) {
	session := &fakePTYSession{code: 23}
	closeConPTY(session)
	if !session.closed {
		t.Fatal("close was not dispatched")
	}
	if err := gracefulStopConPTY(session); err != nil || !session.stopped {
		t.Fatalf("graceful stop was not dispatched: %v", err)
	}
	if err := terminateConPTY(session); err != nil || !session.terminated {
		t.Fatalf("terminate was not dispatched: %v", err)
	}
	code, err := waitConPTY(session)
	if err != nil || code != 23 {
		t.Fatalf("wait = (%d, %v), want (23, nil)", code, err)
	}
}

func TestRemoteHandleTerminateDoesNotRunDesktopTaskkill(t *testing.T) {
	session := &fakePTYSession{}
	handle := &Handle{rootPID: 999999, pty: session, remoteTree: true}
	if err := handle.Terminate(); err != nil {
		t.Fatalf("remote terminate: %v", err)
	}
	if !session.terminated {
		t.Fatal("remote terminate was not dispatched")
	}
}

func TestServiceSessionTerminateClosesPipeInsteadOfWaitingForWorker(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		if _, err := readRunnerFrame(server); err != nil {
			serverDone <- err
			return
		}
		if err := writeRunnerFrame(server, runnerFrame{Type: "started", PID: 42}); err != nil {
			serverDone <- err
			return
		}
		_, err := readRunnerFrame(server)
		serverDone <- err
	}()

	session, _, err := startServiceSession(client, &PreparedCommand{Cmd: "cmd.exe"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.terminate(); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	select {
	case err := <-serverDone:
		if err == nil {
			t.Fatal("server received a control frame instead of disconnect")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe disconnect")
	}
}

func TestServiceSessionCtrlCTimesOutWhenServiceStopsReading(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	go func() {
		_, _ = readRunnerFrame(server)
		_ = writeRunnerFrame(server, runnerFrame{Type: "started", PID: 42})
		select {}
	}()
	session, _, err := startServiceSession(client, &PreparedCommand{Cmd: "cmd.exe"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()

	done := make(chan error, 1)
	go func() { done <- session.sendCtrlC() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected control write timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ctrl+C remained blocked")
	}
}

func errUnexpectedFrame(frame runnerFrame) error {
	return &unexpectedFrameError{frame: frame}
}

type unexpectedFrameError struct{ frame runnerFrame }

func (e *unexpectedFrameError) Error() string { return "unexpected runner frame: " + e.frame.Type }
