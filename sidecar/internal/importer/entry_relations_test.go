package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChineseStartupAndDuplicateEntries(t *testing.T) {
	root := t.TempDir()
	wrapper := "@echo off\r\ncd /d \"%~dp0code\"\r\ncall node scripts/start-preview.mjs --open\r\n"
	main := fixtureFile(t, root, "启动博客.cmd", wrapper)
	duplicate := fixtureFile(t, root, "启动博客Demo.cmd", wrapper)
	fixtureFile(t, root, "管理博客.cmd", "@echo off\ncd /d \"%~dp0code\"\ncall node scripts/start-preview.mjs\nstart \"\" \"http://127.0.0.1:17653/admin\"\n")
	fixtureFile(t, root, "code/scripts/start-preview.mjs", "const url = 'http://127.0.0.1:17653/'; const health = 'http://127.0.0.1:8788/api/health';")
	fixtureFile(t, root, "code/package.json", `{"scripts":{"dev":"node scripts/dev.mjs"}}`)
	for _, name := range []string{"发布博客.cmd", "卸载博客.cmd", "停止博客.cmd", "backups/start.cmd"} {
		fixtureFile(t, root, name, "echo excluded")
	}
	got, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Options[0].RelativePath != "启动博客.cmd" || !got.Options[0].Recommended {
		t.Fatalf("wrong recommendation: %+v", got.Options)
	}
	if len(got.Options) != 4 {
		t.Fatalf("unexpected candidates: %+v", got.Options)
	}
	found := false
	for _, option := range got.Options {
		if option.RelativePath == "启动博客Demo.cmd" {
			found = option.DuplicateOf == "启动博客.cmd"
		}
	}
	if !found {
		t.Fatal("duplicate wrapper not marked")
	}
	if EntryFingerprint(main) != EntryFingerprint(duplicate) {
		t.Fatal("duplicate content missed")
	}
	elsewhere := fixtureFile(t, root, "other/启动博客.cmd", wrapper)
	if EntryFingerprint(main) == EntryFingerprint(elsewhere) {
		t.Fatal("merged different relative working directories")
	}
	candidate, err := Import(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.AccessLinks) != 2 || candidate.AccessLinks[0].URL != "http://127.0.0.1:17653/" || candidate.AccessLinks[1].URL != "http://127.0.0.1:17653/admin" {
		t.Fatalf("links: %+v", candidate.AccessLinks)
	}
	if candidate.EntryScript != main || candidate.AdapterType != "batch" {
		t.Fatal("changed selected startup wrapper")
	}
}

func TestRelatedEntryRequiresSameReferencedFile(t *testing.T) {
	root := t.TempDir()
	a := fixtureFile(t, root, "启动.cmd", "cd /d \"%~dp0code\"\nnode start.mjs\n")
	fixtureFile(t, root, "code/start.mjs", "const url='http://localhost:3000/';")
	fixtureFile(t, root, "管理.cmd", "cd /d \"%~dp0other\"\nnode start.mjs\nstart \"\" \"http://localhost:4000/admin\"\n")
	fixtureFile(t, root, "other/start.mjs", "const url='http://localhost:4000/';")
	got := suggestedAccessLinks(a)
	if len(got) != 1 || got[0].URL != "http://localhost:3000/" {
		t.Fatalf("unrelated links included: %+v", got)
	}
}

func TestBlogStartupReadOnly(t *testing.T) {
	root := os.Getenv("RUNDOCK_BLOG_READONLY")
	if root == "" {
		t.Skip("set RUNDOCK_BLOG_READONLY for the real blog fixture")
	}
	got, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated || len(got.Options) == 0 || got.Options[0].RelativePath != "启动博客.cmd" {
		t.Fatalf("discovery: %+v", got)
	}
	duplicate := false
	for _, opt := range got.Options {
		t.Logf("%s recommended=%v duplicateOf=%s", opt.RelativePath, opt.Recommended, opt.DuplicateOf)
		if strings.Contains(opt.RelativePath, "Demo.cmd") {
			duplicate = opt.DuplicateOf == "启动博客.cmd"
		}
	}
	if !duplicate {
		t.Fatal("real duplicate not identified")
	}
	cand, err := Import(filepath.Join(root, "启动博客.cmd"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cand.AccessLinks) != 2 {
		t.Fatalf("real links: %+v", cand.AccessLinks)
	}
	t.Logf("links: %+v", cand.AccessLinks)
}
