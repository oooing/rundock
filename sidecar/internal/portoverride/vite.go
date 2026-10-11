// Package portoverride describes explicitly adapted, run-only port overrides.
// Knowing a framework's CLI flag is not proof that a project's APIs can move.
package portoverride

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

type Definition struct {
	Version    int    `json:"version"`
	Adapter    string `json:"adapter"`
	Script     string `json:"script"`
	Port       int    `json:"port"`
	Host       string `json:"host"`
	Interfaces string `json:"interfaces"`
}

// Only a direct Vite invocation is supported, never wrappers, workspaces,
// arbitrary shell expressions, or a second process with independent bindings.
var viteCommand = regexp.MustCompile(`^vite(?:\s+--config\s+[A-Za-z0-9_./:\\-]+|\s+--strictPort)*$`)

func Inspect(a *store.App) (*Definition, error) {
	if a == nil || a.AdapterType != "npm" || !strings.EqualFold(filepath.Base(a.EntryScript), "package.json") || !strings.EqualFold(filepath.Clean(a.Cwd), filepath.Dir(a.EntryScript)) {
		return nil, fmt.Errorf("此启动方式尚未适配临时端口；不会修改配置或停止占用程序")
	}
	cmd := strings.ToLower(filepath.Base(a.Cmd))
	if (cmd != "npm" && cmd != "npm.cmd") || len(a.Args) != 2 || a.Args[0] != "run" {
		return nil, fmt.Errorf("仅支持已声明兼容的单服务 npm run 启动；工作区和自定义命令尚未适配")
	}
	f, err := os.Open(a.EntryScript)
	if err != nil {
		return nil, fmt.Errorf("无法读取项目的临时端口适配声明")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return nil, fmt.Errorf("项目配置无法安全读取")
	}
	var pkg struct {
		Scripts    map[string]string `json:"scripts"`
		Workspaces json.RawMessage   `json:"workspaces"`
		RunDock    struct {
			TemporaryPort *Definition `json:"temporaryPort"`
		} `json:"rundock"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return nil, fmt.Errorf("项目配置不是有效的 JSON")
	}
	d := pkg.RunDock.TemporaryPort
	if d == nil {
		return nil, fmt.Errorf("项目未声明临时端口及接口兼容性；不能确认换端口后功能正常")
	}
	if d.Version != 1 || d.Adapter != "vite" || d.Script != a.Args[1] || d.Port < 1024 || d.Port > 65535 || d.Host != "127.0.0.1" || d.Interfaces != "same-origin" {
		return nil, fmt.Errorf("临时端口声明不适用于当前入口；需要 Vite 单服务、回环地址及同源接口声明")
	}
	if !viteCommand.MatchString(strings.TrimSpace(pkg.Scripts[d.Script])) {
		return nil, fmt.Errorf("启动脚本不是直接的单服务 Vite 命令；不会盲目追加端口参数")
	}
	if len(pkg.Workspaces) > 0 && string(pkg.Workspaces) != "null" {
		return nil, fmt.Errorf("工作区入口尚未适配临时端口；不能确认实际启动的服务")
	}
	if strings.TrimSpace(pkg.Scripts["pre"+d.Script]) != "" || strings.TrimSpace(pkg.Scripts["post"+d.Script]) != "" {
		return nil, fmt.Errorf("启动包含额外的前置或后置脚本；这些脚本不会接收临时端口参数，暂不支持")
	}
	if a.HealthURL != "" {
		u, err := url.Parse(a.HealthURL)
		if err != nil || u.Scheme != "http" || u.User != nil || u.Hostname() != d.Host || u.Port() != strconv.Itoa(d.Port) {
			return nil, fmt.Errorf("健康检查地址与适配声明不一致；不能安全切换端口")
		}
	}
	return d, nil
}

// Apply clones launch configuration; it never writes project files or the DB.
func Apply(a *store.App, d *Definition, port int) (*store.App, error) {
	if d == nil || port < 1024 || port > 65535 || port == d.Port {
		return nil, fmt.Errorf("无效的临时端口")
	}
	copy := *a
	copy.Args = append(append([]string{}, a.Args...), "--", "--port", strconv.Itoa(port), "--host", d.Host, "--strictPort")
	copy.Env = map[string]string{}
	for key, value := range a.Env {
		copy.Env[key] = value
	}
	copy.PortHints = []int{port}
	copy.LastURL = ""
	copy.TemporaryPort = &store.TemporaryPort{OriginalPort: d.Port, Port: port, Host: d.Host}
	if a.HealthURL != "" {
		u, _ := url.Parse(a.HealthURL)
		u.Host = net.JoinHostPort(d.Host, strconv.Itoa(port))
		copy.HealthURL = u.String()
	}
	return &copy, nil
}
