package recovery

import (
	"strings"
	"testing"
)

func TestMiniflareWorkerClassification(t *testing.T) {
	internal := `"C:\project with space\node_modules\@cloudflare\workerd-windows-64\bin\workerd.exe" serve --binary --experimental --socket-addr=entry=127.0.0.1:0 --external-addr=loopback=127.0.0.1:12307 --control-fd=3 -`
	for _, tc := range []struct {
		name, executable, command, created string
		want                               bool
	}{
		{"miniflare runtime", `C:\tools\workerd.exe`, internal, "1000", true},
		{"public wrangler proxy", `C:\tools\node.exe`, `node wrangler.js dev --port 8788`, "1000", false},
		{"fixed workerd proxy remains required", `C:\tools\workerd.exe`, strings.Replace(internal, "entry=127.0.0.1:0", "entry=127.0.0.1:8788", 1), "1000", false},
		{"standalone workerd", `C:\tools\workerd.exe`, `workerd serve app.capnp --socket-addr=http=127.0.0.1:8080`, "1000", false},
		{"ordinary backend", `C:\tools\node.exe`, internal, "1000", false},
		{"missing command evidence", `C:\tools\workerd.exe`, "", "1000", false},
		{"different process identity", `C:\tools\workerd.exe`, internal, "2000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := RuntimeSnapshot{Processes: []Process{{PID: 100, Created: tc.created, Executable: tc.executable, CommandLine: tc.command}}}
			if got := AuxiliaryServiceProcesses(s, map[int]uint64{100: 1000})[100]; got != tc.want {
				t.Fatalf("auxiliary=%v, want %v", got, tc.want)
			}
		})
	}
}
