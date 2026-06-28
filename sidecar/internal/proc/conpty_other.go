//go:build !windows

package proc

import (
	"context"
	"fmt"
)

// StartWithConPTY 非 Windows：不支持 ConPTY，回退到普通 Start。
// 调用方无需感知平台差异，统一调这个入口。
func StartWithConPTY(ctx context.Context, pc *PreparedCommand, onLine func(line string)) (*Handle, error) {
	// 非 Windows 没有 stdout/stderr 区分，统一走 onLine
	return Start(ctx, pc, onLine, onLine)
}

// closeConPTY 非 Windows 空实现。
func closeConPTY(s interface{}) {}

// waitConPTY 非 Windows 不会走到（pty 为 nil）。
func waitConPTY(s interface{}) (int, error) { return -1, fmt.Errorf("not conpty") }
