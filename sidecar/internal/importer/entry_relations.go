package importer

import (
	"crypto/sha256"
	"fmt"
	"github.com/launcher-sidecar/internal/accesslink"
	"github.com/launcher-sidecar/internal/probe"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Include the directory: identical wrappers can start different projects when
// their relative paths or %~dp0 differ. This is a hint, never automatic merging.
func EntryFingerprint(path string) string {
	if !supportedScript(path) {
		return ""
	}
	body := boundedEntryText(path)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return ""
	}
	body = strings.ReplaceAll(strings.TrimPrefix(body, "\ufeff"), "\r\n", "\n")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(dir)+"\n"+strings.ToLower(filepath.Ext(path))+"\n"+body)))
}

func boundedEntryText(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return ""
	}
	return readTextBestEffort(path)
}

var batchCD = regexp.MustCompile(`(?im)^\s*@?cd\s+(?:/d\s+)?"?([^"\r\n]+)"?\s*$`)
var batchNode = regexp.MustCompile(`(?im)^\s*@?(?:call\s+)?node(?:\.exe)?\s+(?:"([^"]+)"|([^\s"&|]+))`)
var localURL = regexp.MustCompile("https?://(?:localhost|127\\.0\\.0\\.1|\\[::1\\])(?::[0-9]+)?(?:/[^\\s\"'`<>)]*)?")

// Only resolve an explicit local Node file from simple CMD wrappers. Complex
// shell expressions remain user-configured; no script is evaluated.
func EntryLaunchTarget(script string) string {
	text := boundedEntryText(script)
	match := batchNode.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	target := match[1]
	if target == "" {
		target = match[2]
	}
	if !strings.HasSuffix(strings.ToLower(target), ".mjs") && !strings.HasSuffix(strings.ToLower(target), ".js") && !strings.HasSuffix(strings.ToLower(target), ".cjs") {
		return ""
	}
	dir := filepath.Dir(script)
	if cd := batchCD.FindStringSubmatch(text); cd != nil {
		cwd := strings.ReplaceAll(strings.TrimSpace(cd[1]), "%~dp0", dir+string(filepath.Separator))
		if strings.ContainsAny(cwd, "%!&|") {
			return ""
		}
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(dir, cwd)
		}
		dir = cwd
	}
	target = strings.ReplaceAll(target, "%~dp0", filepath.Dir(script)+string(filepath.Separator))
	if strings.ContainsAny(target, "%!&|") {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return ""
	}
	return resolved
}

func suggestedAccessLinks(script string) []accesslink.Link {
	links := []accesslink.Link{}
	collect := func(text string) {
		for _, raw := range localURL.FindAllString(text, -1) {
			if !probe.LocalWebURL(raw) {
				continue
			}
			// API and health URLs are not browser destinations.
			if strings.Contains(raw, "/api/") || strings.Contains(raw, "/__") {
				continue
			}
			name := "首页"
			if strings.Contains(raw, "/admin") {
				name = "管理后台"
			} else if strings.TrimRight(raw, "/") != raw && len(links) > 0 {
				name = "预览"
			}
			links = append(links, accesslink.Link{Name: name, URL: raw})
		}
	}
	target := EntryLaunchTarget(script)
	if target != "" {
		collect(boundedEntryText(target))
	}
	collect(boundedEntryText(script))
	// A sibling is related only when it invokes the exact same local Node file.
	if target != "" {
		siblings, _ := os.ReadDir(filepath.Dir(script))
		for _, sibling := range siblings {
			p := filepath.Join(filepath.Dir(script), sibling.Name())
			if !sibling.IsDir() && supportedScript(p) && strings.EqualFold(EntryLaunchTarget(p), target) {
				collect(boundedEntryText(p))
			}
		}
	}
	normalized, _ := accesslink.Normalize(links)
	return normalized
}
