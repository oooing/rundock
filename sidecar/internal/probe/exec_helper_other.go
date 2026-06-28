//go:build !windows

package probe

import "os/exec"

func execCombinedOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
