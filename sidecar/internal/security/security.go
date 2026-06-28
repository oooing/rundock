// Package security 实现脚本风险扫描与哈希白名单。
//
// 安全原则（来自报告）：
//   - 永远把"运行脚本"视为执行任意代码，而非"打开一个项目"。
//   - 导入只读分析，不执行；首次真正启动前必须用户确认。
//   - 高风险模式（删目录、格式化、注册表、网络命令、编码命令等）必须高亮告警。
//   - 脚本内容（哈希）+ 路径未变则记入白名单免再确认；变化则重新要求确认。
package security

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
)

// RiskLevel 风险等级。
type RiskLevel string

const (
	RiskInfo    RiskLevel = "info"
	RiskWarn    RiskLevel = "warn"
	RiskDanger  RiskLevel = "danger"
)

// Finding 一条风险发现。
type Finding struct {
	Level    RiskLevel `json:"level"`
	Rule     string    `json:"rule"`      // 规则名（简短标识）
	Message  string    `json:"message"`   // 面向用户的说明
	Snippet  string    `json:"snippet"`   // 匹配到的片段（便于高亮）
}

// riskRule 描述一条静态风险规则。
type riskRule struct {
	level   RiskLevel
	rule    string
	message string
	pattern *regexp.Regexp
}

// riskRules 按报告列出的高风险模式。按需扩展。
var riskRules = []riskRule{
	// 危险：递归删除、格式化
	{RiskDanger, "recursive_delete", "递归删除目录（rd /s /q 或 rmdir）可能导致数据丢失",
		regexp.MustCompile(`(?i)\b(rmd?ir|rd)\s+[/\\]s\s*[/\\]?q\b`)},
	{RiskDanger, "force_delete", "强制删除文件（del /f /s /q）",
		regexp.MustCompile(`(?i)\bdel(?:ete)?\s+[/\\]f\s*[/\\]?s\b`)},
	{RiskDanger, "format", "格式化磁盘（format）",
		regexp.MustCompile(`(?i)\bformat\s+[a-z]:`)},
	// 危险：系统级修改
	{RiskDanger, "registry_add", "修改注册表（reg add）",
		regexp.MustCompile(`(?i)\breg\s+(add|delete|import)\b`)},
	{RiskDanger, "service_create", "操作系统服务（sc create/delete）",
		regexp.MustCompile(`(?i)\bsc\s+(create|delete|config)\b`)},
	{RiskDanger, "netsh", "修改网络配置（netsh）",
		regexp.MustCompile(`(?i)\bnetsh\b`)},
	// 危险：编码命令（常见于混淆/恶意载荷）
	{RiskDanger, "encoded_command", "执行编码命令（-EncodedCommand），通常用于混淆，高风险",
		regexp.MustCompile(`(?i)(powershell|pwsh)[^\n]*-e(ncoded)?command\b`)},
	{RiskDanger, "iex_download", "下载并执行远程脚本（iex / Invoke-WebRequest 管道）",
		regexp.MustCompile(`(?i)(iex|invoke-expression|iwr|invoke-webrequest|invoke-restmethod)`)},
	// 警告：可能影响环境
	{RiskWarn, "setx", "持久设置环境变量（setx），会影响系统环境",
		regexp.MustCompile(`(?i)\bsetx\b`)},
	{RiskWarn, "taskkill_broad", "批量结束进程（taskkill /im），可能误杀",
		regexp.MustCompile(`(?i)\btaskkill\s+[/\\]im\b`)},
	{RiskWarn, "network_listen", "绑定/监听网络，可能开放端口",
		regexp.MustCompile(`(?i)\b(netstat|netsh\s+interface|listen)\b`)},
	{RiskWarn, "elevate", "尝试请求管理员权限（runas）",
		regexp.MustCompile(`(?i)\brunas\b|powershell\s+-verb\s+runas`)},
}

// Scan 对脚本文本做静态风险扫描，返回全部命中（按出现顺序）。
// 文本大小写不敏感匹配；每条规则的多个命中各算一条 Finding。
func Scan(text string) []Finding {
	var out []Finding
	for _, r := range riskRules {
		matches := r.pattern.FindAllString(text, -1)
		for _, m := range matches {
			out = append(out, Finding{
				Level:   r.level,
				Rule:    r.rule,
				Message: r.message,
				Snippet: strings.TrimSpace(m),
			})
		}
	}
	return out
}

// HasBlocking 当存在 danger 级别命中时返回 true（前端据此强制人工确认）。
func HasBlocking(findings []Finding) bool {
	for _, f := range findings {
		if f.Level == RiskDanger {
			return true
		}
	}
	return false
}

// HashFile 计算文件内容的 SHA256（十六进制）。文件不存在返回空串与错误。
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// HashText 计算文本的 SHA256（十六进制）。
func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
