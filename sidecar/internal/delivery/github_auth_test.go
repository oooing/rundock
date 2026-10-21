package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGitHubAuthStatusSeparatesNetworkAndCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, output, code string
		success            bool
	}{
		{"observed timeout", "X Timeout trying to log in to github.com account fixture (keyring)", "github_connection_timeout", false},
		{"reset", "unexpected EOF synthetic-secret (keyring)", "github_connection_interrupted", false},
		{"dns", "lookup api.github.com: no such host", "github_dns_failed", false},
		{"proxy", "proxyconnect: connection refused", "github_connection_failed", false},
		{"certificate", "x509: certificate error", "github_certificate_invalid", false},
		{"invalid credential", "The token in default is invalid. synthetic-secret", "github_auth_required", false},
		{"signed out", "You are not logged into any GitHub hosts", "github_auth_required", false},
		{"401", "Bad credentials (HTTP 401)", "github_auth_required", false},
		{"unknown", "Failed to log in to fixture (keyring); synthetic-secret", "github_request_unconfirmed", false},
		{"403", "HTTP 403: forbidden", "github_request_unconfirmed", false},
		{"plaintext", "Logged in to github.com account fixture (config file)", "credential_store_required", true},
		{"keyring", "Logged in to github.com account fixture (keyring)", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &cappedWriter{max: 64 << 10}
			out.Write([]byte(tc.output))
			var runErr error
			if !tc.success {
				runErr = errors.New("exit 1")
			}
			err := decodeGitHubAuthStatus(context.Background(), runErr, out)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			code, msg := ErrorInfo(err)
			if code != tc.code || strings.Contains(msg, "synthetic-secret") {
				t.Fatalf("%s %s", code, msg)
			}
		})
	}
}

func TestGitHubAuthDeadlineCancellationAndOverflow(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	code, _ := ErrorInfo(decodeGitHubAuthStatus(ctx, errors.New("killed"), &cappedWriter{}))
	if code != "github_connection_timeout" {
		t.Fatal(code)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := decodeGitHubAuthStatus(ctx, errors.New("killed"), &cappedWriter{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	out := &cappedWriter{max: 10}
	out.Write([]byte("(keyring) truncated"))
	code, _ = ErrorInfo(decodeGitHubAuthStatus(context.Background(), nil, out))
	if code != "github_response_invalid" {
		t.Fatal(code)
	}
}

func TestAuthAndAPICallsShareProxyRouting(t *testing.T) {
	for _, args := range [][]string{{"auth", "status", "--active", "--hostname", "github.com"}, {"api", "user"}, {"api", "https://uploads.github.com/repos/a/b/releases/1/assets"}} {
		if !githubNetworkCommand(args) {
			t.Fatal("network command bypassed routing", args)
		}
	}
	for _, args := range [][]string{nil, {"--version"}, {"auth", "login"}, {"auth"}} {
		if githubNetworkCommand(args) {
			t.Fatal("unexpected network routing", args)
		}
	}
}
