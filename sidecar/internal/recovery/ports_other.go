//go:build !windows

package recovery

import (
	"errors"
	"syscall"
)

func ReadExcludedRanges() []PortRange   { return nil }
func socketAccessDenied(err error) bool { return errors.Is(err, syscall.EACCES) }
func socketUnsupported(err error) bool {
	return errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EADDRNOTAVAIL)
}
func socketInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
