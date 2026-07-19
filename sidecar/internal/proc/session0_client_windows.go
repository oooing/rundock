//go:build windows

package proc

import (
	"fmt"
	"io"
	"sync"
	"time"
)

type serviceSession struct {
	conn      io.ReadWriteCloser
	onOutput  func([]byte)
	writeMu   sync.Mutex
	closeOnce sync.Once
	done      chan serviceResult
}

type serviceResult struct {
	code int
	err  error
}

func startServiceSession(conn io.ReadWriteCloser, spec *PreparedCommand, onOutput func([]byte)) (*serviceSession, int, error) {
	if err := writeRunnerFrame(conn, runnerFrame{Type: "start", Spec: spec}); err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	started, err := readRunnerFrame(conn)
	if err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	if started.Type == "error" {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("session 0 runner: %s", started.Error)
	}
	if started.Type != "started" || started.PID <= 0 {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("session 0 runner returned invalid start frame: %q", started.Type)
	}

	s := &serviceSession{
		conn:     conn,
		onOutput: onOutput,
		done:     make(chan serviceResult, 1),
	}
	go s.readLoop()
	return s, started.PID, nil
}

func (s *serviceSession) readLoop() {
	for {
		frame, err := readRunnerFrame(s.conn)
		if err != nil {
			s.done <- serviceResult{code: -1, err: err}
			return
		}
		switch frame.Type {
		case "output":
			if len(frame.Data) > 0 && s.onOutput != nil {
				s.onOutput(frame.Data)
			}
		case "exit":
			s.done <- serviceResult{code: frame.Code}
			return
		case "error":
			s.done <- serviceResult{code: -1, err: fmt.Errorf("session 0 runner: %s", frame.Error)}
			return
		default:
			s.done <- serviceResult{code: -1, err: fmt.Errorf("unexpected session 0 frame: %q", frame.Type)}
			return
		}
	}
}

func (s *serviceSession) send(frame runnerFrame) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writeRunnerFrame(s.conn, frame)
}

func (s *serviceSession) sendCtrlC() error {
	done := make(chan error, 1)
	go func() { done <- s.send(runnerFrame{Type: "ctrl-c"}) }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		return fmt.Errorf("send Ctrl+C to session 0 runner timed out")
	}
}

func (s *serviceSession) terminate() error { return s.close() }

func (s *serviceSession) wait() (int, error) {
	result := <-s.done
	return result.code, result.err
}

func (s *serviceSession) close() error {
	var err error
	s.closeOnce.Do(func() { err = s.conn.Close() })
	return err
}
