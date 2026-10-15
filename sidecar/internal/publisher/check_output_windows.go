//go:build windows

package publisher

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/windows"
)

// Native cmd diagnostics use the OEM code page. Node and other modern tools
// often emit UTF-8; preserve those lines rather than converting all output.
func decodeCheckOutput(raw string) string { return decodeCheckOutputCodePage(raw, 1) }

func decodeCheckOutputCodePage(raw string, codePage uint32) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if utf8.ValidString(line) {
			continue
		}
		bytes := []byte(line)
		buffer := make([]uint16, len(bytes))
		n, err := windows.MultiByteToWideChar(codePage, 0, &bytes[0], int32(len(bytes)), &buffer[0], int32(len(buffer)))
		if err == nil {
			lines[i] = windows.UTF16ToString(buffer[:n])
		} else {
			lines[i] = strings.ToValidUTF8(line, "�")
		}
	}
	return strings.Join(lines, "\n")
}
