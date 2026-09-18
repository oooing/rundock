package publisher

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFilePreviewContentAndLimits(t *testing.T) {
	root := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("中文 #%.md", []byte("<script>alert(1)</script>\n你好"))
	p, err := readFilePreview(root, "中文 #%.md")
	if err != nil || p.Kind != "text" || !strings.Contains(p.Text, "<script>") {
		t.Fatalf("text: %+v %v", p, err)
	}
	write("big.txt", []byte(strings.Repeat("中", previewTextLimit)))
	p, err = readFilePreview(root, "big.txt")
	if err != nil || !p.Truncated || len(p.Text) > previewTextLimit || !utf8.ValidString(p.Text) {
		t.Fatalf("bounded text: %+v %v", p, err)
	}
	write("empty.txt", nil)
	p, err = readFilePreview(root, "empty.txt")
	if err != nil || p.Kind != "text" || p.Text != "" {
		t.Fatal("empty text", err)
	}
	write("installer.apk", []byte{0, 1, 2, 3})
	p, err = readFilePreview(root, "installer.apk")
	if err != nil || p.Kind != "unsupported" || p.Text != "" {
		t.Fatal("binary", err)
	}
	image, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9mQAAAAASUVORK5CYII=")
	write("image.png", image)
	p, err = readFilePreview(root, "image.png")
	if err != nil || p.Kind != "image" || !strings.HasPrefix(p.DataURL, "data:image/png;base64,") {
		t.Fatal("image", err)
	}
	write("big.png", bytes.Repeat([]byte{1}, previewImageLimit+1))
	p, err = readFilePreview(root, "big.png")
	if err != nil || p.Kind != "unsupported" || p.DataURL != "" {
		t.Fatal("big image", err)
	}
	write("icon.svg", []byte(`<svg onload="alert(1)"></svg>`))
	p, err = readFilePreview(root, "icon.svg")
	if err != nil || p.Kind != "text" {
		t.Fatal("SVG must stay inert", err)
	}
}

func TestFilePreviewRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"", "../outside.txt", "..\\outside.txt", "/absolute", "C:\\secret.txt", "file.txt:stream", ".git/config", ".GiT/config", "."} {
		if _, err := readFilePreview(root, path); err == nil {
			t.Errorf("allowed %q", path)
		}
	}
	if _, err := readFilePreview(root, "deleted.txt"); err == nil || err.(*Error).Code != "preview_not_found" {
		t.Fatal("missing file", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("private"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Log("symlink unavailable", err)
	} else if _, err = readFilePreview(root, "link.txt"); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestFilePreviewUsesRepositoryRootWithoutChangingGit(t *testing.T) {
	s, repo, close := newReleaseFixture(t)
	defer close()
	before := runGit(t, repo, "status", "--porcelain")
	p, err := s.PreviewFile(context.Background(), "app1", "package.json")
	if err != nil || p.Kind != "text" {
		t.Fatal("preview", err)
	}
	if after := runGit(t, repo, "status", "--porcelain"); before != after {
		t.Fatal("preview changed Git state")
	}
	if _, err := s.PreviewFile(context.Background(), "missing", "package.json"); err == nil {
		t.Fatal("unknown app accepted")
	}
}
