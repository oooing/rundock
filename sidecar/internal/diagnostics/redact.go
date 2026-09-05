package diagnostics

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var (
	ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	// Header and shell/config forms. Keep the key so the remaining message is
	// still useful for diagnosis.
	authorizationPattern = regexp.MustCompile(`(?i)\b(authorization["']?\s*[:=]\s*(?:bearer|basic)\s+)[^\s,;]+`)
	credentialPattern    = regexp.MustCompile(`(?i)\b((?:password|passwd|pwd|token|access[_-]?token|refresh[_-]?token|api[_-]?key|apikey|client[_-]?secret|secret|cookie|set-cookie)["']?\s*[:=]\s*)('[^']*'|"[^"]*"|[^\s,;]+)`)
	urlUserInfoPattern   = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)
	urlQueryPattern      = regexp.MustCompile(`(?i)([?&](?:token|access_token|api[_-]?key|apikey|password|secret|signature)=)[^&#\s]+`)
	githubTokenPattern   = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
	jwtPattern           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	awsKeyPattern        = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
	npmTokenPattern      = regexp.MustCompile(`\bnpm_[A-Za-z0-9]{20,}\b`)
	pemPattern           = regexp.MustCompile(`(?s)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`)
)

var sensitiveKeyPattern = regexp.MustCompile(`(?i)(password|passwd|pwd|token|api.?key|secret|authorization|cookie|private.?key|credential|signature)`)

// Redact removes common credentials from diagnostic text. It deliberately
// favors hiding too much over persisting a usable secret.
func Redact(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	value = strings.ReplaceAll(value, "\x00", "")
	value = pemPattern.ReplaceAllString(value, redacted)
	value = authorizationPattern.ReplaceAllString(value, `${1}`+redacted)
	value = credentialPattern.ReplaceAllString(value, `${1}`+redacted)
	value = urlUserInfoPattern.ReplaceAllString(value, `${1}`+redacted+`@`)
	value = urlQueryPattern.ReplaceAllString(value, `${1}`+redacted)
	value = githubTokenPattern.ReplaceAllString(value, redacted)
	value = jwtPattern.ReplaceAllString(value, redacted)
	value = awsKeyPattern.ReplaceAllString(value, redacted)
	value = npmTokenPattern.ReplaceAllString(value, redacted)
	return strings.TrimSpace(value)
}

func redactContext(context map[string]any) map[string]any {
	if len(context) == 0 {
		return nil
	}
	// Normalize structs and typed maps through JSON, then recursively redact.
	raw, err := json.Marshal(context)
	if err != nil {
		return map[string]any{"redactionError": "context omitted"}
	}
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return map[string]any{"redactionError": "context omitted"}
	}
	for key, value := range normalized {
		normalized[key] = redactValue(key, value, 0)
	}
	return normalized
}

func redactValue(key string, value any, depth int) any {
	if sensitiveKeyPattern.MatchString(key) {
		return redacted
	}
	if depth >= 8 {
		return "[TRUNCATED]"
	}
	switch typed := value.(type) {
	case string:
		return Redact(typed)
	case []any:
		if len(typed) > 100 {
			typed = typed[:100]
		}
		for i := range typed {
			typed[i] = redactValue("", typed[i], depth+1)
		}
		return typed
	case map[string]any:
		if len(typed) > 100 {
			trimmed := make(map[string]any, 100)
			count := 0
			for childKey, childValue := range typed {
				if count == 100 {
					break
				}
				trimmed[childKey] = redactValue(childKey, childValue, depth+1)
				count++
			}
			return trimmed
		}
		for childKey, childValue := range typed {
			typed[childKey] = redactValue(childKey, childValue, depth+1)
		}
		return typed
	default:
		return typed
	}
}

func truncateText(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	const suffix = " …[TRUNCATED]"
	limit := maxBytes - len(suffix)
	if limit < 0 {
		return fmt.Sprintf("%.*s", maxBytes, suffix)
	}
	for limit > 0 && (value[limit]&0xC0) == 0x80 {
		limit--
	}
	return value[:limit] + suffix
}
