package launcher

import (
	"github.com/launcher-sidecar/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestPreferredOpenURL(t *testing.T) {
	services := []*store.AppService{
		{Role: store.RoleBackend, URL: "http://localhost:4311", Health: "healthy"},
		{Role: store.RoleFrontend, URL: "http://localhost:4310", Health: "healthy"},
	}
	if got := PreferredOpenURL("", services[0].URL, services); got != services[1].URL {
		t.Fatal(got)
	}
	entry := filepath.Join(t.TempDir(), "start.cmd")
	if err := os.WriteFile(entry, []byte("rem rundock:open http://127.0.0.1:4310/library\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := readStartupReadiness(entry)
	if err != nil || len(config.urls) != 0 {
		t.Fatal("open must not create readiness requirement", err)
	}
	for _, svcs := range [][]*store.AppService{nil, services[:1], services} {
		if got := PreferredOpenURL(entry, services[0].URL, svcs); got != "http://127.0.0.1:4310/library" {
			t.Fatal(got)
		}
	}
	if got := PreferredOpenURL("", "http://localhost:3000/path", nil); got != "http://localhost:3000/path" {
		t.Fatal(got)
	}
	auxiliary := &store.AppService{Port: 5284, Role: store.RoleFrontend, Health: "healthy", URL: "http://localhost:5284", StatusScope: "auxiliary"}
	for _, lastURL := range []string{auxiliary.URL, "http://127.0.0.1:5284/path", "http://[::1]:5284/"} {
		if got := PreferredOpenURL("", lastURL, []*store.AppService{auxiliary, services[0]}); got != services[0].URL {
			t.Fatal("auxiliary port became main project URL", got)
		}
	}
	if got := PreferredOpenURL("", "https://example.com:5284/path", []*store.AppService{auxiliary, services[0]}); got != "https://example.com:5284/path" {
		t.Fatal("unrelated remote URL was treated as a local auxiliary port", got)
	}
	for _, text := range []string{
		"rem rundock:open https://example.com:443/",
		"rem rundock:open http://localhost/",
		"rem rundock:open http://localhost:4310/\nrem rundock:open http://localhost:4311/",
	} {
		if err := os.WriteFile(entry, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readStartupReadiness(entry); err == nil {
			t.Fatal("invalid open accepted", text)
		}
	}
}
