package projecticon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pngData(t *testing.T, size int) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	im.Set(0, 0, color.NRGBA{R: 42, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func icoData(t *testing.T) []byte {
	png := pngData(t, 64)
	data := make([]byte, 22)
	data[2], data[4], data[6], data[7] = 1, 1, 64, 64
	binary.LittleEndian.PutUint32(data[14:], uint32(len(png)))
	binary.LittleEndian.PutUint32(data[18:], 22)
	return append(data, png...)
}

func TestDiscoveryPriorityAndSources(t *testing.T) {
	for _, tc := range []struct {
		name, path, mime string
		data             []byte
	}{
		{"startup ico", "scripts/start.ico", "image/x-icon", icoData(t)},
		{"tauri", "src-tauri/icons/128x128.png", "image/png", pngData(t, 128)},
		{"monorepo", "frontend/public/favicon.png", "image/png", pngData(t, 64)},
		{"next", "src/app/icon.svg", "image/svg+xml", []byte(`<svg viewBox="0 0 24 24"><path d="M0 0h24v24z"/></svg>`)},
		{"wxt", "apps/extension/public/icons/icon-128.png", "image/png", pngData(t, 128)},
		{"root script with code", "code/public/favicon.ico", "image/x-icon", icoData(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			put(t, root, tc.path, tc.data)
			got := Discover(root, filepath.Join(root, "scripts/start.bat"))
			if got.MIME != tc.mime || len(got.Data) == 0 || got.ETag == "" {
				t.Fatalf("icon not found: %+v", got)
			}
		})
	}
	root := t.TempDir()
	put(t, root, "start.ico", icoData(t))
	put(t, root, "src-tauri/icons/128x128.png", pngData(t, 128))
	if got := Discover(root, filepath.Join(root, "start.bat")); got.MIME != "image/x-icon" {
		t.Fatal("startup icon must take precedence")
	}
}

func TestConfiguredIconsAndInvalidFallback(t *testing.T) {
	root := t.TempDir()
	put(t, root, "src-tauri/tauri.conf.json", []byte(`{"bundle":{"icon":["icons/32x32.png","icons/128x128.png","icons/icon.ico"]}}`))
	put(t, root, "src-tauri/icons/32x32.png", pngData(t, 32))
	want := pngData(t, 128)
	put(t, root, "src-tauri/icons/128x128.png", want)
	if got := Discover(root, ""); !bytes.Equal(want, got.Data) {
		t.Fatal("must prefer high resolution configured icon")
	}

	root = t.TempDir()
	put(t, root, "package.json", []byte(`{"build":{"win":{"icon":"brand/custom.png"}}}`))
	put(t, root, "brand/custom.png", want)
	if got := Discover(root, ""); !bytes.Equal(want, got.Data) {
		t.Fatal("electron config icon missing")
	}

	root = t.TempDir()
	put(t, root, "public/favicon.ico", []byte("not an image"))
	put(t, root, "public/logo.png", want)
	if got := Discover(root, ""); !bytes.Equal(want, got.Data) {
		t.Fatal("invalid image should fall through to next candidate")
	}
	put(t, root, "manifest.json", []byte(`{"icons":{"128":"custom/icon.png"}}`))
	put(t, root, "custom/icon.png", pngData(t, 64))
	if got := Discover(root, ""); bytes.Equal(want, got.Data) {
		t.Fatal("manifest icon should take priority")
	}
}

func TestDiscoveryBoundaries(t *testing.T) {
	root := t.TempDir()
	put(t, root, "demos/public/favicon.png", pngData(t, 32))
	put(t, root, "node_modules/package/public/favicon.png", pngData(t, 32))
	put(t, root, "backup/public/favicon.png", pngData(t, 32))
	put(t, root, "public/favicon.ico", []byte("<html>not an icon</html>"))
	put(t, root, "public/favicon.png", pngData(t, 2049))
	if got := Discover(root, ""); len(got.Data) != 0 {
		t.Fatal("must not use unrelated or oversized artwork")
	}
	put(t, root, "logo.svg", bytes.Repeat([]byte("x"), maxBytes+1))
	if got := Discover(root, ""); len(got.Data) != 0 {
		t.Fatal("oversized file accepted")
	}
	parent := t.TempDir()
	put(t, parent, "outside.png", pngData(t, 32))
	child := filepath.Join(parent, "project")
	put(t, child, "package.json", []byte(`{"build":{"icon":"../outside.png"}}`))
	if got := Discover(child, ""); len(got.Data) != 0 {
		t.Fatal("path escaped project")
	}
	for _, name := range []string{"https://example.com/icon.png", "file:///icon.png", `C:\outside.png`, "//server/icon.png", `\\server\icon.png`} {
		if relativeIcon(root, name) != "" {
			t.Fatalf("nonlocal config reference accepted: %s", name)
		}
	}
	for _, root := range []string{".", "", `\\server\project`, `\\?\C:\project`, "//server/project"} {
		if got := Discover(root, ""); len(got.Data) != 0 {
			t.Fatal("nonlocal root accepted")
		}
	}
}

func TestSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	put(t, outside, "favicon.png", pngData(t, 32))
	if err := os.Symlink(outside, filepath.Join(root, "public")); err != nil {
		t.Skipf("symlink privilege unavailable: %v", err)
	}
	if got := Discover(root, ""); len(got.Data) != 0 {
		t.Fatal("symlink escaped project")
	}
}

func TestCacheExpiryAndBounds(t *testing.T) {
	root := t.TempDir()
	r := &Resolver{}
	if len(r.Resolve(root, "").Data) != 0 {
		t.Fatal("expected miss")
	}
	put(t, root, "icon.png", pngData(t, 32))
	if len(r.Resolve(root, "").Data) != 0 {
		t.Fatal("miss should be cached")
	}
	r.entries[Key(root, "")] = entry{expires: time.Now().Add(-time.Second)}
	got := r.Resolve(root, "")
	if len(got.Data) == 0 {
		t.Fatal("expired miss must discover newly added icon")
	}
	put(t, root, "icon.png", pngData(t, 64))
	if r.Resolve(root, "").ETag != got.ETag {
		t.Fatal("positive hit should be cached")
	}
	r.entries[Key(root, "")] = entry{expires: time.Now().Add(-time.Second)}
	if r.Resolve(root, "").ETag == got.ETag {
		t.Fatal("updated artwork must invalidate hash after expiry")
	}
	for i := 1; i <= 40; i++ {
		r.Resolve(root, strings.Repeat("s", i))
	}
	if len(r.entries) > 32 {
		t.Fatal("unbounded cache")
	}
}

func TestStaticSVGOnly(t *testing.T) {
	good := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40"><defs><linearGradient id="paint"><stop stop-color="#abc"/></linearGradient></defs><path fill="url(#paint)" d="M0 0h24v24z"/></svg>`)
	if data := safeSVG(good); len(data) == 0 || !bytes.Contains(data, []byte("url(#paint)")) {
		t.Fatal("static gradients should work")
	}
	for _, data := range []string{
		`<svg><script>alert(1)</script></svg>`, `<svg><foreignObject/></svg>`, `<svg><image href="https://example.com"/></svg>`,
		`<!DOCTYPE svg [<!ENTITY secret SYSTEM "file:///secret">]><svg>&secret;</svg>`,
		`<svg><path fill="url(https://example.com)"/></svg>`, `<svg><style>path{fill:red}</style></svg>`,
		`<svg><path fill="u\72l(https://example.com)"/></svg>`, `<svg/><svg/>`, `<html/>`,
	} {
		if len(safeSVG([]byte(data))) > 0 {
			t.Fatalf("unsafe SVG accepted: %s", data)
		}
	}
	data := safeSVG([]byte(`<svg onload="alert(1)" style="background:url(https://example.com)"><path d="M0 0"/></svg>`))
	if bytes.Contains(data, []byte("onload")) || bytes.Contains(data, []byte("style=")) {
		t.Fatal("active attributes survived")
	}
}
