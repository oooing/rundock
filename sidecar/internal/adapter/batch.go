package adapter

import (
	"strings"
)

// BatchAdapter 处理 .bat/.cmd 批处理脚本。
// 启动方式：cmd.exe /d /s /c call <script>
//   /d    禁用 AutoRun（cmd 启动时不执行注册表里的初始化命令，更可控）
//   /s    保持引号规则一致
//   /c    执行后退出
//   call  避免批处理调用其它脚本时过早返回，确保退出码正确传播
type BatchAdapter struct{}

func (BatchAdapter) Type() string { return "batch" }

// Detect：.bat/.cmd 后缀 = 100；其它后缀不主动认领（返回 0，让其它适配器或默认处理）。
func (BatchAdapter) Detect(_, entryFile string) int {
	lower := strings.ToLower(entryFile)
	if strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd") {
		return 100
	}
	return 0
}

func (BatchAdapter) Prepare(in *PrepareInput) (*PrepareOutput, error) {
	comspec := getComSpec()
	return &PrepareOutput{
		Cmd:  comspec,
		Args: []string{"/d", "/s", "/c", "call", in.EntryScript},
		Cwd:  in.Cwd,
		Env:  in.Env,
	}, nil
}

// DefaultBatchAdapter 是 Select 的兜底：当没有适配器匹配时，
// 仍假定入口是个可执行脚本，用同样的 cmd /c call 方式启动。
type DefaultBatchAdapter = BatchAdapter

// getComSpec 取命令解释器路径，优先 %ComSpec%。
func getComSpec() string {
	if cs := envOrEmpty("ComSpec"); cs != "" {
		return cs
	}
	return `C:\Windows\System32\cmd.exe`
}

// envOrEmpty 读环境变量（封装便于测试）。
func envOrEmpty(key string) string {
	return osLookupEnv(key)
}
