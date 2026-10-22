// Package projecticon discovers local project artwork without executing scripts,
// fetching websites, or recursively walking dependency/build directories.
package projecticon

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxBytes = 1 << 20

type Icon struct {
	Data []byte
	MIME string
	ETag string
}

type entry struct {
	icon    Icon
	expires time.Time
}

// Resolver has a bounded memory cache, including misses. Discovery is separate
// from app listing and startup; a missing or slow icon never delays those APIs.
type Resolver struct {
	mu      sync.Mutex
	entries map[string]entry
}

func Key(cwd, script string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(cwd+"\x00"+script)))[:16]
}

func (r *Resolver) Resolve(cwd, script string) Icon {
	key := Key(cwd, script)
	r.mu.Lock()
	cached, ok := r.entries[key]
	r.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.icon
	}
	icon := Discover(cwd, script)
	ttl := time.Minute
	if len(icon.Data) == 0 {
		ttl = 15 * time.Second
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]entry)
	}
	if len(r.entries) >= 32 {
		oldestKey := ""
		var oldest time.Time
		for k, e := range r.entries {
			if oldestKey == "" || e.expires.Before(oldest) {
				oldestKey, oldest = k, e.expires
			}
		}
		delete(r.entries, oldestKey)
	}
	r.entries[key] = entry{icon, time.Now().Add(ttl)}
	return icon
}

func localAbsolute(path string) bool {
	// Never implicitly connect to network shares or Windows device paths.
	return filepath.IsAbs(path) && !strings.HasPrefix(path, `\\`) && !strings.HasPrefix(path, "//")
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Reads only regular files whose resolved path stays in the registered project.
func readLocal(root, path string, limit int64) []byte {
	if !within(root, path) {
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || !localAbsolute(real) || !within(root, real) {
		return nil
	}
	f, err := os.Open(real)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil
	}
	return data
}

// Fixed locations, not a tree scan: monorepos can keep their UI one or two
// levels below the launch script. Do not search demos, backups or build outputs.
var projectDirs = []string{".", "code", "frontend", "web", "client", "desktop", "apps/web", "apps/desktop", "apps/extension", "code/frontend", "code/web", "code/client", "extension"}
var commonIcons = []string{
	"src-tauri/icons/128x128.png", "src-tauri/icons/icon.ico", "src-tauri/icons/icon.png",
	"app/icon.svg", "app/icon.png", "src/app/icon.svg", "src/app/icon.png",
	"public/favicon.svg", "public/favicon.ico", "public/favicon.png",
	"favicon.svg", "favicon.ico", "favicon.png", "app/favicon.ico", "src/app/favicon.ico",
	"public/icons/icon-128.png", "public/icon/128.png", "public/icon.png", "public/logo.svg", "public/logo.png",
	"public/brand/logo.svg", "public/assets/logo.svg", "public/assets/logo256.png",
	"assets/icon.ico", "assets/icon.png", "assets/logo.svg", "assets/logo.png",
	"build/icon.ico", "resources/icon.ico", "icon.ico", "icon.png", "logo.svg", "logo.png",
}

func Discover(cwd, script string) Icon {
	if cwd == "" {
		cwd = filepath.Dir(script)
	}
	if !localAbsolute(cwd) {
		return Icon{}
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil || !localAbsolute(root) {
		return Icon{}
	}
	var candidates []string
	// An explicitly adjacent startup icon wins over framework defaults.
	if localAbsolute(script) && within(cwd, script) {
		rel, _ := filepath.Rel(cwd, script)
		stem := strings.TrimSuffix(rel, filepath.Ext(rel))
		for _, ext := range []string{".ico", ".png", ".svg"} {
			candidates = append(candidates, filepath.Join(root, stem+ext))
		}
	}
	for _, dir := range projectDirs {
		base := filepath.Join(root, filepath.FromSlash(dir))
		candidates = append(candidates, configuredIcons(root, base)...)
		for _, name := range commonIcons {
			candidates = append(candidates, filepath.Join(base, filepath.FromSlash(name)))
		}
		for _, path := range candidates {
			if icon := loadIcon(root, path); len(icon.Data) > 0 {
				return icon
			}
		}
		candidates = nil
	}
	return Icon{}
}

func configuredIcons(root, base string) []string {
	var result []string
	// Tauri and Electron configuration is data only; never evaluate JS/TS config.
	var tauri struct {
		Bundle struct {
			Icon []string `json:"icon"`
		} `json:"bundle"`
	}
	tauriDir := filepath.Join(base, "src-tauri")
	if json.Unmarshal(readLocal(root, filepath.Join(tauriDir, "tauri.conf.json"), 128<<10), &tauri) == nil {
		// Prefer a crisp, reasonably sized PNG over 16/32px or multi-image ICOs.
		icons := append([]string{}, tauri.Bundle.Icon...)
		sort.SliceStable(icons, func(i, j int) bool { return iconRank(icons[i]) < iconRank(icons[j]) })
		for _, name := range icons {
			if path := relativeIcon(tauriDir, name); path != "" {
				result = append(result, path)
			}
		}
	}
	var pkg struct {
		Build struct {
			Icon string `json:"icon"`
			Win  struct {
				Icon string `json:"icon"`
			} `json:"win"`
		} `json:"build"`
	}
	if json.Unmarshal(readLocal(root, filepath.Join(base, "package.json"), 128<<10), &pkg) == nil {
		for _, name := range []string{pkg.Build.Win.Icon, pkg.Build.Icon} {
			if path := relativeIcon(base, name); path != "" {
				result = append(result, path)
			}
		}
	}
	// Browser extension manifests use a size -> path map.
	for _, file := range []string{"manifest.json", "public/manifest.json"} {
		var manifest struct {
			Icons map[string]string `json:"icons"`
		}
		if json.Unmarshal(readLocal(root, filepath.Join(base, file), 128<<10), &manifest) != nil {
			continue
		}
		for _, size := range []string{"128", "96", "64", "48", "32"} {
			if path := relativeIcon(filepath.Dir(filepath.Join(base, file)), manifest.Icons[size]); path != "" {
				result = append(result, path)
			}
		}
	}
	return result
}

func relativeIcon(base, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, ":?#") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || filepath.IsAbs(name) {
		return ""
	}
	return filepath.Join(base, filepath.FromSlash(name))
}

func iconRank(name string) int {
	if strings.HasSuffix(strings.ToLower(name), ".png") && strings.Contains(name, "128") {
		return 0
	}
	if strings.HasSuffix(strings.ToLower(name), ".ico") {
		return 1
	}
	return 2
}

func loadIcon(root, path string) Icon {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".ico" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".svg" {
		return Icon{}
	}
	data := readLocal(root, path, maxBytes)
	if len(data) == 0 {
		return Icon{}
	}
	mime := ""
	switch ext {
	case ".svg":
		data = safeSVG(data)
		if len(data) == 0 {
			return Icon{}
		}
		mime = "image/svg+xml"
	case ".ico":
		if !validICO(data) {
			return Icon{}
		}
		mime = "image/x-icon"
	default:
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 2048 || cfg.Height > 2048 {
			return Icon{}
		}
		if format != "png" && format != "jpeg" {
			return Icon{}
		}
		mime = "image/" + format
	}
	return Icon{data, mime, fmt.Sprintf(`"%x"`, sha256.Sum256(data))}
}

func validICO(data []byte) bool {
	if len(data) < 22 || !bytes.Equal(data[:4], []byte{0, 0, 1, 0}) {
		return false
	}
	n := int(binary.LittleEndian.Uint16(data[4:6]))
	if n < 1 || n > 256 || len(data) < 6+n*16 {
		return false
	}
	for i := 0; i < n; i++ {
		p := data[6+i*16 : 6+(i+1)*16]
		size, offset := uint64(binary.LittleEndian.Uint32(p[8:12])), uint64(binary.LittleEndian.Uint32(p[12:16]))
		if size == 0 || offset < uint64(6+n*16) || offset+size > uint64(len(data)) {
			return false
		}
	}
	return true
}
