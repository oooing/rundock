//go:build !windows

package proc

func RunInternalMode(_ []string) (bool, int) { return false, 0 }

func EnsureSession0Service() error { return nil }
