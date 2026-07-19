package launcher

import (
	"testing"

	"github.com/launcher-sidecar/internal/probe"
)

func TestIsServicePort(t *testing.T) {
	cases := []struct {
		name        string
		port        int
		log, hinted bool
		declared    probe.Role
		root        *probe.HealthResult
		want        bool
	}{
		{"internal listener", 8882, false, false, "", nil, false},
		{"http 404 still speaks HTTP", 9000, false, false, "", &probe.HealthResult{StatusCode: 404}, true},
		{"script hint", 3000, false, true, "", nil, true},
		{"declared backend", 8001, false, false, probe.RoleBackend, nil, true},
		{"log URL", 7000, true, false, "", nil, true},
		{"database", 5432, false, false, "", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isServicePort(c.port, c.log, c.declared, c.hinted, c.root); got != c.want {
				t.Fatalf("isServicePort() = %v, want %v", got, c.want)
			}
		})
	}
}
