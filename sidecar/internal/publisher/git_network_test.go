package publisher

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/launcher-sidecar/internal/netproxy"
)

func isolatedNetworkEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	writeTestFile(t, config, "")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

type unavailableNetworkRunner struct{ deliveryGitRunner }

func (unavailableNetworkRunner) CheckNetwork(context.Context, string, string) (string, error) {
	return "", &netproxy.UnavailableError{Proxy: "http://127.0.0.1:1"}
}

func TestUnavailableProxyStopsReleaseBeforeVersionCommitOrBuild(t *testing.T) {
	f := newDeliveryFixture(t, "normal", false)
	before := runGit(t, f.repo, "rev-parse", "HEAD")
	f.svc.runner = unavailableNetworkRunner{}
	run := f.start(t)
	final := f.settle(t, run.ID)
	if final.Status != "failed" || final.Stage != "preparing" || !strings.Contains(final.ErrorMessage, "本机网络代理") {
		t.Fatalf("%+v", final)
	}
	if f.builds() != 0 || f.gh.creates != 0 || f.gh.attempts != 0 {
		t.Fatal("offline preflight ran a build or write")
	}
	if after := runGit(t, f.repo, "rev-parse", "HEAD"); after != before {
		t.Fatal("offline preflight committed changes")
	}
	version, err := os.ReadFile(filepath.Join(f.repo, "package.json"))
	if err != nil || !strings.Contains(string(version), "1.0.0") {
		t.Fatal("offline preflight changed version")
	}
}

// Optional verification against the user's repository. CheckNetwork only runs
// ls-remote; it never fetches, pushes, tags, or creates a GitHub release.
func TestLiveGitNetworkReadOnly(t *testing.T) {
	repo := os.Getenv("RUNDOCK_PROXY_LIVE_REPO")
	if repo == "" {
		t.Skip("opt-in current repository connectivity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	notice, err := (execRunner{}).CheckNetwork(ctx, repo, "origin")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("current repository read-only connectivity passed", notice)
}
func unusedProxyAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	value := "http://" + l.Addr().String()
	l.Close()
	return value
}

func TestRealGitRecoversURLSpecificProxyWithoutChangingConfigOrWritingRemote(t *testing.T) {
	isolatedNetworkEnvironment(t)
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	_ = svc
	bare := filepath.Join(filepath.Dir(repo), "remote.git")
	runGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, bare, "update-server-info")
	requests := 0
	files := http.FileServer(http.Dir(filepath.Dir(repo)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" {
			t.Errorf("unexpected remote write: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	remoteURL := "http://rundock.fixture.invalid/remote.git"
	runGit(t, repo, "remote", "set-url", "origin", remoteURL)
	runGit(t, repo, "config", "http."+remoteURL+".proxy", unusedProxyAddress(t))
	config := filepath.Join(repo, ".git", "config")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read := func(ctx context.Context, args ...string) (string, error) {
		return runCommand(ctx, repo, "git", nil, args...)
	}
	args := []string{"-C", repo, "ls-remote", "origin", "HEAD"}
	prepared, notice, err := prepareGitNetworkWith(ctx, repo, args, false, read, func(string) []string { return []string{server.URL} })
	if err != nil {
		t.Fatal(err)
	}
	if notice == "" || reflect.DeepEqual(prepared, args) {
		t.Fatal("missing verified recovery")
	}
	out, err := runCommand(ctx, repo, "git", nil, prepared...)
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if !strings.Contains(out, "HEAD") || requests < 2 {
		t.Fatalf("out=%s requests=%d", out, requests)
	}
	after, err := os.ReadFile(config)
	if err != nil || string(before) != string(after) {
		t.Fatal("Git settings changed")
	}
}

func TestGitPreflightRespectsRemotePoliciesAndPushDestination(t *testing.T) {
	for _, tc := range []struct {
		name, url, policy           string
		configured, multiple, force bool
	}{
		{name: "explicit direct", url: "https://fixture.invalid/repo.git", configured: true, force: true},
		{name: "SSH", url: "git@fixture.invalid:repo.git", force: true},
		{name: "local file", url: "file:///fixture/repo.git", force: true},
		{name: "multiple push URLs", url: "https://one.invalid/repo.git\nhttps://two.invalid/repo.git", multiple: true, force: true},
		{name: "external proxy", url: "https://fixture.invalid/repo.git", policy: "http://corporate.invalid:8080", configured: true, force: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedNetworkEnvironment(t)
			args := []string{"-C", "repo", "push", "origin", "main"}
			probes := 0
			read := func(_ context.Context, argv ...string) (string, error) {
				joined := strings.Join(argv, " ")
				if strings.Contains(joined, "remote get-url") {
					if !strings.Contains(joined, "--push") {
						t.Fatal("fetch URL used for push check")
					}
					return tc.url, nil
				}
				if strings.Contains(joined, "config --get remote.origin.proxy") && tc.configured {
					return tc.policy, nil
				}
				if strings.Contains(joined, "config") {
					return "", errors.New("unset")
				}
				if strings.Contains(joined, "ls-remote") {
					probes++
					if !strings.Contains(joined, tc.url) {
						t.Fatal("wrong destination")
					}
					if tc.name == "explicit direct" && !strings.Contains(joined, "http."+tc.url+".proxy=") {
						t.Fatal("explicit direct policy lost")
					}
					return "head", nil
				}
				t.Fatalf("unexpected command %v", argv)
				return "", nil
			}
			got, notice, err := prepareGitNetworkWith(context.Background(), "repo", args, tc.force, read, func(string) []string { return []string{"http://127.0.0.1:1"} })
			wantProbes := 0
			if tc.configured {
				wantProbes = 1
			}
			if err != nil || notice != "" || !reflect.DeepEqual(got, args) || probes != wantProbes {
				t.Fatalf("%v %s %v probes=%d", got, notice, err, probes)
			}
		})
	}
}

func TestRetryFetchFlagsUseFreshProxyAndOnlyProbeReads(t *testing.T) {
	isolatedNetworkEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	old := unusedProxyAddress(t)
	calls := 0
	read := func(_ context.Context, argv ...string) (string, error) {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "remote get-url") {
			return "https://fixture.invalid/repo.git", nil
		}
		if strings.Contains(joined, "config --get-urlmatch") {
			return old, nil
		}
		if strings.Contains(joined, "config") {
			return "", errors.New("unset")
		}
		if strings.Contains(joined, "ls-remote") {
			calls++
			if !strings.Contains(joined, server.URL) {
				t.Fatal("missing fresh proxy")
			}
			return "head", nil
		}
		t.Fatalf("unexpected mutating command: %v", argv)
		return "", nil
	}
	args := []string{"-C", "repo", "fetch", "--refmap=", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "origin", "refs/heads/main"}
	got, _, err := prepareGitNetworkWith(context.Background(), "repo", args, false, read, func(string) []string { return []string{server.URL} })
	if err != nil || calls != 1 || !reflect.DeepEqual(got[len(got)-len(args):], args) {
		t.Fatalf("%v probes=%d args=%v", err, calls, got)
	}
}
