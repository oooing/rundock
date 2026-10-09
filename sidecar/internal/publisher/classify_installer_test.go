package publisher

import (
	"github.com/launcher-sidecar/internal/releaseconfig"
	"testing"
)

func TestInstallerSourcesAndLicensesAreRecommended(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"src-tauri/windows/installer.nsi", "src-tauri/windows/installer-hooks.nsh", "src-tauri/windows/LICENSE-TAURI-MIT.txt", "LICENSE", "licenses/LICENSE_MIT", "LICENSE.md", "COPYING", "LICENCE.txt"} {
		item := classifyOne(repo, FileChange{Path: name, Status: "??"}, nil)
		if item.Category != CategoryRecommend || !item.SelectedDefault {
			t.Fatalf("required installer source not recommended: %+v", item)
		}
	}
	for _, name := range []string{"notes.txt", "license.key", "license-private.pem", "license-secret.env"} {
		item := classifyOne(repo, FileChange{Path: name, Status: "??"}, nil)
		if item.Category != CategoryReview || item.SelectedDefault {
			t.Fatalf("unknown material was broadly included: %+v", item)
		}
	}
	for _, name := range []string{".tmp/installer.nsi", "dist/LICENSE.txt", "reports/LICENSE-MIT.txt"} {
		item := classifyOne(repo, FileChange{Path: name, Status: "??"}, nil)
		if item.Category != CategoryLocal || item.SelectedDefault {
			t.Fatalf("output exclusion lost priority: %+v", item)
		}
	}
	for _, kind := range []string{releaseconfig.RuleSensitive, releaseconfig.RuleLocal, releaseconfig.RuleReview} {
		item := classifyOne(repo, FileChange{Path: "src-tauri/windows/installer.nsi", Status: "??"},
			[]releaseconfig.FileRule{{ID: "explicit", Pattern: "src-tauri/windows/**", Kind: kind}})
		if item.SelectedDefault || item.Category == CategoryRecommend {
			t.Fatalf("heuristic overrode explicit %s rule: %+v", kind, item)
		}
	}
}
