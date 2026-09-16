package publisher

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type scanTarget struct {
	Path    string
	Content []byte
}

func scanSensitiveContent(files []scanTarget) ([]SensitiveFinding, error) {
	findings := []SensitiveFinding{}
	for _, file := range files {
		findings = append(findings, scanSensitiveFile(file.Path, file.Content)...)
	}
	return findings, nil
}

func scanSensitiveFile(rel string, content []byte) []SensitiveFinding {
	if looksBinary(content) {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	out := []SensitiveFinding{}
	for i, line := range lines {
		out = append(out, matchSensitiveLine(rel, i+1, line)...)
	}
	if location := pem.FindIndex(content); location != nil {
		out = append(out, SensitiveFinding{
			Path: rel, Kind: "private-key", Reason: "包含私钥块",
			Line:     strings.Count(string(content[:location[0]]), "\n") + 1,
			Redacted: "-----BEGIN *** PRIVATE KEY-----",
		})
	}
	return out
}

func matchSensitiveLine(rel string, lineNo int, line string) []SensitiveFinding {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	out := []SensitiveFinding{}
	for _, loc := range awsKey.FindAllStringIndex(trimmed, -1) {
		token := trimmed[loc[0]:loc[1]]
		if !placeholderValue(token) {
			out = append(out, SensitiveFinding{Path: rel, Kind: "aws-access-key", Reason: "疑似 AWS Access Key", Line: lineNo, Column: loc[0] + 1, Redacted: redactKnownToken(token)})
		}
	}
	for _, loc := range githubToken.FindAllStringIndex(trimmed, -1) {
		token := trimmed[loc[0]:loc[1]]
		if !placeholderValue(token) {
			out = append(out, SensitiveFinding{Path: rel, Kind: "github-token", Reason: "疑似 GitHub 令牌", Line: lineNo, Column: loc[0] + 1, Redacted: redactKnownToken(token)})
		}
	}
	for _, loc := range slackToken.FindAllStringIndex(trimmed, -1) {
		token := trimmed[loc[0]:loc[1]]
		if !placeholderValue(token) {
			out = append(out, SensitiveFinding{Path: rel, Kind: "slack-token", Reason: "疑似 Slack 令牌", Line: lineNo, Column: loc[0] + 1, Redacted: redactKnownToken(token)})
		}
	}
	if key, value, ok := assignmentValue(trimmed); ok && looksSecretKey(key) {
		literal, isLiteral := secretAssignmentLiteral(rel, value)
		if isLiteral && !placeholderValue(literal) && len(literal) >= 12 {
			kind, reason := "secret-assignment", "疑似真实密钥赋值"
			if descriptiveExample(literal) || descriptiveTestCredential(rel, literal) || !looksHighConfidenceSecret(literal) {
				kind, reason = "credential-literal-review", "固定字符串可能是模板或测试数据，未判定为真实密钥；可在手动处理入口复核"
			}
			out = append(out, SensitiveFinding{
				Path: rel, Kind: kind, Reason: reason, Line: lineNo,
				Redacted: "[secret assignment]=" + redactSecretLiteral(literal),
			})
		}
	}
	return out
}

func assignmentValue(line string) (string, string, bool) {
	stripped := line
	for _, prefix := range []string{"#", "//", "/*", "*", "--"} {
		if strings.HasPrefix(strings.TrimSpace(stripped), prefix) {
			stripped = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stripped), prefix))
		}
	}
	for _, sep := range []string{"=", ":"} {
		if i := unquotedAssignmentSeparator(stripped, sep[0]); i > 0 {
			key := strings.TrimSpace(stripped[:i])
			value := strings.TrimSpace(stripped[i+1:])
			if key != "" {
				return key, value, true
			}
		}
	}
	return "", "", false
}

func unquotedAssignmentSeparator(line string, separator byte) int {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			quote = c
			continue
		}
		if c == separator {
			return i
		}
	}
	return -1
}

// Source expressions (store.accessToken, get_password_hash(...), schema types)
// are not embedded credentials. Only inspect literal values for generic keys;
// provider token and private-key detection still scans the whole source.
func secretAssignmentLiteral(rel, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	if value[0] == '\'' || value[0] == '"' || value[0] == '`' {
		quote := value[0]
		for i := 1; i < len(value); i++ {
			if value[i] == '\\' {
				i++
				continue
			}
			if value[i] == quote {
				return value[1:i], true
			}
		}
		return "", false
	}
	base := strings.ToLower(filepath.Base(rel))
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".vue", ".rs", ".java", ".kt", ".swift", ".cs", ".c", ".h", ".cpp", ".rb", ".php", ".dart", ".json":
		return "", false
	}
	if i := strings.Index(value, " #"); i >= 0 {
		value = value[:i]
	}
	return strings.TrimSpace(value), true
}

func looksSecretKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.TrimPrefix(k, "export ")
	k = strings.Trim(k, `"'`)
	if i := strings.LastIndex(k, " "); i >= 0 {
		k = k[i+1:]
	}
	markers := []string{"password", "passwd", "secret", "token", "api_key", "apikey", "access_key", "private_key", "client_secret"}
	for _, marker := range markers {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func placeholderValue(value string) bool {
	v := strings.TrimSpace(value)
	v = strings.Trim(v, `"'`)
	if v == "" {
		return true
	}
	if (strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}")) ||
		(strings.HasPrefix(v, "{{") && strings.HasSuffix(v, "}}")) ||
		(strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">")) {
		return true
	}
	exact := map[string]bool{
		"replace-me": true, "changeme": true, "change_me": true, "change-me": true,
		"placeholder": true, "your_api_key": true, "your_api_key_here": true,
		"your-api-key": true, "your-token": true, "insert-key-here": true,
		"dummy": true, "xxx": true, "todo": true, "fixme": true, "secret": true,
		"password": true, "token": true, "example": true, "sample": true,
	}
	if exact[strings.ToLower(v)] {
		return true
	}

	return false
}

func looksHighConfidenceSecret(value string) bool {
	if len(value) < 16 {
		return false
	}
	if strings.ContainsAny(value, " \t") && !strings.HasPrefix(value, "-----") {
		return false
	}
	letters, digits, upper, lower := 0, 0, 0, 0
	counts := map[rune]int{}
	for _, r := range value {
		// Natural-language test messages are not random credential tokens.
		if r > unicode.MaxASCII {
			return false
		}
		counts[r]++
		switch {
		case unicode.IsLetter(r):
			letters++
			if unicode.IsUpper(r) {
				upper++
			} else {
				lower++
			}
		case unicode.IsDigit(r):
			digits++
		default:
			if unicode.IsSpace(r) {
				return false
			}
		}
	}
	length := float64(len([]rune(value)))
	entropy := 0.0
	for _, count := range counts {
		p := float64(count) / length
		entropy -= p * math.Log2(p)
	}
	if entropy < 3.5 {
		return false
	}
	if upper > 0 && lower > 0 || letters > 0 && digits > 0 {
		return true
	}
	// Long, random single-case keys remain blockable; ordinary prose separated
	// by hyphens is only a review hint, not proof of a leaked credential.
	return len(value) >= 32 && letters+digits == len([]rune(value)) && entropy >= 3.8
}

func descriptiveExample(value string) bool {
	return descriptiveCredentialWords(value, false)
}

func descriptiveTestCredential(rel, value string) bool {
	path := strings.ToLower(filepath.ToSlash(rel))
	base := filepath.Base(path)
	isTest := strings.HasPrefix(path, "tests/") || strings.Contains(path, "/tests/") || strings.HasPrefix(base, "test_") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasSuffix(base, "_test.go")
	return isTest && descriptiveCredentialWords(value, true)
}

func descriptiveCredentialWords(value string, testContext bool) bool {
	if len(value) > 100 {
		return false
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '_' })
	marker := false
	credentialWords := 0
	for _, part := range parts {
		if part == "" || len(part) > 12 {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
		switch part {
		case "test", "tests", "testing", "fixture", "example", "dummy", "fake", "replace", "development":
			marker = true
		}
		switch part {
		case "production", "secret", "key", "password", "credential", "token":
			credentialWords++
		}
	}
	return len(parts) > 1 && marker || testContext && len(parts) >= 4 && credentialWords >= 2
}

func redactKnownToken(token string) string {
	if len(token) <= 4 {
		return "***"
	}
	keep := 3
	if strings.HasPrefix(token, "ghp_") || strings.HasPrefix(token, "gho_") || strings.HasPrefix(token, "github_pat_") {
		keep = strings.Index(token, "_") + 1
	}
	if keep < 3 {
		keep = 3
	}
	if keep > 12 {
		keep = 12
	}
	return token[:keep] + strings.Repeat("*", 8)
}

func redactSecretLiteral(value string) string {
	if value == "" {
		return "***"
	}
	if len(value) <= 4 {
		return "***"
	}
	return value[:2] + strings.Repeat("*", 8)
}

func findingFingerprint(finding SensitiveFinding) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{finding.Path, finding.Kind, finding.Reason, finding.Redacted, strconv.Itoa(finding.Line), strconv.Itoa(finding.Column)}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func filterSensitiveExceptions(findings []SensitiveFinding, exceptions []SensitiveException, contentFP map[string]string) []SensitiveFinding {
	out := []SensitiveFinding{}
	for _, finding := range findings {
		skip := false
		fp := findingFingerprint(finding)
		finding.Fingerprint = fp
		finding.ContentFingerprint = contentFP[finding.Path]
		for _, ex := range exceptions {
			if filepath.ToSlash(ex.Path) != finding.Path {
				continue
			}
			if strings.TrimSpace(ex.Reason) == "" || strings.TrimSpace(ex.FindingFingerprint) == "" || strings.TrimSpace(ex.ContentFingerprint) == "" {
				continue
			}
			if ex.FindingFingerprint == fp && contentFP[finding.Path] == ex.ContentFingerprint {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, finding)
		}
	}
	return out
}

func looksBinary(content []byte) bool {
	n := len(content)
	if n > 2048 {
		n = 2048
	}
	if n == 0 {
		return false
	}
	for i := 0; i < n; i++ {
		if content[i] == 0 {
			return true
		}
	}
	return false
}

func readScanTargets(root string, paths []string) ([]scanTarget, error) {
	out := []scanTarget{}
	for _, rel := range paths {
		abs, err := secureProjectPath(root, rel, false)
		if err != nil {
			return nil, &Error{Code: "invalid_path", Message: "无法安全读取候选文件：" + rel + "：" + err.Error()}
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			continue
		}
		if isPathLink(info) {
			return nil, &Error{Code: "symlink_unsupported", Message: "候选版本不支持符号链接：" + rel}
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		out = append(out, scanTarget{Path: filepath.ToSlash(rel), Content: raw})
	}
	return out, nil
}

var (
	pem         = regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`)
	awsKey      = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	githubToken = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b`)
	slackToken  = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
)
