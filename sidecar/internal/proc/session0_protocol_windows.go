//go:build windows

package proc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const maxRunnerFrameSize = 16 << 20

type runnerFrame struct {
	Type  string           `json:"type"`
	Spec  *PreparedCommand `json:"spec,omitempty"`
	Data  []byte           `json:"data,omitempty"`
	PID   int              `json:"pid,omitempty"`
	Code  int              `json:"code,omitempty"`
	Error string           `json:"error,omitempty"`
}

func writeRunnerFrame(w io.Writer, frame runnerFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("encode runner frame: %w", err)
	}
	if len(payload) > maxRunnerFrameSize {
		return fmt.Errorf("runner frame size %d exceeds limit %d", len(payload), maxRunnerFrameSize)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(payload))); err != nil {
		return fmt.Errorf("write runner frame length: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write runner frame payload: %w", err)
	}
	return nil
}

func readRunnerFrame(r io.Reader) (runnerFrame, error) {
	var size uint32
	if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
		return runnerFrame{}, fmt.Errorf("read runner frame length: %w", err)
	}
	if size > maxRunnerFrameSize {
		return runnerFrame{}, fmt.Errorf("runner frame size %d exceeds limit %d", size, maxRunnerFrameSize)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return runnerFrame{}, fmt.Errorf("read runner frame payload: %w", err)
	}
	var frame runnerFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return runnerFrame{}, fmt.Errorf("decode runner frame: %w", err)
	}
	return frame, nil
}
