//go:build !windows

package launcher

import "os"

// processAlive 非 Windows：Signal(syscall.Signal(0)) 探活。
func processAlive(p *os.Process, _ int) bool {
	return p.Signal(syscallSignal0()) == nil
}
