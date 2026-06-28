package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// packageJSONScripts 读取项目根的 package.json，返回 name 与 scripts。
// 文件不存在或解析失败均返回零值与 false，由调用方决定降级策略。
func packageJSONScripts(projectRoot string) (name string, scripts map[string]string, ok bool) {
	data, err := os.ReadFile(filepath.Join(projectRoot, "package.json"))
	if err != nil {
		return "", nil, false
	}
	var pkg struct {
		Name    string            `json:"name"`
		Product string            `json:"productName"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", nil, false
	}
	if pkg.Name == "" {
		pkg.Name = pkg.Product
	}
	if pkg.Scripts == nil {
		pkg.Scripts = map[string]string{}
	}
	return pkg.Name, pkg.Scripts, true
}

// pickStartScript 在 scripts 里挑最合适的启动脚本名。
// 优先级：dev > start > serve。返回空串表示无可用脚本。
func pickStartScript(scripts map[string]string) string {
	for _, key := range []string{"dev", "start", "serve"} {
		if _, ok := scripts[key]; ok {
			return key
		}
	}
	return ""
}

// NPMAdapter 用 npm run 启动 package.json 项目。
type NPMAdapter struct{ runner string }

// NewNPMAdapter 创建 npm 适配器。
func NewNPMAdapter() NPMAdapter { return NPMAdapter{runner: "npm"} }

func (a NPMAdapter) Type() string { return "npm" }

// Detect：项目根有 package.json，且入口脚本是 .bat/.cmd 或 package.json 本身，给较高分。
func (a NPMAdapter) Detect(projectRoot, entryFile string) int {
	if _, _, ok := packageJSONScripts(projectRoot); !ok {
		return 0
	}
	lower := strings.ToLower(entryFile)
	switch {
	case strings.HasSuffix(lower, ".bat"), strings.HasSuffix(lower, ".cmd"):
		// 进一步看脚本内容是否含 npm run
		return npmDetectScoreFromScript(entryFile)
	case filepath.Base(lower) == "package.json":
		return 70
	}
	return 0
}

func (a NPMAdapter) Prepare(in *PrepareInput) (*PrepareOutput, error) {
	runner := a.runner
	if runner == "" {
		runner = "npm"
	}
	_, scripts, _ := packageJSONScripts(in.Cwd)
	script := pickStartScript(scripts)
	if script == "" {
		script = "dev"
	}
	return &PrepareOutput{
		Cmd:  runner + ".cmd", // Windows 下需要 .cmd 才能由 CreateProcess 找到
		Args: []string{"run", script},
		Cwd:  in.Cwd,
		Env:  in.Env,
	}, nil
}

// YarnAdapter 用 yarn 启动。
type YarnAdapter = NPMAdapter

func NewYarnAdapter() YarnAdapter { return NPMAdapter{runner: "yarn"} }

// PnpmAdapter 用 pnpm 启动。
type PnpmAdapter = NPMAdapter

func NewPnpmAdapter() PnpmAdapter { return NPMAdapter{runner: "pnpm"} }

// npmDetectScoreFromScript 粗读脚本内容，若含 npm/yarn/pnpm run 则加分。
func npmDetectScoreFromScript(entryFile string) int {
	data, err := os.ReadFile(entryFile)
	if err != nil {
		// 无法读取不加分，但也不否决（可能是权限问题）
		return 40
	}
	content := strings.ToLower(string(data))
	if strings.Contains(content, "npm run") ||
		strings.Contains(content, "yarn ") ||
		strings.Contains(content, "pnpm ") ||
		strings.Contains(content, "npm start") {
		return 85
	}
	return 40
}
