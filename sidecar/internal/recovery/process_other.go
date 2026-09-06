//go:build !windows

package recovery

import "fmt"

func commandArgs(string) []string  { return nil }
func Snapshot() ([]Process, error) { return nil, fmt.Errorf("当前平台不支持安全释放端口") }
func Terminate(Process) error      { return fmt.Errorf("当前平台不支持安全释放端口") }
