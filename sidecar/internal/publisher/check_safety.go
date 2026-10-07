package publisher

import (
	"os/exec"
	"regexp"
	"strings"
)

// Only resolve a literal first command. Shell expressions and builtins still
// run in the configured shell; they are never inferred to have passed.
func missingDirectCheckTool(command string) string {
	s := strings.TrimSpace(command)
	if s == "" {
		return ""
	}
	first := ""
	if s[0] == '"' || s[0] == '\'' {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			first = s[1 : 1+end]
		}
	} else {
		first = strings.Fields(s)[0]
	}
	if first == "" || strings.ContainsAny(first, "=$;&|`(){}<>%") {
		return ""
	}
	switch strings.ToLower(first) {
	case "cd", "chdir", "echo", "set", "setlocal", "endlocal", "if", "for", "call", "exit", "dir", "type", "copy", "move", "del", "erase", "ren", "rename", "mkdir", "md", "rmdir", "rd", "start", "rem", "pushd", "popd", "path", "pause", "ver", "verify", "vol", "cls", "color", "title", "chcp", "export", "source", "exec", "test", "true", "false", ":", ".", "[":
		return ""
	}
	// Relative executables are checked in the command's configured cwd by the
	// shell, not relative to the launcher's process directory.
	if strings.HasPrefix(first, "./") || strings.HasPrefix(first, ".\\") || strings.HasPrefix(first, "../") || strings.HasPrefix(first, "..\\") {
		return ""
	}
	_, err := exec.LookPath(first)
	if err != nil {
		return first
	}
	return ""
}

func missingCheckToolReason(tool string) string {
	label := redactSensitiveLog(tool)
	if strings.EqualFold(tool, "pwsh") || strings.EqualFold(tool, "pwsh.exe") {
		label = "pwsh（PowerShell 7）"
	}
	return "找不到检查工具：" + label + "。RunDock 当前进程的 PATH 无法定位该工具，检查尚未执行。"
}

var privateKeyLog = regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)

func redactSensitiveLog(text string) string {
	text = redact(text)
	for _, pattern := range []*regexp.Regexp{githubToken, awsKey, slackToken} {
		text = pattern.ReplaceAllString(text, "[redacted credential]")
	}
	text = privateKeyLog.ReplaceAllString(text, "[redacted private key]")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// Redaction is deliberately stricter than submission confidence: even
		// low-confidence literals must not be echoed into the UI or API logs.
		if key, value, ok := assignmentValue(strings.TrimSpace(line)); ok && looksSecretKey(key) && !placeholderValue(value) {
			lines[i] = strings.ReplaceAll(line, value, "[redacted credential]")
		}
	}
	return strings.Join(lines, "\n")
}
