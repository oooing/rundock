package publisher

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

type connectionFixture struct {
	t          *testing.T
	identities int
	account    string
}

func (f *connectionFixture) Identity(context.Context) (string, error) {
	f.identities++
	return f.account, nil
}
func (f *connectionFixture) JSON(_ context.Context, method, path string, _ any, out any) error {
	if method != "GET" {
		f.t.Fatal("preflight wrote to GitHub")
	}
	if path == "repos/example/app" || path == "repos/example/other" {
		raw, _ := json.Marshal(map[string]any{"full_name": path[len("repos/"):], "permissions": map[string]bool{"push": true}, "default_branch": "main"})
		return json.Unmarshal(raw, out)
	}
	return &delivery.HTTPError{Status: 404}
}
func (f *connectionFixture) Upload(context.Context, string, int64, string, string) error {
	f.t.Fatal("upload")
	return nil
}
func (f *connectionFixture) Digest(context.Context, string) (string, error) {
	f.t.Fatal("digest")
	return "", nil
}

func TestDeliveryConnectionCheckedOncePerRepositoryAndAccount(t *testing.T) {
	client := &connectionFixture{t: t, account: "example"}
	svc := &Service{delivery: &delivery.Engine{Client: client}}
	plan := &executionPlan{}
	for _, group := range []string{"desktop", "web", "android"} {
		plan.Targets = append(plan.Targets, planTarget{VersionGroup: group, Delivery: &releaseconfig.Delivery{Repository: "example/app", Account: "example"}, Selection: store.ReleaseTargetSelection{Publish: true}})
	}
	run := &store.ReleaseRun{RepoRoot: t.TempDir()}
	if err := svc.preflightDeliveryBuild(context.Background(), run, plan); err != nil {
		t.Fatal(err)
	}
	if client.identities != 1 {
		t.Fatalf("same repository checked %d times", client.identities)
	}
	plan.Targets[2].Delivery = &releaseconfig.Delivery{Repository: "example/other", Account: "example"}
	client.identities = 0
	if err := svc.preflightDeliveryBuild(context.Background(), run, plan); err != nil {
		t.Fatal(err)
	}
	if client.identities != 2 {
		t.Fatalf("different repositories checked %d times", client.identities)
	}
	plan.Targets[2].Delivery = &releaseconfig.Delivery{Repository: "example/app", Account: "different"}
	if err := svc.preflightDeliveryBuild(context.Background(), run, plan); err == nil {
		t.Fatal("different account was not verified")
	}
}
