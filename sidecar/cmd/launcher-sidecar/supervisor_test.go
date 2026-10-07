package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSupervisorOnlyRestartsExplicitRequests(t *testing.T) {
	if file := os.Getenv("RUNDOCK_SUPERVISOR_FIXTURE"); file != "" {
		if os.Getenv("LAUNCHER_SUPERVISOR_PID") != strconv.Itoa(os.Getppid()) {
			os.Exit(9)
		}
		if _, err := os.Stat(file); err != nil {
			if os.WriteFile(file, []byte("first"), 0600) != nil {
				os.Exit(10)
			}
			if os.Getenv("RUNDOCK_SUPERVISOR_CRASH") == "1" {
				os.Exit(2)
			}
			os.Exit(restartExitCode)
		}
		os.WriteFile(file, []byte("restarted"), 0600)
		os.Exit(0)
	}
	for _, crash := range []bool{false, true} {
		file := filepath.Join(t.TempDir(), "marker")
		t.Setenv("RUNDOCK_SUPERVISOR_FIXTURE", file)
		if crash {
			t.Setenv("RUNDOCK_SUPERVISOR_CRASH", "1")
		}
		code := supervise([]string{"-test.run=^TestSupervisorOnlyRestartsExplicitRequests$"})
		data, _ := os.ReadFile(file)
		if crash {
			if code != 2 || string(data) != "first" {
				t.Fatal(code, string(data))
			}
		} else if code != 0 || string(data) != "restarted" {
			t.Fatal(code, string(data))
		}
	}
}
