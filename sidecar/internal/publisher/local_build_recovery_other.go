//go:build !windows

package publisher

import "errors"

// The supported desktop platform supplies delete-sharing directory leases.
// Other platforms conservatively retain scratch space until an equivalent
// descriptor-relative removal implementation is available.
func lockRecoveryDirectory(path string) (func(), error) {
	return nil, errors.New("当前平台不能安全锁定临时目录，已保留目录")
}
