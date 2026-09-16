package publisher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Parent-owned acceptance probes use synthetic data only.
func TestParentSecretTemplateIsNotAnExemption(t *testing.T) {
	secret := "A7mQ9vB2nL5pR8sT6xY3zW1c"
	for _, name := range []string{".env", ".env.example", "sample-config.env"} {
		findings := scanSensitiveFile(name, []byte("API_TOKEN="+secret+"\n"))
		if len(findings) == 0 {
			t.Errorf("real-looking assignment exempted by filename %s", name)
		}
		encoded, _ := json.Marshal(findings)
		if strings.Contains(string(encoded), secret) {
			t.Errorf("secret leaked for %s", name)
		}
	}
	if got := scanSensitiveFile(".env.example", []byte("API_TOKEN=replace-me\n")); len(got) != 0 {
		t.Fatalf("safe placeholder should be allowed: %v", got)
	}
}

func TestParentSecretInCommentsStillScanned(t *testing.T) {
	token := "ghp_" + strings.Repeat("aB3cD4", 6)
	for _, line := range []string{"# " + token, "// " + token, "const value = \"" + token + "\"; // example value"} {
		findings := scanSensitiveFile("config.js", []byte(line))
		if len(findings) == 0 {
			t.Errorf("high-confidence token not detected in %q", line[:8])
		}
		encoded, _ := json.Marshal(findings)
		if strings.Contains(string(encoded), token) {
			t.Fatal("full token leaked into findings")
		}
	}
}

func TestParentDependencyExtensionAlternatives(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"src/main.ts":  "import { greet } from './greet'; export { greet };",
		"src/greet.ts": "export const greet = () => 'hello';",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := findMissingLocalDependencies(root, root, []string{"src/main.ts"}, nil); len(got) != 0 {
		t.Fatalf("existing extensionless module must not report alternative extensions as missing: %v", got)
	}
}

func TestParentDependencyReportsResolvedMissingFile(t *testing.T) {
	candidate, source := t.TempDir(), t.TempDir()
	for _, root := range []string{candidate, source} {
		if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "src/main.cjs"), []byte("module.exports=require('./greet');"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "src/greet.cjs"), []byte("module.exports=()=> 'hello';"), 0600); err != nil {
		t.Fatal(err)
	}
	got := findMissingLocalDependencies(candidate, source, []string{"src/main.cjs"}, []FileClassification{{Path: "src/greet.cjs", Category: CategoryRecommend}})
	if len(got) != 1 || got[0].Suggestion != "src/greet.cjs" {
		t.Fatalf("must identify one actual omitted module rather than nonexistent extension variants: %v", got)
	}
}

func TestParentRuleDirectoryBoundary(t *testing.T) {
	for _, rel := range []string{"reports/file.txt", "reports/nested/file.txt"} {
		if !matchFileRule("reports/**", rel) {
			t.Errorf("expected directory match: %s", rel)
		}
	}
	for _, rel := range []string{"reports-old/file.txt", "reports2/file.txt", "myreports/file.txt"} {
		if matchFileRule("reports/**", rel) {
			t.Errorf("must not exclude neighboring directory: %s", rel)
		}
	}
}
