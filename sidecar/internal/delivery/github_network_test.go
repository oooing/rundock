package delivery

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/netproxy"
)

func TestCLIChildDoesNotInheritTokenOverrides(t *testing.T) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(name, "synthetic-token")
	}
	cmd, err := cliCommand(context.Background(), "auth", "status")
	if err != nil {
		t.Skip("GitHub CLI not installed")
	}
	for _, entry := range cmd.Env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN":
			t.Fatal("token override inherited")
		}
	}
	if os.Getenv("GH_TOKEN") != "synthetic-token" {
		t.Fatal("parent environment modified")
	}
}

func TestLiveGitHubAPIReadOnly(t *testing.T) {
	if os.Getenv("RUNDOCK_PROXY_LIVE_REPO") == "" {
		t.Skip("opt-in current GitHub connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var who struct {
		Login string `json:"login"`
	}
	if err := (CLI{}).JSON(ctx, "GET", "user", nil, &who); err != nil {
		t.Fatal(err)
	}
	if who.Login == "" {
		t.Fatal("empty authenticated response")
	}
	t.Log("current GitHub API read-only connectivity passed")
}

func TestUploadRoutingUsesUploadHost(t *testing.T) {
	if got := apiDestination([]string{"api", "--method", "POST", "https://uploads.github.com/repos/fixture/repo/releases/1/assets?name=package.zip"}); got != "https://uploads.github.com/" {
		t.Fatal(got)
	}
	if got := apiDestination([]string{"api", "repos/fixture/repo/releases"}); got != "https://api.github.com/" {
		t.Fatal(got)
	}
}

func TestUnavailableProxyHasActionableDeliveryMessage(t *testing.T) {
	code, message := ErrorInfo(&netproxy.UnavailableError{Proxy: "http://127.0.0.1:1"})
	if code != "github_proxy_unavailable" || !strings.Contains(message, "127.0.0.1:1") || !strings.Contains(message, "开启代理") {
		t.Fatalf("%s %s", code, message)
	}
}
