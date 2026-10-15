//go:build windows

package publisher

import "testing"

func TestCheckOutputPreservesUTF8AndDecodesNativeChinese(t *testing.T) {
	raw := "现代工具：中文\n" + string([]byte{0xd6, 0xd0, 0xce, 0xc4}) + "\r\nASCII failure"
	if got := decodeCheckOutputCodePage(raw, 936); got != "现代工具：中文\n中文\r\nASCII failure" {
		t.Fatalf("mixed encoding corrupted: %q", got)
	}
}
