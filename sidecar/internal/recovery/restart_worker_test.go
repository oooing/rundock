//go:build windows

package recovery

import "testing"

func TestDirectWorkerEntryRejectsPathMentions(t *testing.T) {
	for _, tc := range []struct {
		exe, line string
		allow     bool
	}{
		{`C:\tools\node.exe`, `node C:\project\server.js`, true},
		{`C:\tools\node.exe`, `node C:\project\server.mjs --port 3333`, true},
		{`C:\tools\python.exe`, `python -u C:\project\server.py`, true},
		{`C:\tools\node.exe`, `node --require C:\project\server.js shared.js`, false},
		{`C:\tools\node.exe`, `node -e C:\project\server.js`, false},
		{`C:\tools\node.exe`, `node server.js`, false},
		{`C:\tools\node.exe`, `node C:\project-other\server.js`, false},
		{`C:\tools\python.exe`, `python -m C:\project\server.py`, false},
		{`C:\tools\editor.exe`, `editor C:\project\server.js`, false},
	} {
		p := Process{PID: 100, Created: "1000", Executable: tc.exe, CommandLine: tc.line}
		if got := DirectWorkerEntry(p, `C:\project`) != ""; got != tc.allow {
			t.Fatalf("%s: got %v", tc.line, got)
		}
	}
}
