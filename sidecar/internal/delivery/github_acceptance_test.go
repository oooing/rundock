package delivery

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in, GET-only acceptance of the same pre-build path used by publishing.
func TestLiveGitHubDeliveryPreflight(t *testing.T) {
	root, repo, account := os.Getenv("RUNDOCK_READONLY_ROOT"), os.Getenv("RUNDOCK_READONLY_REPO"), os.Getenv("RUNDOCK_READONLY_ACCOUNT")
	if root == "" || repo == "" || account == "" {
		t.Skip("requires explicit read-only acceptance target")
	}
	workflows, err := Workflows(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	engine := Engine{Client: readOnlyAcceptance{CLI{}, t}}
	if err = engine.Preflight(ctx, Batch{Repository: repo, Account: account, Workflows: workflows}); err != nil {
		t.Fatal(err)
	}
	t.Logf("GitHub account, repository permissions and %d workflows verified in %v; no writes", len(workflows), time.Since(start))
}

type readOnlyAcceptance struct {
	CLI
	t *testing.T
}

func (p readOnlyAcceptance) JSON(ctx context.Context, method, path string, body, out any) error {
	if method != "GET" {
		p.t.Fatalf("unexpected write: %s", method)
	}
	start := time.Now()
	err := p.CLI.JSON(ctx, method, path, body, out)
	p.t.Logf("GET %s: %v (%v)", path, err, time.Since(start))
	return err
}
func (p readOnlyAcceptance) Upload(context.Context, string, int64, string, string) error {
	p.t.Fatal("unexpected upload")
	return nil
}
