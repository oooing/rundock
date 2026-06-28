// Package importer 处理"拖入 start.bat 即可生成 App 配置"的核心体验。
//
// 流程（全部只读，不执行任何脚本）：
//  1. 路径归一化与项目根推断：向上扫标志文件（package.json / docker-compose.yml / 锁文件 / 框架配置 / .env）
//  2. 适配器识别：adapter.Registry.Select 选出置信度最高的适配器
//  3. 静态提取：候选 name / cwd / env / 端口提示
//  4. 脚本哈希 + 风险扫描（security）
//  5. 返回 Candidate（候选配置 + 风险 + 是否需确认），等前端确认
package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/launcher-sidecar/internal/adapter"
	"github.com/launcher-sidecar/internal/security"
)

// Candidate 导入候选结果，前端确认后据此创建 App。
type Candidate struct {
	Name        string             `json:"name"`        // 候选名称
	EntryScript string             `json:"entryScript"` // 归一化后的入口路径
	Cwd         string             `json:"cwd"`         // 工作目录
	ProjectRoot string             `json:"projectRoot"` // 项目根
	AdapterType string             `json:"adapterType"` // 适配器类型
	Cmd         string             `json:"cmd"`         // prepare 后的可执行程序
	Args        []string           `json:"args"`        // 参数
	Env         map[string]string  `json:"env"`         // 候选环境变量
	PortHints   []int              `json:"portHints"`   // 候选端口提示
	ScriptHash  string             `json:"scriptHash"`  // 脚本哈希
	Findings    []security.Finding `json:"findings"`    // 风险扫描结果
	NeedsConfirm bool              `json:"needsConfirm"`// 是否需用户确认（危险或首次）
	Markers     []string           `json:"markers"`     // 命中的项目标志文件（用于说明推断依据）
}

// Import 对一个脚本路径做只读分析，返回候选配置。
// registry 为 nil 时使用仅含 BatchAdapter 的默认注册表。
func Import(scriptPath string, registry *adapter.Registry) (*Candidate, error) {
	abs, err := filepath.Abs(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("解析路径失败: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("找不到脚本文件。请确认是完整路径（含盘符，如 D:\\proj\\start.bat）。收到的输入: %q，解析后: %q", scriptPath, abs)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("路径是目录，请拖入脚本文件: %s", abs)
	}

	// 1. 推断项目根与工作目录
	root, markers := detectProjectRoot(abs)
	cwd := root
	if cwd == "" {
		cwd = filepath.Dir(abs)
	}

	// 2. 适配器识别
	if registry == nil {
		registry = adapter.NewRegistry()
		registry.Register(adapter.BatchAdapter{})
	}
	chosen := registry.Select(root, abs)
	adapterType := chosen.Type()

	// 3. 静态提取 name / env / 端口
	name := inferName(abs, root)
	env := scanEnvFromScript(abs)
	portHints := scanPortHints(abs)

	// 4. 哈希 + 风险扫描
	hash, _ := security.HashFile(abs)
	scriptText := readTextBestEffort(abs)
	findings := security.Scan(scriptText)
	if envContainsDanger(findings) {
		findings = append(findings, security.Finding{
			Level: security.RiskWarn, Rule: "dangerous_env",
			Message: "脚本注入了可能危险的环境变量，请人工核对", Snippet: "",
		})
	}
	needsConfirm := security.HasBlocking(findings) || hash == ""

	// prepare 命令
	po, err := chosen.Prepare(&adapter.PrepareInput{
		EntryScript: abs, Cwd: cwd, Env: env, PortHints: portHints,
	})
	if err != nil {
		// prepare 失败不致命，降级用 batch
		po = &adapter.PrepareOutput{Cmd: "", Args: []string{}, Cwd: cwd, Env: env}
	}

	return &Candidate{
		Name:        name,
		EntryScript: abs,
		Cwd:         cwd,
		ProjectRoot: root,
		AdapterType: adapterType,
		Cmd:         po.Cmd,
		Args:        po.Args,
		Env:         po.Env,
		PortHints:   portHints,
		ScriptHash:  hash,
		Findings:    findings,
		NeedsConfirm: needsConfirm,
		Markers:     markers,
	}, nil
}

// detectProjectRoot 从脚本向上扫描标志文件，找到项目根。
// 标志文件清单参考报告：package.json / docker-compose.yml / 锁文件 / 框架配置 / .env。
// 返回根目录与命中的标志文件名（用于 UI 说明推断依据）。找不到返回空。
func detectProjectRoot(scriptAbs string) (string, []string) {
	start := filepath.Dir(scriptAbs)
	markers := []string{
		"package.json", "docker-compose.yml", "docker-compose.yaml",
		"pnpm-lock.yaml", "yarn.lock", "package-lock.json",
		"go.mod", "Cargo.toml", "requirements.txt", "pyproject.toml",
		".env", ".env.local",
		"vite.config.js", "vite.config.ts", "next.config.js", "next.config.mjs",
		"nuxt.config.ts", "nuxt.config.js", "angular.json",
	}
	dir := start
	for i := 0; i < 6; i++ { // 最多向上 6 层
		var hit []string
		for _, m := range markers {
			if fileExists(filepath.Join(dir, m)) {
				hit = append(hit, m)
			}
		}
		// vite.config 等通配
		globs := []string{"vite.config.*", "next.config.*", "nuxt.config.*", "webpack.config.*"}
		for _, g := range globs {
			if ms, _ := filepath.Glob(filepath.Join(dir, g)); len(ms) > 0 {
				hit = append(hit, filepath.Base(ms[0]))
			}
		}
		if len(hit) > 0 {
			return dir, dedupStrings(hit)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", nil
}

// inferName 推断应用名。优先级：package.json name -> 目录名 -> 脚本名去后缀。
func inferName(scriptAbs, root string) string {
	if root != "" {
		if name := tryReadPackageName(root); name != "" {
			return name
		}
		// 用项目根目录名
		if base := filepath.Base(root); base != "" && base != "." && base != "/" {
			return base
		}
	}
	base := filepath.Base(scriptAbs)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// tryReadPackageName 读取 package.json 的 name 字段。
func tryReadPackageName(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	// 轻量提取，避免引整 json 包（adapter 已引；这里复用更简单的方式）
	s := string(data)
	// 粗匹配 "name": "xxx"
	idx := strings.Index(s, "\"name\"")
	if idx < 0 {
		return ""
	}
	rest := s[idx:]
	ci := strings.Index(rest, ":")
	if ci < 0 {
		return ""
	}
	rest = rest[ci+1:]
	q1 := strings.Index(rest, "\"")
	if q1 < 0 {
		return ""
	}
	rest = rest[q1+1:]
	q2 := strings.Index(rest, "\"")
	if q2 < 0 {
		return ""
	}
	name := strings.TrimSpace(rest[:q2])
	if name == "" {
		return ""
	}
	return name
}

// scanEnvFromScript 从脚本里抽取 set VAR=... / $env:VAR=... 候选环境变量。
// 仅作候选提示，最终以用户确认为准。敏感词（含 path/key/secret/token）默认不自动带入。
func scanEnvFromScript(scriptAbs string) map[string]string {
	out := map[string]string{}
	text := readTextBestEffort(scriptAbs)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// cmd: set NAME=VALUE
		if strings.HasPrefix(strings.ToLower(line), "set ") {
			rest := strings.TrimSpace(line[4:])
			if eq := strings.Index(rest, "="); eq > 0 {
				k := strings.TrimSpace(rest[:eq])
				v := strings.TrimSpace(rest[eq+1:])
				if !looksSensitive(k) {
					out[k] = v
				}
			}
		}
	}
	return out
}

// scanPortHints 从脚本里抽取端口提示。
// 只提取脚本中真实出现的端口（set PORT=、命令行 -p NNNN、URL 里的 :NNNN），
// 不再硬塞通用默认端口清单——避免每个项目都显示 5173/8000 等无关端口造成误解。
// 实际监听端口的发现由 probe（端口快照对比）负责，端口提示仅作辅助。
func scanPortHints(scriptAbs string) []int {
	seen := map[int]bool{}
	text := readTextBestEffort(scriptAbs)
	// set PORT=1234 / PORT:1234 / -p 1234 / --port 1234
	portRe := regexpMustCompile(`(?i)\b(?:port|server_port|app_port)\s*[=:]\s*(\d{2,5})|[-]{1,2}p(?:ort)?\s+(\d{2,5})`)
	for _, m := range portRe.FindAllStringSubmatch(text, -1) {
		pStr := m[1]
		if pStr == "" {
			pStr = m[2]
		}
		if p := atoiSafe(pStr); p > 0 && p < 65536 {
			seen[p] = true
		}
	}
	// 脚本里出现的 localhost:NNNN / 127.0.0.1:NNNN / 0.0.0.0:NNNN
	urlPortRe := regexpMustCompile(`(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::\]):(\d{2,5})`)
	for _, m := range urlPortRe.FindAllStringSubmatch(text, -1) {
		if p := atoiSafe(m[1]); p > 0 && p < 65536 {
			seen[p] = true
		}
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

// ----- 小工具 -----

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func readTextBestEffort(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func looksSensitive(k string) bool {
	lk := strings.ToLower(k)
	for _, w := range []string{"path", "key", "secret", "token", "password", "pwd", "passwd"} {
		if strings.Contains(lk, w) {
			return true
		}
	}
	return false
}

func envContainsDanger(_ []security.Finding) bool { return false } // 预留扩展
