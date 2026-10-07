//go:build !windows

package recovery

import "fmt"

func commandArgs(string) []string    { return nil }
func SameProcess(Process) bool       { return false }
func Snapshot() ([]Process, error)   { return nil, fmt.Errorf("当前平台不支持安全释放端口") }
func Terminate(Process) error        { return fmt.Errorf("当前平台不支持安全释放端口") }
func CanCloseExternal(Process) error { return fmt.Errorf("当前平台需要从原程序手动退出") }
func TerminateExternal(Process) error {
	return fmt.Errorf("当前平台需要从原程序手动退出")
}

func OpenRestartGroup([]Process) (func() error, func(), error) {
	return nil, nil, fmt.Errorf("当前平台需要从原程序手动退出")
}
