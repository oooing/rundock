package publisher

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launcher-sidecar/internal/releaseconfig"
)

func TestSensitiveSourceExpressionsAreNotCredentialLiterals(t *testing.T) {
	cases := map[string]string{
		"config.py":   "secret_key = self.SECRET_KEY.strip()\nadmin_password = self.ADMIN_PASSWORD.strip()\nlease_token = Column(String(36))\nuser.hashed_password = get_password_hash(new_password)\npassword=settings.ADMIN_PASSWORD,\n",
		"client.ts":   "const token = useAuthStore.getState().accessToken;\nrefreshToken: parsed.refresh_token,\naccess_token: z.string().min(1),\ntoken_type: z.literal('bearer'),\n",
		"test.py":     "self.assertFalse(policy_snapshot({'groq_api_key': 'secret'})['enabled'])\nreturn {'token': create_access_token(1)}\n",
		"messages.py": "'API_KEY=your_configuration_value': 'missing',\n",
	}
	for name, body := range cases {
		if got := scanSensitiveFile(name, []byte(body)); len(got) != 0 {
			t.Errorf("%s: expression was flagged: %+v", name, got)
		}
	}
}

func TestSensitiveQuotedLiteralsStillBlockedAndRedacted(t *testing.T) {
	secret := "A7mQ9vB2nL5pR8sT6xY3zW1c"
	for name, body := range map[string]string{
		"test.py":      "API_TOKEN = '" + secret + "',\n",
		"config.ts":    "const API_TOKEN = \"" + secret + "\";\n",
		".env.example": "API_TOKEN=" + secret + " # example\n",
		"config.yml":   "api_key: " + secret + "\n",
		"notes.txt":    "API_TOKEN=" + secret + "\n",
	} {
		got := scanSensitiveFile(name, []byte(body))
		if len(got) == 0 || got[0].Kind != "secret-assignment" {
			t.Errorf("%s: real-looking literal missed", name)
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret leaked")
		}
	}
	if got := scanSensitiveFile("config.py", []byte("API_KEY = 'replace-me',\n")); len(got) != 0 {
		t.Fatalf("literal terminator caused placeholder false positive: %+v", got)
	}
}

func TestSensitiveConfidencePreservesWarningsAndBlocking(t *testing.T) {
	for _, value := range []string{"a-long-random-production-secret-key-123456789", "测试消息包含token12345"} {
		got := scanSensitiveFile("tests/test_config.py", []byte("SECRET = '"+value+"'\n"))
		if len(got) != 1 || got[0].Kind != "credential-literal-review" {
			t.Fatal("readable test data should be a warning")
		}
	}
	got := scanSensitiveFile("config.py", []byte("SECRET = 'a-long-random-production-secret-key-123456789'\n"))
	if len(got) != 1 || got[0].Kind != "secret-assignment" {
		t.Fatal("test-only passphrase rule applied to production config")
	}
	for _, value := range []string{"access-token-1", "short!", "'test-password-for-fixture'"} {
		if strings.Contains(redactSensitiveLog("API_TOKEN="+value), value) {
			t.Fatal("low confidence must not weaken log redaction")
		}
	}
	for _, value := range []string{"replace-with-a-long-random-secret-before-use", "test-password-for-fixture", "development-secret-for-local-use-only", "access-token-1"} {
		got := scanSensitiveFile("settings.py", []byte("TOKEN = '"+value+"'\n"))
		if len(got) != 1 || got[0].Kind != "credential-literal-review" {
			t.Fatalf("descriptive literal should remain a review warning: %+v", got)
		}
	}
	for _, value := range []string{"A7mQ9vB2nL5p!R8sT6xY3zW1c", "test-A7mQ9vB2nL5pR8sT6xY3zW1c", "ghp_" + strings.Repeat("aB3cD4", 6)} {
		got := scanSensitiveFile("tests/fixtures/example.py", []byte("TOKEN = '"+value+"'\n"))
		blocked := false
		for _, f := range got {
			if f.Kind != "credential-literal-review" {
				blocked = true
			}
		}
		if !blocked {
			t.Fatal("random or provider token exempted by test context")
		}
	}
	if got := scanSensitiveFile("tests/example.txt", []byte("-----BEGIN PRIVATE KEY-----\nexample\n-----END PRIVATE KEY-----")); len(got) == 0 || got[0].Kind != "private-key" {
		t.Fatal("private key marker must remain blocking")
	}
}

func TestCandidateCredentialWarningsAllowCodeSubmission(t *testing.T) {
	svc, repo, cleanup := newReleaseFixture(t)
	defer cleanup()
	writeTestFile(t, filepath.Join(repo, "sample.py"), "TOKEN = 'test-password-for-fixture'\n")
	pf, err := svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"sample.py"}, Intent: IntentSaveProgress})
	if err != nil {
		t.Fatal(err)
	}
	if !view.CanSaveProgress || view.Status != "ready" || len(view.SensitiveFindings) != 0 || len(view.Warnings) == 0 {
		t.Fatalf("warning incorrectly blocks submission: %+v", view)
	}
	if !strings.Contains(strings.Join(view.Warnings, "\n"), "sample.py:1") {
		t.Fatal("manual review location missing")
	}
	writeTestFile(t, filepath.Join(repo, "sample.py"), "TOKEN = 'A7mQ9vB2nL5pR8sT6xY3zW1c'\n")
	pf, err = svc.PreflightLocal(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	view, err = svc.PrepareCandidate(context.Background(), "app1", CandidateRequest{StatusFingerprint: pf.StatusFingerprint, SelectedPaths: []string{"sample.py"}, Intent: IntentSaveProgress})
	if err != nil {
		t.Fatal(err)
	}
	if view.CanSaveProgress || view.Status != "blocked" || len(view.SensitiveFindings) == 0 {
		t.Fatal("random credential must still block submission")
	}
}

func TestUntrackedReportsStayLocalWithManualOverride(t *testing.T) {
	root := t.TempDir()
	name := "reports/releases/preflight-20260915/snapshot/src/main.ts"
	writeTestFile(t, filepath.Join(root, filepath.FromSlash(name)), "export const value=1\n")
	change := FileChange{Path: name, Status: "??"}
	item := classifyOne(root, change, nil)
	if item.Category != CategoryLocal || item.SelectedDefault {
		t.Fatalf("snapshot recommended: %+v", item)
	}
	manual := applyManualDecision(item, ManualDecision{Path: name, ContentFingerprint: item.ContentFingerprint, Decision: DecisionInclude})
	if manual.Category != CategoryRecommend || !manual.SelectedDefault {
		t.Fatal("manual inclusion lost")
	}
	explicit := classifyOne(root, change, []releaseconfig.FileRule{{ID: "share", Pattern: "reports/", Kind: releaseconfig.RuleRecommend}})
	if !explicit.SelectedDefault {
		t.Fatal("explicit project rule must prevail")
	}
	change.Tracked = true
	change.Status = " M"
	if tracked := classifyOne(root, change, nil); !tracked.SelectedDefault {
		t.Fatal("tracked report changes must remain included")
	}
}
