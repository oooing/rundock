// Package config 负责应用配置与数据目录定位。
// 数据目录策略（Windows 优先）： %APPDATA%\launcher-sidecar
// 兜底（非 Windows 或 APPDATA 缺失）：~/.launcher-sidecar
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config 汇总运行期配置。
type Config struct {
	DataDir  string // 数据根目录
	DBPath   string // SQLite 文件路径
	LogsDir  string // 归档日志目录
	HTTPAddr string // HTTP 监听地址，例如 127.0.0.1:0（0=随机端口）
	HTTPPort int    // 实际监听端口（启动后回填）
	DevMode  bool   // 是否开发模式（打印更多日志）
}

// Default 返回默认配置。Windows 下用 %APPDATA%，否则用 ~/.launcher-sidecar。
func Default() *Config {
	dir := resolveDataDir()
	return &Config{
		DataDir:  dir,
		DBPath:   filepath.Join(dir, "launcher.db"),
		LogsDir:  filepath.Join(dir, "logs"),
		HTTPAddr: resolveHTTPAddr(),
		DevMode:  os.Getenv("LAUNCHER_DEV") == "1",
	}
}

// resolveDataDir 决定数据根目录，并保证其存在。
func resolveDataDir() string {
	var dir string
	if explicit := os.Getenv("LAUNCHER_DATA_DIR"); explicit != "" {
		// Never fall back to shared data when isolation was explicitly requested.
		absolute, err := filepath.Abs(explicit)
		if err != nil {
			panic(fmt.Errorf("resolve LAUNCHER_DATA_DIR: %w", err))
		}
		if err := os.MkdirAll(absolute, 0o755); err != nil {
			panic(fmt.Errorf("create LAUNCHER_DATA_DIR: %w", err))
		}
		return absolute
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		dir = filepath.Join(appdata, "launcher-sidecar")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".launcher-sidecar")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// 极端情况：创建失败则退回当前目录
		dir = ".launcher-data"
		_ = os.MkdirAll(dir, 0o755)
	}
	return dir
}

// resolveHTTPAddr 决定监听地址。
// 优先用 LAUNCHER_PORT 显式指定端口（便于 Tauri 壳固定连接）；
// 否则用 :0 让系统分配随机端口（sidecar 启动后把实际端口写回供前端连接）。
func resolveHTTPAddr() string {
	if p := os.Getenv("LAUNCHER_PORT"); p != "" {
		if _, err := strconv.Atoi(p); err == nil {
			return "127.0.0.1:" + p
		}
	}
	return "127.0.0.1:0"
}

// String 用于日志输出。
func (c *Config) String() string {
	return fmt.Sprintf("config{dataDir=%s db=%s http=%s port=%d}", c.DataDir, c.DBPath, c.HTTPAddr, c.HTTPPort)
}
