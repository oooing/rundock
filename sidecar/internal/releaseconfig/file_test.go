package releaseconfig

import (
	"strings"
	"testing"
)

func TestAnnotatedReleaseExample(t *testing.T) {
	cfg, err := decode([]byte(ExampleFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(cfg); err != nil {
		t.Fatal(err)
	}
	// A # inside a JSON string is data, not a YAML comment.
	raw := strings.Replace(ExampleFile, `"check": ""`, `"check": "echo #hello"`, 1)
	cfg, err = decode([]byte(raw))
	if err != nil || cfg.Targets[1].Steps.Check != "echo #hello" {
		t.Fatalf("string changed: %v", err)
	}
	if _, err := decode([]byte(ExampleFile + "\n{}")); err == nil {
		t.Fatal("accepted multiple documents")
	}
	if _, err := decode([]byte(strings.Replace(ExampleFile, `"schemaVersion": 1,`, `"schemaVersion": 1, "unknownKey": true,`, 1))); err == nil {
		t.Fatal("accepted unknown key")
	}
}
