package publisher

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	encodingpem "encoding/pem"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestTestLiteralReportedRepositoryFixtures(t *testing.T) {
	for _, path := range []string{"../diagnostics/service_test.go", "finding_context_test.go", "safe_release_test.go", "sensitive_expression_test.go", "skip_checks_test.go", "sensitive_test_literals.go", "test_literal_detection_test.go"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range scanSensitiveFile(path, content) {
			if finding.Kind != "credential-literal-review" {
				t.Errorf("%s:%d: fixture still blocked as %s", path, finding.Line, finding.Kind)
			}
		}
	}
}

func TestTestLiteralScopedEvidence(t *testing.T) {
	fakeToken := "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"
	fakeKey := "-----BEGIN " + "PRIVATE KEY-----\nexample\n-----END PRIVATE KEY-----"
	source := func(v string) string {
		return "package fixture\nfunc TestFixture(){ input := " + strconv.Quote(v) + "; _ = input }\n"
	}
	for _, value := range []string{fakeToken, fakeKey} {
		if got := scanSensitiveFile("fixture_test.go", []byte(source(value))); len(got) != 0 {
			t.Fatal("known test literal blocked", got)
		}
		for _, name := range []string{"config.go", "tests/fixture.txt", ".env.example"} {
			if got := scanSensitiveFile(name, []byte(source(value))); len(got) == 0 {
				t.Fatal("path-only exemption", name)
			}
		}
		if got := scanSensitiveFile("fixture_test.go", []byte("package fixture\n// "+value)); len(got) == 0 {
			t.Fatal("comment exempted")
		}
		if got := scanSensitiveFile("fixture_test.go", []byte(value)); len(got) == 0 {
			t.Fatal("unparsed source exempted")
		}
	}
	// A sequence prefix is not enough to exempt a token with additional material.
	for _, value := range []string{fakeToken + "RandomSuffix", "ghp_" + strings.Repeat("aB3cD4", 6)} {
		if got := scanSensitiveFile("fixture_test.go", []byte(source(value))); len(got) == 0 {
			t.Fatal("unknown token exempted")
		}
	}
	if strings.Contains(redactSensitiveLog(fakeToken), fakeToken) {
		t.Fatal("log redaction was weakened")
	}
}

func TestTestLiteralRealPrivateKeyAndMixedFile(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	realKey := string(encodingpem.EncodeToMemory(&encodingpem.Block{Type: "PRIVATE KEY", Bytes: der}))
	fakeKey := "-----BEGIN " + "PRIVATE KEY-----\nexample\n-----END PRIVATE KEY-----"
	source := "package fixture\nvar input = " + strconv.Quote(fakeKey) + "\nvar second = " + strconv.Quote(realKey)
	got := scanSensitiveFile("fixture_test.go", []byte(source))
	if len(got) != 1 || got[0].Kind != "private-key" || got[0].Line != 3 {
		t.Fatal("real key behind fixture was missed", got)
	}
	// A label is not proof that the following encoded key material is harmless.
	labelled := strings.Replace(realKey, "-----\n", "-----\nTEST-ONLY-NOT-A-REAL-KEY\n", 1)
	if got := scanSensitiveFile("fixture_test.go", []byte("package fixture\nvar input="+strconv.Quote(labelled))); len(got) == 0 {
		t.Fatal("label bypassed encoded key")
	}
	unknown := "package fixture\nvar input=" + strconv.Quote("-----BEGIN "+"PRIVATE KEY-----\n") + " + unknownBody"
	if got := scanSensitiveFile("fixture_test.go", []byte(unknown)); len(got) == 0 {
		t.Fatal("unknown concatenation exempted")
	}
	// Scanning continues past the first private-key header even without source evidence.
	if got := scanSensitiveFile("keys.txt", []byte(fakeKey+"\n"+realKey)); len(got) != 2 {
		t.Fatal("not all headers scanned", got)
	}
}

func TestTestLiteralConstantConcatAndLineEndings(t *testing.T) {
	header := "-----BEGIN " + "RSA PRIVATE KEY-----\n"
	source := "package fixture\r\nfunc TestFixture(){\r\n const marker=\"TEST-ONLY-NOT-A-REAL-KEY\"\r\n input:=unknownPrefix + " + strconv.Quote(header) + " + marker + \"\\n# after\\n\"; _=input\r\n}"
	if got := scanSensitiveFile("fixture_test.go", []byte(source)); len(got) != 0 {
		t.Fatal("constant fixture concat blocked", got)
	}
	source = strings.Replace(source, "const marker=", "var marker=", 1)
	if got := scanSensitiveFile("fixture_test.go", []byte(source)); len(got) == 0 {
		t.Fatal("mutable variable trusted")
	}
}
