package publisher

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

type cargoManifestPackage struct {
	Name    string
	Version string
}

type cargoLockPackage struct {
	name         string
	version      string
	hasSource    bool
	versionStart int
	versionEnd   int
}

var (
	cargoManifestSectionRE = regexp.MustCompile(`(?m)^[\t ]*\[package\][\t ]*(?:#[^\r\n]*)?\r?$`)
	cargoNextSectionRE     = regexp.MustCompile(`(?m)^[\t ]*\[`)
	cargoLockPackageRE     = regexp.MustCompile(`(?m)^[\t ]*\[\[package\]\][\t ]*(?:#[^\r\n]*)?\r?$`)
	tomlNameFieldRE        = regexp.MustCompile(`(?m)^[\t ]*name[\t ]*=[\t ]*"([^"\r\n]+)"[\t ]*(?:#[^\r\n]*)?\r?$`)
	tomlVersionFieldRE     = regexp.MustCompile(`(?m)^[\t ]*version[\t ]*=[\t ]*"([^"\r\n]+)"[\t ]*(?:#[^\r\n]*)?\r?$`)
	tomlSourceFieldRE      = regexp.MustCompile(`(?m)^[\t ]*source[\t ]*=`)
)

func readCargoManifestPackage(raw []byte) (cargoManifestPackage, error) {
	header := cargoManifestSectionRE.FindIndex(raw)
	if len(header) != 2 {
		return cargoManifestPackage{}, fmt.Errorf("Cargo.toml 缺少 [package] 段")
	}
	bodyStart := header[1]
	if bodyStart < len(raw) && raw[bodyStart] == '\n' {
		bodyStart++
	}
	bodyEnd := len(raw)
	if next := cargoNextSectionRE.FindIndex(raw[bodyStart:]); len(next) == 2 {
		bodyEnd = bodyStart + next[0]
	}
	body := raw[bodyStart:bodyEnd]
	nameMatch := tomlNameFieldRE.FindSubmatch(body)
	versionMatch := tomlVersionFieldRE.FindSubmatch(body)
	if len(nameMatch) != 2 || len(versionMatch) != 2 {
		return cargoManifestPackage{}, fmt.Errorf("Cargo.toml 的 [package] 必须包含 name 和 version")
	}
	return cargoManifestPackage{Name: string(nameMatch[1]), Version: string(versionMatch[1])}, nil
}

func cargoLockPackages(raw []byte) []cargoLockPackage {
	headers := cargoLockPackageRE.FindAllIndex(raw, -1)
	packages := make([]cargoLockPackage, 0, len(headers))
	for i, header := range headers {
		bodyStart := header[1]
		if bodyStart < len(raw) && raw[bodyStart] == '\n' {
			bodyStart++
		}
		bodyEnd := len(raw)
		if i+1 < len(headers) {
			bodyEnd = headers[i+1][0]
		}
		body := raw[bodyStart:bodyEnd]
		nameMatch := tomlNameFieldRE.FindSubmatch(body)
		versionMatch := tomlVersionFieldRE.FindSubmatchIndex(body)
		if len(nameMatch) != 2 || len(versionMatch) < 4 || versionMatch[2] < 0 || versionMatch[3] < 0 {
			continue
		}
		packages = append(packages, cargoLockPackage{
			name:         string(nameMatch[1]),
			version:      string(body[versionMatch[2]:versionMatch[3]]),
			hasSource:    tomlSourceFieldRE.Match(body),
			versionStart: bodyStart + versionMatch[2],
			versionEnd:   bodyStart + versionMatch[3],
		})
	}
	return packages
}

func findCargoLockRootPackage(raw []byte, manifest cargoManifestPackage) (cargoLockPackage, error) {
	matches := []cargoLockPackage{}
	for _, candidate := range cargoLockPackages(raw) {
		// Workspace/path packages have no source entry. A registry or Git
		// dependency may legally have the same package name, so name alone is
		// not enough to identify the application's own lock entry.
		if candidate.name == manifest.Name && candidate.version == manifest.Version && !candidate.hasSource {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return cargoLockPackage{}, fmt.Errorf("Cargo.lock 中未找到与 Cargo.toml [package] %q v%s 对应的本地 [[package]]", manifest.Name, manifest.Version)
	}
	if len(matches) > 1 {
		return cargoLockPackage{}, fmt.Errorf("Cargo.lock 中存在多个与 Cargo.toml [package] %q v%s 对应的本地 [[package]]", manifest.Name, manifest.Version)
	}
	return matches[0], nil
}

func replaceCargoLockRootVersion(raw []byte, manifest cargoManifestPackage, version string) ([]byte, error) {
	target, err := findCargoLockRootPackage(raw, manifest)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)-target.versionEnd+target.versionStart+len(version))
	out = append(out, raw[:target.versionStart]...)
	out = append(out, version...)
	out = append(out, raw[target.versionEnd:]...)
	return out, nil
}

func cargoManifestForLock(repo, lockRelativePath string, originals map[string][]byte) (cargoManifestPackage, error) {
	lockRelativePath = filepath.Clean(filepath.FromSlash(lockRelativePath))
	manifestRelativePath := filepath.Join(filepath.Dir(lockRelativePath), "Cargo.toml")
	manifestPath, err := secureProjectPath(repo, filepath.ToSlash(manifestRelativePath), false)
	if err != nil {
		return cargoManifestPackage{}, fmt.Errorf("无法读取同目录 Cargo.toml：%w", err)
	}
	raw, ok := originals[manifestPath]
	if !ok {
		raw, err = os.ReadFile(manifestPath)
		if err != nil {
			return cargoManifestPackage{}, fmt.Errorf("无法读取同目录 Cargo.toml：%w", err)
		}
	}
	manifest, err := readCargoManifestPackage(bytes.Clone(raw))
	if err != nil {
		return cargoManifestPackage{}, err
	}
	return manifest, nil
}
