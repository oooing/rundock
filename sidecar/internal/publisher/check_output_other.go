//go:build !windows

package publisher

import "strings"

func decodeCheckOutput(raw string) string { return strings.ToValidUTF8(raw, "�") }
