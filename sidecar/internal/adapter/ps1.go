package adapter

import "strings"

// PS1Adapter 处理 .ps1 PowerShell 脚本。
// 启动方式：powershell.exe -NoProfile -ExecutionPolicy Bypass -File <script>
// 注意（来自报告）：Execution Policy 只是安全提示而非完整安全边界，
// 真正的"是否允许执行"由产品白名单 + 用户确认把关（security 模块），不依赖策略本身。
type PS1Adapter struct{}

func (PS1Adapter) Type() string { return "ps1" }

func (PS1Adapter) Detect(_, entryFile string) int {
	if strings.HasSuffix(strings.ToLower(entryFile), ".ps1") {
		return 100
	}
	return 0
}

func (PS1Adapter) Prepare(in *PrepareInput) (*PrepareOutput, error) {
	// 默认使用 Windows 自带 PowerShell；仅显式 LAUNCHER_PWSH 配置可覆盖。
	// 不自动把项目声明的 pwsh 命令替换为不兼容的 Windows PowerShell。
	ps := "powershell.exe"
	if p := osLookupEnv("LAUNCHER_PWSH"); p != "" {
		ps = p
	}
	return &PrepareOutput{
		Cmd: ps,
		Args: []string{
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy", "Bypass",
			"-File", in.EntryScript,
		},
		Cwd: in.Cwd,
		Env: in.Env,
	}, nil
}
