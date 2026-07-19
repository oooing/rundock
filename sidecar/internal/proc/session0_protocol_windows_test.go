//go:build windows

package proc

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func TestRunnerFrameRoundTrip(t *testing.T) {
	want := runnerFrame{
		Type: "start",
		Spec: &PreparedCommand{
			Cmd:  `C:\Windows\System32\cmd.exe`,
			Args: []string{"/d", "/c", `D:\apps\start.bat`},
			Cwd:  `D:\apps`,
			Env:  map[string]string{"APP_ENV": "test"},
		},
	}

	var stream bytes.Buffer
	if err := writeRunnerFrame(&stream, want); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	got, err := readRunnerFrame(&stream)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frame mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestReadRunnerFrameRejectsOversizedPayload(t *testing.T) {
	var stream bytes.Buffer
	if err := binary.Write(&stream, binary.LittleEndian, uint32(maxRunnerFrameSize+1)); err != nil {
		t.Fatal(err)
	}

	_, err := readRunnerFrame(&stream)
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected explicit size-limit error, got %v", err)
	}
}
