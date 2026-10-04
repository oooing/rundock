package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

// Read the actual two projects through the production configuration boundary.
// This never modifies project source or a GitHub repository.
func TestProjectDeliveryE2E(t *testing.T) {
	if os.Getenv("RUNDOCK_PROJECT_E2E") != "1" {
		t.Skip("requires two local project checkouts")
	}
	codeRoot, _ := filepath.Abs("../../..")
	roots := map[string]string{"rundock": codeRoot, "inglocalplay": os.Getenv("RUNDOCK_INGLOCAL_ROOT")}
	st, err := store.Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evidence := []map[string]any{}
	for id, root := range roots {
		if root == "" {
			t.Fatal("RUNDOCK_INGLOCAL_ROOT is required")
		}
		app := &store.App{ID: id, Name: id, Cwd: root, EntryScript: filepath.Join(root, "package.json"), AdapterType: "batch", Args: []string{}, Env: map[string]string{}, Tags: []string{}, PortHints: []int{}, LastStatus: "stopped"}
		if err = st.CreateApp(app); err != nil {
			t.Fatal(err)
		}
		cfg, err := releaseconfig.New(st).Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		workflows, err := delivery.Workflows(root)
		if err != nil {
			t.Fatal(err)
		}
		cloud, local := 0, 0
		for _, target := range cfg.Targets {
			if target.Delivery != nil {
				local++
				if len(target.Verification) == 0 || len(target.ArtifactRules) == 0 {
					t.Fatal("unverified delivery target")
				}
			}
			if target.Runner.Type == "git-push" {
				cloud++
				if target.Steps.Publish == "tag-push" {
					t.Fatal("old tag trigger still configured")
				}
			}
		}
		if cloud == 0 || local == 0 || cfg.Automation.Trigger != "dispatch" {
			t.Fatal("both build modes must remain available")
		}
		item := map[string]any{"project": id, "localDeliveryTargets": local, "cloudTargets": cloud, "workflowHashes": workflows, "configValidated": true}
		if os.Getenv("RUNDOCK_VERIFY_GITHUB") == "1" {
			account, identityErr := (delivery.CLI{}).Identity(context.Background())
			if identityErr != nil {
				code, message := delivery.ErrorInfo(identityErr)
				item["credentialStatus"] = code
				item["credentialMessage"] = message
			} else {
				item["credentialStatus"] = "verified"
				item["account"] = account
			}
		}
		evidence = append(evidence, item)
	}
	raw, _ := json.MarshalIndent(map[string]any{"passed": true, "boundary": "actual project config and workflow validation; optional read-only OS-keyring GitHub identity", "projects": evidence}, "", "  ")
	if dir := os.Getenv("RUNDOCK_DELIVERY_EVIDENCE"); dir != "" {
		if err = os.WriteFile(filepath.Join(dir, "projects-e2e.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
