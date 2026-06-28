//go:build !windows

package proc

import "os"

func interruptSignal() os.Signal { return os.Interrupt }
