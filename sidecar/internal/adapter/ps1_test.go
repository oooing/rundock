package adapter

import (
	"reflect"
	"testing"
)

func TestPS1UsesWindowsPowerShellUnlessExplicitlyConfigured(t *testing.T) {
	for _, host := range []string{"", `C:\Program Files\PowerShell\7\pwsh.exe`} {
		t.Setenv("LAUNCHER_PWSH", host)
		input := &PrepareInput{EntryScript: `C:\project with spaces\start.ps1`, Cwd: `C:\project with spaces`}
		out, err := (PS1Adapter{}).Prepare(input)
		if err != nil {
			t.Fatal(err)
		}
		want := host
		if want == "" {
			want = "powershell.exe"
		}
		if out.Cmd != want || out.Cwd != input.Cwd || !reflect.DeepEqual(out.Args, []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", input.EntryScript}) {
			t.Fatalf("unexpected PowerShell launch: %#v", out)
		}
	}
}
