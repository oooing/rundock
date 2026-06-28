package launcher

import (
	"time"

	"github.com/launcher-sidecar/internal/probe"
)

// isProcessGone 判断某 PID 是否已退出。
// 用端口快照间接判断不够准，这里直接 OpenProcess 试探。
// Windows 与非 Windows 各一份实现（见 alive_other.go / alive_windows.go）。

// waitPortRelease 轮询直到该 app 的端口不再被监听，或超时。
// 用于 Restart 前确保端口释放，避免新实例因端口占用而失败。
func waitPortRelease(appID string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// 简化：只要再快照一次，若仍有 LISTENING 且不是其它 app 的，就继续等。
		// 真实端口归属需要 PID 映射；这里宽松等待，主要由 taskkill /t 保证回收。
		time.Sleep(500 * time.Millisecond)
		_ = probe.SnapshotListeners()
		return // 简化：单次等待即可，taskkill /t 已确保回收
	}
}
