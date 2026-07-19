package probe

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	rolePortAssignment = regexp.MustCompile(`(?i)^(?:set\s+)?["']?\$?(frontend|backend)[a-z0-9_]*port["']?\s*=\s*["']?(\d{2,5})`)
	roleURLAssignment  = regexp.MustCompile(`(?i)^(?:set\s+)?["']?\$?(frontend|backend)[a-z0-9_]*(?:url|uri)["']?\s*=\s*["'][^"']*:(\d{2,5})`)
	rolePortText       = regexp.MustCompile(`(?i)\b(frontend|backend)\b.{0,120}?\bport\s*(?:=|:)?\s*(\d{2,5})`)
	roleURLText        = regexp.MustCompile(`(?i)\b(frontend|backend)\b.{0,120}?https?://[^\s"']+:(\d{2,5})`)
	quotedScript       = regexp.MustCompile(`(?i)["']([^"']+\.(?:bat|cmd|ps1))["']`)
	unquotedScript     = regexp.MustCompile(`(?i)([^\s"']+\.(?:bat|cmd|ps1))`)
	dp0                = regexp.MustCompile(`(?i)%~dp0`)
)

// DeclaredRoles 读取入口脚本及其直接引用的一层脚本，提取明确声明的前后端端口。
func DeclaredRoles(entryScript string) map[int]Role {
	roles := map[int]Role{}
	conflicts := map[int]bool{}
	entryText := readScript(entryScript)
	scanDeclaredRoles(entryText, roles, conflicts)

	// ponytail: 只跟一层脚本引用；出现多层启动器时再改成带去重的递归扫描。
	for _, ref := range referencedScripts(entryScript, entryText) {
		scanDeclaredRoles(readScript(ref), roles, conflicts)
	}
	return roles
}

func scanDeclaredRoles(text string, roles map[int]Role, conflicts map[int]bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if line == "" || strings.HasPrefix(lower, "rem ") || strings.HasPrefix(line, "::") || strings.HasPrefix(line, "#") {
			continue
		}
		for _, re := range []*regexp.Regexp{rolePortAssignment, roleURLAssignment} {
			if m := re.FindStringSubmatch(line); len(m) == 3 {
				addDeclaredRole(roles, conflicts, m[1], m[2])
			}
		}
		// 同一行同时出现 frontend/backend 时无法可靠地把数字归属给其中一个。
		if strings.Contains(lower, "frontend") && strings.Contains(lower, "backend") {
			continue
		}
		for _, re := range []*regexp.Regexp{rolePortText, roleURLText} {
			if m := re.FindStringSubmatch(line); len(m) == 3 {
				addDeclaredRole(roles, conflicts, m[1], m[2])
			}
		}
	}
}

func addDeclaredRole(roles map[int]Role, conflicts map[int]bool, roleText, portText string) {
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || conflicts[port] {
		return
	}
	role := Role(strings.ToLower(roleText))
	if old, ok := roles[port]; ok && old != role {
		delete(roles, port)
		conflicts[port] = true
		return
	}
	roles[port] = role
}

func referencedScripts(entryScript, text string) []string {
	dir := filepath.Dir(entryScript)
	seen := map[string]bool{filepath.Clean(entryScript): true}
	var out []string
	for _, re := range []*regexp.Regexp{quotedScript, unquotedScript} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			ref := dp0.ReplaceAllString(m[1], dir+string(filepath.Separator))
			if strings.ContainsAny(ref, "%$") {
				continue
			}
			if !filepath.IsAbs(ref) {
				ref = filepath.Join(dir, ref)
			}
			ref = filepath.Clean(ref)
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	return out
}

func readScript(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
