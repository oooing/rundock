package publisher

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/launcher-sidecar/internal/releaseconfig"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func samePath(a, b string) bool {
	return gitPathKey(canonicalRepositoryPath(a)) == gitPathKey(canonicalRepositoryPath(b))
}

// Windows Git and Go may spell the same directory using long or 8.3 names.
// Resolve aliases before repository comparisons and lock lookup.
func canonicalRepositoryPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func secureProjectPath(root, relativePath string, requireDir bool) (string, error) {
	if strings.TrimSpace(relativePath) == "" {
		relativePath = "."
	}
	if filepath.IsAbs(relativePath) || filepath.VolumeName(relativePath) != "" {
		return "", errors.New("必须使用项目内的相对路径")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(rootAbs, filepath.FromSlash(relativePath))
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("路径不能跳出项目目录")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if requireDir && !info.IsDir() {
		return "", errors.New("路径不是目录")
	}
	// Treat the selected repository root as the trust boundary, but reject any
	// symlink or Windows reparse point below it. This avoids following a
	// repository-owned link outside the project and works even where resolving
	// ancestors (for example a sandboxed system Temp directory) is forbidden.
	current := rootAbs
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			linkInfo, linkErr := os.Lstat(current)
			if linkErr != nil {
				return "", linkErr
			}
			if isPathLink(linkInfo) {
				return "", errors.New("项目内路径不能经过符号链接或目录联接")
			}
		}
	}
	return filepath.Clean(path), nil
}

var (
	jsonPackageVersionRE = regexp.MustCompile(`(?m)("version"\s*:\s*")([0-9]+\.[0-9]+\.[0-9]+)(")`)
	jsonNestedVersionRE  = regexp.MustCompile(`(?ms)("package"\s*:\s*\{.*?"version"\s*:\s*")([0-9]+\.[0-9]+\.[0-9]+)(")`)
	gradleVersionRE      = regexp.MustCompile(`(?m)(versionName\s*(?:=\s*)?["'])([^"']+)(["'])`)
)

func configuredVersionPattern(file releaseconfig.VersionFile) (*regexp.Regexp, error) {
	switch strings.ToLower(strings.TrimSpace(file.Format)) {
	case "json":
		switch file.JSONPointer {
		case "", "/version":
			return jsonPackageVersionRE, nil
		case "/package/version":
			return jsonNestedVersionRE, nil
		default:
			return nil, fmt.Errorf("unsupported JSON version pointer %s", file.JSONPointer)
		}
	case "cargo", "toml":
		return cargoPackageRE, nil
	case "npm-lock", "cargo-lock":
		return nil, nil
	case "gradle":
		return gradleVersionRE, nil
	default:
		return nil, fmt.Errorf("unsupported version file format %s", file.Format)
	}
}

func readConfiguredVersion(repo string, file releaseconfig.VersionFile) (string, error) {
	path, err := secureProjectPath(repo, file.Path, false)
	if err != nil {
		return "", fmt.Errorf("read version file %s: %w", file.Path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	format := strings.ToLower(strings.TrimSpace(file.Format))
	if format == "npm-lock" {
		value := readNpmLockVersion(path)
		if value == "" {
			return "", fmt.Errorf("version field not found or inconsistent in %s", file.Path)
		}
		return value, nil
	}
	if format == "cargo-lock" {
		manifest, manifestErr := cargoManifestForLock(repo, file.Path, nil)
		if manifestErr != nil {
			return "", fmt.Errorf("version file %s: %w", file.Path, manifestErr)
		}
		target, targetErr := findCargoLockRootPackage(raw, manifest)
		if targetErr != nil {
			return "", fmt.Errorf("version file %s: %w", file.Path, targetErr)
		}
		return target.version, nil
	}
	pattern, err := configuredVersionPattern(file)
	if err != nil {
		return "", err
	}
	match := pattern.FindSubmatch(raw)
	if len(match) < 3 {
		return "", fmt.Errorf("version field not found in %s", file.Path)
	}
	return string(match[2]), nil
}

func updateConfiguredVersionFiles(repo string, files []releaseconfig.VersionFile, version string) (map[string][]byte, error) {
	originals := map[string][]byte{}
	type pendingWrite struct {
		path  string
		after []byte
	}
	pending := make([]pendingWrite, 0, len(files))

	// Read every configured source before changing anything. Cargo.lock must be
	// matched against the pre-update Cargo.toml version even when Cargo.toml is
	// listed first in the group.
	for _, file := range files {
		path, err := secureProjectPath(repo, file.Path, false)
		if err != nil {
			return originals, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			return originals, err
		}
		originals[path] = append([]byte(nil), before...)
	}
	for _, file := range files {
		path, err := secureProjectPath(repo, file.Path, false)
		if err != nil {
			return originals, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		before := originals[path]
		after, err := configuredVersionBytes(repo, file, before, originals, version)
		if err != nil {
			return originals, err
		}
		pending = append(pending, pendingWrite{path: path, after: after})
	}
	for _, write := range pending {
		path := write.path
		if bytes.Equal(originals[path], write.after) {
			continue
		}
		info, _ := os.Stat(path)
		mode := fs.FileMode(0o644)
		if info != nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(path, write.after, mode); err != nil {
			return originals, err
		}
	}
	return originals, nil
}

func expectedConfiguredVersionWrites(repo string, files []releaseconfig.VersionFile, originals map[string][]byte, version string) map[string][]byte {
	expected := map[string][]byte{}
	for _, file := range files {
		path, err := secureProjectPath(repo, file.Path, false)
		if err != nil {
			continue
		}
		before, ok := originals[path]
		if !ok {
			continue
		}
		if after, err := configuredVersionBytes(repo, file, before, originals, version); err == nil {
			expected[path] = after
		}
	}
	return expected
}

func configuredVersionBytes(repo string, file releaseconfig.VersionFile, before []byte, originals map[string][]byte, version string) ([]byte, error) {
	format := strings.ToLower(strings.TrimSpace(file.Format))
	switch format {
	case "npm-lock":
		after, ok := replaceVersionBytesForPath(file.Path, before, version)
		if !ok {
			return nil, fmt.Errorf("version field not found in %s", file.Path)
		}
		return after, nil
	case "cargo-lock":
		manifest, err := cargoManifestForLock(repo, file.Path, originals)
		if err != nil {
			return nil, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		after, err := replaceCargoLockRootVersion(before, manifest, version)
		if err != nil {
			return nil, fmt.Errorf("version file %s: %w", file.Path, err)
		}
		return after, nil
	default:
		pattern, err := configuredVersionPattern(file)
		if err != nil {
			return nil, err
		}
		if !pattern.Match(before) {
			return nil, fmt.Errorf("version field not found in %s", file.Path)
		}
		after, _ := replaceFirstVersion(before, pattern, version)
		return after, nil
	}
}

type worktreeEntry struct {
	Status  string
	Tracked bool
	Hash    string
}

type worktreeSnapshot map[string]worktreeEntry
