//go:build !windows

package launcher

import "syscall"

func syscallSignal0() syscall.Signal { return syscall.Signal(0) }
