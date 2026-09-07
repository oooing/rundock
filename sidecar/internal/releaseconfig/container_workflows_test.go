package releaseconfig

import (
	"context"
	"strings"
	"testing"
)

func TestContainerWorkflowVersionSourceOverridesEarlierUnrelatedPackage(t *testing.T) {
	root := t.TempDir()
	// Alphabetic discovery visits the extension first. It is not the server's
	// version source even when a release happened to give it the same number.
	writeFixture(t, root, "code/extension/package.json", `{"name":"extension","version":"2.0.16"}`)
	writeFixture(t, root, "code/frontend/package.json", `{"name":"web","version":"2.0.16","scripts":{"build":"vite build","tauri":"tauri"},"devDependencies":{"vite":"1","@tauri-apps/cli":"2"}}`)
	writeFixture(t, root, "code/frontend/package-lock.json", `{"name":"web","version":"2.0.16","lockfileVersion":3,"packages":{"":{"name":"web","version":"2.0.16"}}}`)
	writeFixture(t, root, "code/frontend/src-tauri/tauri.conf.json", `{"version":"1.2.29"}`)
	writeFixture(t, root, "code/frontend/src-tauri/Cargo.toml", "[package]\nname = \"desktop\"\nversion = \"1.2.29\"\n")
	writeFixture(t, root, "code/backend/requirements.txt", "fastapi\n")
	writeFixture(t, root, "code/Dockerfile", "FROM scratch\n")
	writeFixture(t, root, ".github/workflows/container-image.yml", `name: Container
on:
  push:
    tags: ["web-server/v*"]
  workflow_dispatch:
jobs:
  build:
    steps:
      - run: |
          web_version="$(node -p "require('./code/frontend/package.json').version")"
      - uses: docker/build-push-action@v6
        with:
          context: code
          file: ./code/Dockerfile
`)
	service, closeStore := fixtureService(t, root)
	defer closeStore()
	cfg, err := service.Scan(context.Background(), "app1")
	if err != nil {
		t.Fatal(err)
	}
	web := targetByID(cfg, "code-frontend-web")
	container := targetByID(cfg, "code-cloud-container")
	extension := targetByID(cfg, "code-extension-node")
	backend := targetByID(cfg, "code-backend-python-server")
	if web == nil || container == nil || extension == nil || backend == nil {
		t.Fatalf("missing targets: %+v", cfg.Targets)
	}
	if web.VersionGroup != container.VersionGroup || backend.VersionGroup != web.VersionGroup || extension.VersionGroup == web.VersionGroup {
		t.Fatalf("incorrect package ownership: %+v", cfg.Targets)
	}
	group := groupByID(cfg, web.VersionGroup)
	if groupByID(cfg, extension.VersionGroup).TagPrefix == "server" {
		t.Fatal("extension retained the container's server namespace")
	}
	if group.TagPrefix != "web-server" || group.CurrentVersion != "2.0.16" {
		t.Fatalf("incorrect workflow version: %+v", group)
	}
	if !groupHasVersionFile(cfg, group.ID, "code/frontend/package.json") || !groupHasVersionFile(cfg, group.ID, "code/frontend/package-lock.json") || groupHasVersionFile(cfg, group.ID, "code/extension/package.json") {
		t.Fatalf("wrong version files: %+v", group)
	}
	for _, target := range []*Target{web, container} {
		if !target.Enabled || target.Runner.Type != RunnerGitPush || target.Steps.Publish != "tag-push" || target.Steps.Build != "" {
			t.Fatalf("wrong workflow action: %+v", target)
		}
	}
	if backend.Runner.Type != RunnerLocal || backend.Steps.Check == "" {
		t.Fatalf("backend checks must stay local: %+v", backend)
	}
	if desktop := targetByID(cfg, "code-frontend-windows"); desktop.VersionGroup == group.ID {
		t.Fatalf("independent desktop version was merged: %+v", desktop)
	}
	if !warningsContain(cfg.Warnings, "code/frontend/package.json") || !warningsContain(cfg.Warnings, "web-server/v*") {
		t.Fatalf("missing workflow evidence: %+v", cfg.Warnings)
	}
}

func TestTagOnlyContainerDoesNotPretendBranchPushWillBuild(t *testing.T) {
	for index, source := range []string{"", "node -p \"require('./missing/package.json').version\"", "node -p \"require('./frontend/package.json').version\"\n          node -p \"require('./extension/package.json').version\""} {
		t.Run([]string{"no version evidence", "unknown package", "multiple packages"}[index], func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "frontend/package.json", `{"name":"web","version":"1.0.0","scripts":{"build":"vite build"},"devDependencies":{"vite":"1"}}`)
			writeFixture(t, root, "extension/package.json", `{"name":"extension","version":"1.0.0"}`)
			writeFixture(t, root, "Dockerfile", "FROM scratch\n")
			writeFixture(t, root, ".github/workflows/container.yml", "on:\n  push:\n    tags: [\"web-server/v*\"]\njobs:\n  build:\n    steps:\n      - run: |\n          "+source+"\n      - uses: docker/build-push-action@v6\n")
			service, closeStore := fixtureService(t, root)
			defer closeStore()
			cfg, err := service.Scan(context.Background(), "app1")
			if err != nil {
				t.Fatal(err)
			}
			container := targetByID(cfg, "cloud-container")
			if container == nil || container.Enabled || container.Steps.Publish == "branch-push" {
				t.Fatalf("unverified cloud action: %+v", container)
			}
			web := targetByID(cfg, "frontend-web")
			if web.Runner.Type != RunnerLocal || web.Steps.Build == "" {
				t.Fatalf("uncertain workflow replaced the local Web build: %+v", web)
			}
			if !warningsContain(cfg.Warnings, "无法唯一确认") {
				t.Fatalf("missing uncertainty warning: %+v", cfg.Warnings)
			}
		})
	}
}

func TestBranchPushEventDistinguishesTagsAndBranches(t *testing.T) {
	tests := []struct {
		document string
		want     bool
	}{
		{"on:\n  push:\n    tags: [\"web-server/v*\"]\n", false},
		{"on:\n  push:\n    tags-ignore: [\"draft*\"]\n", false},
		{"on:\n  push:\n    branches: [main]\n", true},
		{"on:\n  push:\n    branches: [main]\n    tags: [\"v*\"]\n", true},
		{"on:\n  push:\n", true},
		{"on: [push, workflow_dispatch]\n", true},
		{"on: push\n", true},
		{"on:\n  workflow_dispatch:\njobs:\n  build:\n    push: true\n", false},
	}
	for _, tt := range tests {
		if got := hasBranchPushEvent(tt.document); got != tt.want {
			t.Fatalf("branch detection=%v want=%v: %s", got, tt.want, tt.document)
		}
	}
}

func TestWorkflowVersionSourcesRejectCommentsAndExpressions(t *testing.T) {
	sources := workflowNodeVersionSources("# node -p \"require('./wrong/package.json').version\"\nnode -p \"require('./code/frontend/package.json').version\"\nnode -p \"require('../outside/package.json').version\"\nnode -p \"require('${SOURCE}/package.json').version\"")
	if strings.Join(sources, ",") != "code/frontend/package.json" {
		t.Fatalf("untrusted version source inferred: %+v", sources)
	}
}
