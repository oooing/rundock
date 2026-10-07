//go:build windows

package recovery

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestRestartOwnedFixtureProcess(t *testing.T) {
	if os.Getenv("RUNDOCK_RESTART_FIXTURE") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestartOwnedFixtureProcess$")
	cmd.Env = append(os.Environ(), "RUNDOCK_RESTART_FIXTURE=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	var c, e, k, u windows.Filetime
	err = windows.GetProcessTimes(h, &c, &e, &k, &u)
	windows.CloseHandle(h)
	if err != nil {
		t.Fatal(err)
	}
	p := Process{PID: cmd.Process.Pid, Executable: os.Args[0], Created: strconv.FormatUint(uint64(c.HighDateTime)<<32|uint64(c.LowDateTime), 10)}
	wrong := p
	wrong.Created = "1"
	if _, _, err = OpenRestartGroup([]Process{wrong}); err == nil {
		t.Fatal("reused PID accepted")
	}
	if !SameProcess(p) {
		t.Fatal("invalid confirmation killed fixture")
	}
	stop, release, err := OpenRestartGroup([]Process{p})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = stop(); err != nil {
		t.Fatal(err)
	}
	if SameProcess(p) {
		t.Fatal("confirmed fixture remains alive")
	}
	_ = cmd.Wait()
}
