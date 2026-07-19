package probe

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name     string
		in       ClassifyInput
		wantRole Role
		wantConf Confidence
	}{
		// --- DB 端口(高,短路,优先于一切)---
		{"db port 5432", ClassifyInput{Port: 5432}, RoleDatabase, ConfHigh},
		{"db port 6379 redis", ClassifyInput{Port: 6379}, RoleDatabase, ConfHigh},
		{"db port 3306 mysql", ClassifyInput{Port: 3306}, RoleDatabase, ConfHigh},
		{"db port 27017 mongo", ClassifyInput{Port: 27017}, RoleDatabase, ConfHigh},
		{"db port overrides vite header", ClassifyInput{Port: 5432, Headers: map[string]string{"server": "vite"}}, RoleDatabase, ConfHigh},

		// --- 响应头(高)---
		{"header vite", ClassifyInput{Headers: map[string]string{"server": "vite"}}, RoleFrontend, ConfHigh},
		{"header express is ambiguous", ClassifyInput{Headers: map[string]string{"x-powered-by": "Express"}}, RoleUnknown, ConfNone},
		{"header server uvicorn", ClassifyInput{Headers: map[string]string{"server": "uvicorn"}}, RoleBackend, ConfHigh},
		{"header next.js -> frontend", ClassifyInput{Headers: map[string]string{"x-powered-by": "Next.js"}}, RoleFrontend, ConfHigh},
		// 键大小写不敏感:调用方可能传 Go canonical 大写键(Server / X-Powered-By)。
		{"header canonical-case keys", ClassifyInput{Headers: map[string]string{"Server": "Vite", "X-Powered-By": "Express"}}, RoleFrontend, ConfHigh},

		// --- Title/Content-Type(中)---
		{"title vite+react no header", ClassifyInput{Title: "Vite + React"}, RoleFrontend, ConfMedium},
		{"generic spa mount is ambiguous", ClassifyInput{Body: `<div id="root"></div>`}, RoleUnknown, ConfNone},
		{"vite client body", ClassifyInput{Body: `<script type="module" src="/@vite/client"></script>`}, RoleFrontend, ConfMedium},
		{"content-type json no header", ClassifyInput{BodyCT: "application/json"}, RoleBackend, ConfMedium},
		{"declared frontend overrides uvicorn", ClassifyInput{DeclaredRole: RoleFrontend, Headers: map[string]string{"server": "uvicorn"}}, RoleFrontend, ConfHigh},
		{"frontend log overrides uvicorn", ClassifyInput{LogHints: []string{"Frontend running at http://localhost:1111"}, Headers: map[string]string{"server": "uvicorn"}}, RoleFrontend, ConfHigh},

		// --- 日志(低)---
		{"log vite version", ClassifyInput{LogHints: []string{"VITE v5.0.0 ready in 312 ms"}}, RoleFrontend, ConfLow},
		{"log uvicorn running", ClassifyInput{LogHints: []string{"INFO:     Uvicorn running on http://127.0.0.1:8000"}}, RoleBackend, ConfLow},

		// --- 都不命中 ---
		{"empty input", ClassifyInput{}, RoleUnknown, ConfNone},
		{"random port no signals", ClassifyInput{Port: 12345}, RoleUnknown, ConfNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotRole, gotConf := Classify(c.in)
			if gotRole != c.wantRole {
				t.Errorf("role = %q, want %q", gotRole, c.wantRole)
			}
			if gotConf != c.wantConf {
				t.Errorf("conf = %v, want %v", gotConf, c.wantConf)
			}
		})
	}
}
