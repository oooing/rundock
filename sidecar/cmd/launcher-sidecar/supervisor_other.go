//go:build !windows

package main

import "os/exec"

func configureSupervisorChild(*exec.Cmd) {}
