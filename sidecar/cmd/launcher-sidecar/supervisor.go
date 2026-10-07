package main

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"
)

const restartExitCode = 75

// This independent parent remains alive while the HTTP worker restarts. Never
// retry crashes: only the explicit, safety-checked restart exit code is retried.
func supervise(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		log.Print(err)
		return 1
	}
	for {
		child := exec.Command(exe, args...)
		configureSupervisorChild(child)
		child.Env = append(os.Environ(), "LAUNCHER_SUPERVISOR_PID="+strconv.Itoa(os.Getpid()))
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		err = child.Run()
		if err == nil {
			return 0
		}
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			log.Print(err)
			return 1
		}
		if exited.ExitCode() != restartExitCode {
			return exited.ExitCode()
		}
		time.Sleep(200 * time.Millisecond)
	}
}
