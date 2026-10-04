//go:build windows

package recovery

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// netsh is read-only and its numeric rows are independent of display language.
// Failure does not invent a reservation: fresh starts also probe actual binding.
func ReadExcludedRanges() []PortRange {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := []PortRange{}
	for _, family := range []string{"ipv4", "ipv6"} {
		cmd := exec.CommandContext(ctx, "netsh.exe", "interface", family, "show", "excludedportrange", "protocol=tcp")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || len(fields) > 3 {
				continue
			}
			start, e1 := strconv.Atoi(fields[0])
			end, e2 := strconv.Atoi(fields[1])
			if e1 == nil && e2 == nil && start > 0 && end >= start && end <= 65535 {
				network := "tcp4"
				if family == "ipv6" {
					network = "tcp6"
				}
				result = append(result, PortRange{Start: start, End: end, Family: network})
			}
		}
	}
	return result
}

func socketAccessDenied(err error) bool {
	return errors.Is(err, syscall.Errno(10013)) || errors.Is(err, syscall.EACCES)
}
func socketUnsupported(err error) bool {
	return errors.Is(err, syscall.Errno(10047)) || errors.Is(err, syscall.Errno(10049))
}
func socketInUse(err error) bool {
	return errors.Is(err, syscall.Errno(10048)) || errors.Is(err, syscall.EADDRINUSE)
}
