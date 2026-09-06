package recovery

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestConflictDetectionDoesNotTreatHintsAsFailures(t *testing.T) {
	tests := []struct {
		line string
		want []int
	}{
		{"[ERROR] Development port 17655 is occupied. Stop the existing development instance first.", []int{17655}},
		{"[错误] 端口 1421 被占用", []int{1421}},
		{"Error: listen EADDRINUSE: address already in use :::3000", []int{3000}},
		{"portHints=[1421 17655]", []int{}},
		{"GET http://localhost:1421/api/health", []int{}},
		{"port 99999 is occupied", []int{}},
	}
	for _, tt := range tests {
		if got := PortsFromLogs([]string{tt.line}); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%q: %v != %v", tt.line, got, tt.want)
		}
	}
}

func TestProjectOwnershipRequiresBoundedPaths(t *testing.T) {
	root := t.TempDir()
	p := Process{PID: 1234, Created: "1", Executable: filepath.Join(root, "bin", "server.exe")}
	if !Belongs(root, p) {
		t.Fatal("project executable not recognized")
	}
	p.Executable = filepath.Join(root+"-other", "server.exe")
	if Belongs(root, p) {
		t.Fatal("path prefix must not count as ownership")
	}
	p.Executable = filepath.Join(root, "server.exe")
	p.Created = ""
	if Belongs(root, p) {
		t.Fatal("unknown process identity accepted")
	}
}
