package api

import "github.com/launcher-sidecar/internal/security"

// hashFile 包装 security.HashFile，便于 handlers 引用。
func hashFile(path string) (string, error) {
	return security.HashFile(path)
}
