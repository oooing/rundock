package releaseconfig

import (
	"context"
	"testing"
)

func TestScanUsesTagTriggeredActionsForMatchingPlatforms(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "frontend/package.json", `{
  "name":"frontend","version":"2.0.4",
  "scripts":{"build":"vite build","tauri":"tauri"},
  "devDependencies":{"vite":"1","@tauri-apps/cli":"2"}
}`)
	writeFixture(t, root, "frontend/src-tauri/tauri.conf.json", `{"version":"1.2.33"}`)
	writeFixture(t, root, "frontend/src-tauri/Cargo.toml", "[package]\nname = \"frontend\"\nversion = \"1.2.33\"\n")
	writeFixture(t, root, "mobile/package.json", `{"name":"mobile","version":"1.1.0","dependencies":{"expo":"1"}}`)
	writeFixture(t, root, "mobile/android/gradlew", "")
	writeFixture(t, root, "Dockerfile", "FROM scratch\n")
	writeFixture(t, root, "backend/requirements.txt", "fastapi\n")
	writeFixture(t, root, ".github/workflows/container-image.yml", `name: Container
on:
  push:
    tags:
      - "web-server/v*"
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: docker/build-push-action@v6
`)
	writeFixture(t, root, ".github/workflows/desktop-client.yml", `name: Clients
on:
  push:
    tags: ["desktop/v*", "android/v*"]
jobs:
  windows:
    runs-on: windows-latest
    steps:
      - run: npm run tauri build
      - uses: actions/upload-artifact@v7
  android:
    runs-on: ubuntu-latest
    steps:
      - run: ./gradlew assembleRelease
      - uses: actions/upload-artifact@v7
`)

	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"frontend-web", "frontend-windows", "mobile-android-android", "cloud-container"} {
		target := targetByID(cfg, id)
		if target == nil {
			t.Fatalf("missing target %s in %+v", id, cfg.Targets)
		}
		if target.Runner.Type != RunnerGitPush || target.Steps.Publish != "tag-push" {
			t.Fatalf("target %s must be owned by the tag workflow: %+v", id, target)
		}
		if target.Steps.Check != "" || target.Steps.Build != "" || target.Steps.Package != "" || target.Steps.Deploy != "" {
			t.Fatalf("cloud target %s retained a local command: %+v", id, target.Steps)
		}
	}
	checkOnly := targetByID(cfg, "backend-python-server")
	if checkOnly == nil || checkOnly.Runner.Type != RunnerLocal || checkOnly.Steps.Check == "" {
		t.Fatalf("check-only source validation must remain local: %+v", checkOnly)
	}
	mac := targetByID(cfg, "frontend-macos")
	if mac == nil || mac.Runner.Type != RunnerLocal {
		t.Fatalf("a Windows-only workflow must not claim macOS: %+v", mac)
	}
	if !warningsContain(cfg.Warnings, "container-image.yml 由版本 Tag 触发") || !warningsContain(cfg.Warnings, "desktop-client.yml 由版本 Tag 触发") {
		t.Fatalf("cloud ownership should be explained: %+v", cfg.Warnings)
	}
}

func TestTagWorkflowDetectionIsConservative(t *testing.T) {
	tests := []struct {
		name     string
		workflow string
	}{
		{
			name: "matching tag without a platform build",
			workflow: `on:
  push:
    tags: ["desktop/v*"]
jobs:
  test:
    runs-on: windows-latest
    steps:
      - run: npm test
`,
		},
		{
			name: "platform build with another tag namespace",
			workflow: `on:
  push:
    tags: ["nightly/v*"]
jobs:
  build:
    runs-on: windows-latest
    steps:
      - run: npm run tauri build
`,
		},
		{
			name: "tag-looking text outside the event block",
			workflow: `on:
  workflow_dispatch:
jobs:
  build:
    runs-on: windows-latest
    env:
      tags: desktop/v*
    steps:
      - run: npm run tauri build
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "package.json", `{"name":"desktop","version":"1.0.0","scripts":{"tauri":"tauri"},"devDependencies":{"@tauri-apps/cli":"2"}}`)
			writeFixture(t, root, "src-tauri/tauri.conf.json", `{"version":"2.0.0"}`)
			writeFixture(t, root, "src-tauri/Cargo.toml", "[package]\nname = \"desktop\"\nversion = \"2.0.0\"\n")
			writeFixture(t, root, ".github/workflows/release.yml", test.workflow)
			service, closeStore := fixtureService(t, root)
			defer closeStore()
			cfg, err := service.Scan(context.Background(), "app1")
			if err != nil {
				t.Fatal(err)
			}
			windows := targetByID(cfg, "windows")
			if windows == nil || windows.Runner.Type != RunnerLocal || windows.Steps.Build == "" {
				t.Fatalf("uncertain workflow must leave local target unchanged: %+v", windows)
			}
		})
	}
}

func TestPushTagPatternParserHonorsInlineAndNegativeFilters(t *testing.T) {
	patterns := parsePushTagPatterns(`name: release
on:
  push:
    tags: ['desktop/v*', '!desktop/v0.*'] # ordered filters
jobs:
  build:
    tags: ["not-an-event/v*"]
`)
	workflow := tagWorkflow{tagPatterns: patterns}
	if workflow.matchesPrefix("desktop") {
		t.Fatalf("negative filter should exclude the generated probe tag: %#v", patterns)
	}
	if len(patterns) != 2 {
		t.Fatalf("unexpected parsed filters: %#v", patterns)
	}
}
