package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGitHubResponseErrorsAreActionableAndDoNotLeakOutput(t *testing.T) {
	for _, tc := range []struct{ stderr, code string }{
		{"Get https://user:synthetic-secret@api.github.com: net/http: TLS handshake timeout", "github_connection_timeout"},
		{"unexpected EOF: Bearer synthetic-secret", "github_connection_interrupted"},
		{"stream error: stream ID 1; INTERNAL_ERROR; received from peer", "github_connection_interrupted"},
		{"dial tcp: connectex: connection refused", "github_connection_failed"},
		{"lookup api.github.com: no such host", "github_dns_failed"},
		{"x509: certificate signed by unknown authority", "github_certificate_invalid"},
		{"unknown error with synthetic-secret", "github_request_unconfirmed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			result := &cappedWriter{max: 1024}
			err := decodeGitHubResponse(context.Background(), errors.New("exit 1"), result, tc.stderr, nil)
			code, msg := ErrorInfo(fmt.Errorf("%s：%w", githubOperation("GET", "repos/example/app/contents/.github/workflows/build.yml"), err))
			if code != tc.code || !strings.Contains(msg, "自动发布配置") || strings.Contains(msg, "synthetic-secret") {
				t.Fatalf("%s %s", code, msg)
			}
		})
	}
}

func TestGitHubReadRetryIsBoundedAndNeverRepeatsWrites(t *testing.T) {
	for _, tc := range []struct {
		method  string
		err     error
		recover bool
		calls   int
	}{
		{"GET", githubTransportError("unexpected EOF"), true, 2},
		{"GET", githubTransportError("timeout"), false, 2},
		{"HEAD", &HTTPError{503}, true, 2},
		{"GET", &HTTPError{401}, false, 1},
		{"GET", &HTTPError{403}, false, 1},
		{"GET", &HTTPError{404}, false, 1},
		{"GET", githubTransportError("x509 certificate"), false, 1},
		{"POST", githubTransportError("timeout"), false, 1},
		{"PATCH", &HTTPError{502}, false, 1},
		{"DELETE", githubTransportError("EOF"), false, 1},
	} {
		t.Run(fmt.Sprintf("%s-%v", tc.method, tc.err), func(t *testing.T) {
			calls := 0
			err := readRetry(context.Background(), tc.method, func() error {
				calls++
				if calls > 1 && tc.recover {
					return nil
				}
				return tc.err
			}, func(context.Context) error { return nil })
			if calls != tc.calls || (err == nil) != tc.recover {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := readRetry(ctx, "GET", func() error { calls++; return githubTransportError("EOF") }, func(context.Context) error { cancel(); return ctx.Err() })
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %d %v", calls, err)
	}
}

func TestGitHubDecodePreservesHTTPStatusAndRejectsPartialResponse(t *testing.T) {
	for _, tc := range []struct {
		wire   string
		runErr error
		status int
		code   string
	}{
		{"HTTP/2.0 200 OK\r\nContent-Type: application/json\r\n\r\n{\"ok\":true}", nil, 0, ""},
		{"HTTP/2.0 404 Not Found\n\n{}", errors.New("exit 1"), 404, "github_http_404"},
		{"HTTP/2.0 200 OK\n\n{\"ok\":", nil, 0, "github_response_invalid"},
		{"HTTP/2.0 200 OK", nil, 0, "github_response_invalid"},
	} {
		result := &cappedWriter{max: 1024}
		result.Write([]byte(tc.wire))
		var out map[string]any
		err := decodeGitHubResponse(context.Background(), tc.runErr, result, "", &out)
		if tc.code == "" {
			if err != nil || out["ok"] != true {
				t.Fatal(err, out)
			}
			continue
		}
		code, _ := ErrorInfo(err)
		if code != tc.code || tc.status != 0 && !isStatus(err, tc.status) {
			t.Fatalf("%s %v", code, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := decodeGitHubResponse(ctx, errors.New("killed"), &cappedWriter{}, "", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
