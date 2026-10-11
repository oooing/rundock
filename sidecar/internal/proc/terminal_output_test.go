package proc

import "testing"

func TestStripANSI(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"WXT clock", "\x1b[32m√\x1b[0m Started dev server @ http://localhost:3000\x1b[95G13:01:58", "√ Started dev server @ http://localhost:3000 13:01:58"},
		{"colors inside URL", "http://local\x1b[34mhost:3000\x1b[0m", "http://localhost:3000"},
		{"title BEL", "\x1b]0;npm\x07ready", "ready"},
		{"title ST", "\x1b]0;npm\x1b\\ready", "ready"},
		{"hyperlink", "\x1b]8;;http://localhost:3000\x1b\\http://localhost:3000\x1b]8;;\x1b\\", "http://localhost:3000"},
		{"CSI nonletter final", "\x1b[200~ready\x1b[201~", "ready"},
		{"erase and unicode", "\x1b[2K✓ 启动完成", "✓ 启动完成"},
		{"cursor row column", "ready\x1b[2;90H13:01:58", "ready 13:01:58"},
		{"incomplete control", "ready\x1b[", "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSI(tc.input); got != tc.want {
				t.Fatalf("stripANSI() = %q, want %q", got, tc.want)
			}
		})
	}
}
