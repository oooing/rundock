package logbus

import "testing"

func TestExtractURLs(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"Local:   http://localhost:5173/", []string{"http://localhost:5173/"}},
		{"ready - started server on http://127.0.0.1:3000", []string{"http://127.0.0.1:3000"}},
		{"Vite v5.0.0 ready in 320 ms", []string{}}, // 无 URL，只有 ready 关键词
		{"listening on port 8080", []string{}},       // 裸端口不算 URL
		{"open http://localhost:3000 and https://127.0.0.1:8443/a", []string{"http://localhost:3000", "https://127.0.0.1:8443/a"}},
	}
	for i, c := range cases {
		got := ExtractURLs(c.line)
		if len(got) != len(c.want) {
			t.Errorf("case %d %q: got %v want %v", i, c.line, got, c.want)
			continue
		}
		for j := range got {
			if got[j] != c.want[j] {
				t.Errorf("case %d %q [%d]: got %q want %q", i, c.line, j, got[j], c.want[j])
			}
		}
	}
}

func TestPortFromURL(t *testing.T) {
	cases := map[string]int{
		"http://localhost:5173/": 5173,
		"https://127.0.0.1:8443": 8443,
		"http://localhost/":      0,
		"nope":                   0,
	}
	for u, want := range cases {
		if got := portFromURL(u); got != want {
			t.Errorf("portFromURL(%q) = %d want %d", u, got, want)
		}
	}
}

func TestParseLineReady(t *testing.T) {
	evs := ParseLine("Vite v5.0.0 ready in 320 ms")
	found := false
	for _, e := range evs {
		if e.Kind == EventReady {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a ready event, got %v", evs)
	}
}

func TestParseLineURLAndPort(t *testing.T) {
	evs := ParseLine("Local:   http://localhost:5173/")
	var gotURL, gotPort bool
	for _, e := range evs {
		if e.Kind == EventURLListen && e.URL == "http://localhost:5173/" {
			gotURL = true
		}
		if e.Kind == EventPortListen && e.Port == 5173 {
			gotPort = true
		}
	}
	if !gotURL {
		t.Errorf("expected url_detected event, got %v", evs)
	}
	if !gotPort {
		t.Errorf("expected port_listen(5173) event, got %v", evs)
	}
}

func TestInferLevel(t *testing.T) {
	if l := InferLevel("stdout", "all good"); l != "info" {
		t.Errorf("info case: got %s", l)
	}
	if l := InferLevel("stderr", "boom"); l != "error" {
		t.Errorf("stderr case: got %s", l)
	}
	if l := InferLevel("stdout", "Warning: low memory"); l != "warn" {
		t.Errorf("warn case: got %s", l)
	}
	if l := InferLevel("stdout", "Error: failed to bind"); l != "error" {
		t.Errorf("error case: got %s", l)
	}
}
