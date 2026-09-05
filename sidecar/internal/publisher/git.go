package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

type commandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

type inputCommandRunner interface {
	RunWithInput(ctx context.Context, dir, name string, input []byte, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	return execRunner{}.RunWithInput(ctx, dir, name, nil, args...)
}

func (execRunner) RunWithInput(ctx context.Context, dir, name string, input []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}

func (s *Service) git(ctx context.Context, repo string, args ...string) (string, error) {
	out, err := s.gitRaw(ctx, repo, args...)
	return strings.TrimSpace(out), err
}

func (s *Service) gitRaw(ctx context.Context, repo string, args ...string) (string, error) {
	all := append([]string{"-C", repo}, args...)
	out, err := s.runner.Run(ctx, repo, "git", all...)
	return strings.TrimRight(out, "\r\n"), err
}

func (s *Service) gitRawWithInput(ctx context.Context, repo string, input []byte, args ...string) (string, error) {
	all := append([]string{"-C", repo}, args...)
	runner, ok := s.runner.(inputCommandRunner)
	if !ok {
		// Test/instrumentation runners commonly wrap execRunner while exposing
		// only Run. Ignore checks still need Git's NUL-safe stdin protocol, so
		// use the production executor for this read-only query.
		runner = execRunner{}
	}
	out, err := runner.RunWithInput(ctx, repo, "git", input, all...)
	return strings.TrimRight(out, "\r\n"), err
}

func gitPathKey(path string) string {
	if path == "" {
		return ""
	}
	key := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

const diagnosticsPath = ".launcher/diagnostics"

// isDiagnosticsPath reports whether path is the project-local diagnostics
// directory or one of its descendants. Git always reports repository paths
// with slash separators; gitPathKey additionally preserves Windows' normal
// case-insensitive path semantics.
func isDiagnosticsPath(path string) bool {
	key := gitPathKey(path)
	root := gitPathKey(diagnosticsPath)
	return key == root || strings.HasPrefix(key, root+"/")
}

func isUntrackedDiagnosticsChange(change FileChange) bool {
	return !change.Tracked && isDiagnosticsPath(change.Path)
}

// untrackedDiagnosticsPaths returns diagnostics paths that are not already in
// Git's index. It is used as a final guard for automatically-added version
// files, which do not come through the user's preflight file selection.
func (s *Service) untrackedDiagnosticsPaths(ctx context.Context, repo string, paths []string) ([]string, error) {
	wanted := map[string]string{}
	args := []string{"ls-files", "--cached", "-z", "--"}
	for _, path := range dedupe(paths) {
		path = filepath.ToSlash(path)
		if !isDiagnosticsPath(path) {
			continue
		}
		key := gitPathKey(path)
		wanted[key] = path
		args = append(args, path)
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	trackedRaw, err := s.gitRaw(ctx, repo, args...)
	if err != nil {
		return nil, err
	}
	for _, path := range strings.Split(trackedRaw, "\x00") {
		delete(wanted, gitPathKey(path))
	}
	untracked := make([]string, 0, len(wanted))
	for _, path := range wanted {
		untracked = append(untracked, path)
	}
	sort.Strings(untracked)
	return untracked, nil
}

// filterUntrackedDiagnosticsStatus removes only untracked diagnostics records
// from porcelain output. Tracked files below the same directory are kept so a
// repository that deliberately versions one of them never loses a change.
//
// Keeping the filter at the raw-status boundary is important: the raw bytes
// participate in statusFingerprint in addition to the parsed file list.
func filterUntrackedDiagnosticsStatus(raw string) string {
	if strings.Contains(raw, "\x00") {
		records := strings.Split(raw, "\x00")
		filtered := make([]string, 0, len(records))
		changed := false
		for i := 0; i < len(records); i++ {
			record := records[i]
			if len(record) >= 4 {
				xy, path := record[:2], record[3:]
				if xy == "??" && isDiagnosticsPath(path) {
					changed = true
					continue
				}
				filtered = append(filtered, record)
				if strings.ContainsAny(xy, "RC") && i+1 < len(records) {
					i++
					filtered = append(filtered, records[i])
				}
				continue
			}
			filtered = append(filtered, record)
		}
		if changed {
			return strings.Join(filtered, "\x00")
		}
		return raw
	}

	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	filtered := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if len(line) >= 3 && line[:2] == "??" {
			path := strings.Trim(strings.TrimSpace(line[3:]), `"`)
			if isDiagnosticsPath(path) {
				changed = true
				continue
			}
		}
		filtered = append(filtered, line)
	}
	if changed {
		return strings.Join(filtered, "\n")
	}
	return raw
}

// ignoredUntrackedPaths returns only paths that are both absent from Git's
// index and matched by an ignore rule. A tracked file remains releasable even
// if a later ignore rule also happens to match it.
func (s *Service) ignoredUntrackedPaths(ctx context.Context, repo string, paths []string) []string {
	wanted := map[string]string{}
	args := []string{"ls-files", "--cached", "-z", "--"}
	for _, path := range dedupe(paths) {
		path = filepath.ToSlash(path)
		if key := gitPathKey(path); key != "" && key != "." {
			wanted[key] = path
			args = append(args, path)
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	trackedRaw, err := s.gitRaw(ctx, repo, args...)
	if err != nil {
		return nil
	}
	for _, path := range strings.Split(trackedRaw, "\x00") {
		delete(wanted, gitPathKey(path))
	}
	if len(wanted) == 0 {
		return nil
	}
	untracked := make([]string, 0, len(wanted))
	for _, path := range wanted {
		untracked = append(untracked, path)
	}
	sort.Strings(untracked)
	var input bytes.Buffer
	for _, path := range untracked {
		input.WriteString(path)
		input.WriteByte(0)
	}
	ignoredRaw, _ := s.gitRawWithInput(ctx, repo, input.Bytes(), "check-ignore", "-z", "--stdin")
	ignored := []string{}
	for _, path := range strings.Split(ignoredRaw, "\x00") {
		if original, ok := wanted[gitPathKey(path)]; ok {
			ignored = append(ignored, original)
		}
	}
	return dedupe(ignored)
}

// unstageExactPaths removes only index entries covered by the release's own
// pathspecs. Querying the changed index first excludes an ignored path that
// caused git add to fail and would otherwise make the cleanup fail as well.
func (s *Service) unstageExactPaths(ctx context.Context, repo string, paths []string) {
	wanted := map[string]string{}
	args := []string{"diff", "--cached", "--name-only", "-z", "--"}
	for _, path := range dedupe(paths) {
		path = filepath.ToSlash(path)
		if key := gitPathKey(path); key != "" && key != "." {
			wanted[key] = path
			args = append(args, path)
		}
	}
	if len(wanted) == 0 {
		return
	}
	stagedRaw, err := s.gitRaw(ctx, repo, args...)
	if err != nil {
		return
	}
	resetPaths := []string{}
	seen := map[string]bool{}
	for _, path := range strings.Split(stagedRaw, "\x00") {
		key := gitPathKey(path)
		if original, ok := wanted[key]; ok && !seen[key] {
			seen[key] = true
			resetPaths = append(resetPaths, original)
		}
	}
	if len(resetPaths) == 0 {
		return
	}
	_, _ = s.git(ctx, repo, append([]string{"reset", "HEAD", "--"}, resetPaths...)...)
}

func parseChanges(raw string) []FileChange {
	raw = filterUntrackedDiagnosticsStatus(raw)
	out := []FileChange{}
	if strings.Contains(raw, "\x00") {
		records := strings.Split(raw, "\x00")
		for i := 0; i < len(records); i++ {
			record := records[i]
			if len(record) < 4 {
				continue
			}
			xy, path := record[:2], record[3:]
			appendChange(&out, xy, path)
			if strings.ContainsAny(xy, "RC") && i+1 < len(records) {
				i++ // porcelain -z 的 rename/copy 下一项是原路径。
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		return out
	}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if len(line) < 3 {
			continue
		}
		xy, path := line[:2], strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		appendChange(&out, xy, strings.Trim(path, `"`))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func appendChange(out *[]FileChange, xy, path string) {
	if len(xy) != 2 || path == "" {
		return
	}
	tracked := xy != "??"
	staged := tracked && xy[0] != ' '
	*out = append(*out, FileChange{Path: filepath.ToSlash(path), Status: xy, Tracked: tracked, Staged: staged})
}

func parseCommittedChanges(raw string) []CommittedFileChange {
	records := strings.Split(raw, "\x00")
	out := []CommittedFileChange{}
	for i := 0; i+1 < len(records); {
		status := strings.TrimSpace(records[i])
		i++
		if status == "" {
			continue
		}
		path := records[i]
		i++
		if (strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")) && i < len(records) {
			path = records[i]
			i++
		}
		if path != "" {
			out = append(out, CommittedFileChange{Path: filepath.ToSlash(path), Status: status})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func statusFingerprint(repo, head, raw string, changes []FileChange) string {
	raw = filterUntrackedDiagnosticsStatus(raw)
	filteredChanges := make([]FileChange, 0, len(changes))
	for _, change := range changes {
		if !isUntrackedDiagnosticsChange(change) {
			filteredChanges = append(filteredChanges, change)
		}
	}
	parts := []string{head, raw}
	hashes := fileHashes(repo, filteredChanges, map[string]bool{})
	keys := make([]string, 0, len(hashes))
	for key := range hashes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key, hashes[key])
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

var semverRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

type semver [3]int

func parseSemver(v string) (semver, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	m := semverRE.FindStringSubmatch(v)
	if m == nil {
		return semver{}, false
	}
	var out semver
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

func compareSemver(a, b semver) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// latestTagForPrefix returns the greatest strict SemVer tag in one version
// scope. Repository tags use vX.Y.Z; independent version groups use
// <prefix>/vX.Y.Z. Tags from other scopes are ignored.
func latestTagForPrefix(tags []string, prefix string) (string, string) {
	want := "v"
	if prefix = strings.TrimSpace(prefix); prefix != "" {
		want = prefix + "/v"
	}
	best := semver{}
	bestTag, bestVersion := "", ""
	found := false
	for _, raw := range tags {
		tag := strings.TrimSpace(strings.TrimSuffix(raw, "^{}"))
		if !strings.HasPrefix(tag, want) {
			continue
		}
		versionText := strings.TrimPrefix(tag, want)
		version, ok := parseSemver(versionText)
		if !ok || strings.HasPrefix(strings.TrimSpace(versionText), "v") {
			continue
		}
		if !found || compareSemver(version, best) > 0 {
			best, bestTag, bestVersion, found = version, tag, versionText, true
		}
	}
	return bestTag, bestVersion
}

func remoteTagNames(raw string) []string {
	seen := map[string]bool{}
	tags := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "refs/tags/") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(fields[1], "refs/tags/"), "^{}")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		tags = append(tags, name)
	}
	sort.Strings(tags)
	return tags
}

func nextPatch(values ...string) string {
	best := semver{}
	found := false
	for _, raw := range values {
		if v, ok := parseSemver(raw); ok && (!found || compareSemver(v, best) > 0) {
			best, found = v, true
		}
	}
	if !found {
		return "0.1.0"
	}
	best[2]++
	return fmt.Sprintf("%d.%d.%d", best[0], best[1], best[2])
}

func validateNewVersion(version string, references []string) error {
	target, ok := parseSemver(version)
	if !ok || strings.HasPrefix(strings.TrimSpace(version), "v") {
		return &Error{Code: "invalid_version", Message: "版本号必须是 X.Y.Z，例如 2.0.0"}
	}
	for _, raw := range references {
		if current, valid := parseSemver(raw); valid && compareSemver(target, current) <= 0 {
			return &Error{Code: "version_not_newer", Message: "目标版本必须高于当前版本和对应的最新 Tag"}
		}
	}
	return nil
}

// suggestReleaseVersion preserves a version that has already been aligned to
// 2.0.0 but has never been tagged. This is the one-time v2 migration path; all
// later releases retain the strict "greater than files and latest tag" rule.
func suggestReleaseVersion(currentVersions []string, latestTag string) string {
	if currentV2CanBeReleased("2.0.0", currentVersions, latestTag) {
		return "2.0.0"
	}
	values := append([]string{}, currentVersions...)
	values = append(values, latestTag)
	return nextPatch(values...)
}

func validateReleaseVersion(version string, currentVersions []string, latestTag string) error {
	if currentV2CanBeReleased(version, currentVersions, latestTag) {
		return nil
	}
	values := append([]string{}, currentVersions...)
	values = append(values, latestTag)
	return validateNewVersion(version, values)
}

func currentV2CanBeReleased(version string, currentVersions []string, latestTag string) bool {
	if strings.TrimSpace(version) != "2.0.0" || len(currentVersions) == 0 {
		return false
	}
	for _, current := range currentVersions {
		if strings.TrimSpace(current) != "2.0.0" {
			return false
		}
	}
	target, _ := parseSemver("2.0.0")
	if latest, ok := parseSemver(latestTag); ok && compareSemver(target, latest) <= 0 {
		return false
	}
	return true
}

func releaseTagNames(versions []store.ReleaseVersion) []string {
	out := make([]string, 0, len(versions))
	for _, version := range versions {
		if version.TagName != "" {
			out = append(out, version.TagName)
		}
	}
	return out
}

func releaseVersionsForRun(run *store.ReleaseRun, plan *executionPlan) []store.ReleaseVersion {
	if plan != nil && len(plan.ReleaseVersions) > 0 {
		return plan.ReleaseVersions
	}
	if run.CreateTag && run.TagName != "" {
		return []store.ReleaseVersion{{TargetVersion: run.TargetVersion, TagName: run.TagName}}
	}
	return []store.ReleaseVersion{}
}

var jsonVersionRE = regexp.MustCompile(`(?m)("version"\s*:\s*")([0-9]+\.[0-9]+\.[0-9]+)(")`)
var cargoPackageRE = regexp.MustCompile(`(?ms)(\[package\].*?^\s*version\s*=\s*")([0-9]+\.[0-9]+\.[0-9]+)(")`)
var npmLockPackageVersionRE = regexp.MustCompile(`(?ms)("packages"\s*:\s*\{\s*""\s*:\s*\{.*?"version"\s*:\s*")([0-9]+\.[0-9]+\.[0-9]+)(")`)

func readVersion(path string, cargo bool) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := jsonVersionRE
	if cargo {
		re = cargoPackageRE
	}
	m := re.FindSubmatch(b)
	if len(m) < 3 {
		return ""
	}
	return string(m[2])
}

func detectVersionStrategy(repo, requested string) (string, []string, map[string]string) {
	files := []string{}
	strategy := requested
	if strategy == "" || strategy == StrategyAuto {
		if fileExists(filepath.Join(repo, "package.json")) &&
			fileExists(filepath.Join(repo, "src-tauri", "tauri.conf.json")) &&
			fileExists(filepath.Join(repo, "src-tauri", "Cargo.toml")) {
			strategy = StrategyTauri
		} else if fileExists(filepath.Join(repo, "package.json")) {
			strategy = StrategyNode
		} else {
			strategy = StrategyManual
		}
	}
	switch strategy {
	case StrategyTauri:
		files = []string{"package.json", "src-tauri/tauri.conf.json", "src-tauri/Cargo.toml"}
		if fileExists(filepath.Join(repo, "src-tauri", "Cargo.lock")) {
			files = append(files, "src-tauri/Cargo.lock")
		}
		if fileExists(filepath.Join(repo, "package-lock.json")) {
			files = append(files, "package-lock.json")
		}
	case StrategyNode:
		files = []string{"package.json"}
		if fileExists(filepath.Join(repo, "package-lock.json")) {
			files = append(files, "package-lock.json")
		}
	default:
		strategy = StrategyManual
	}
	versions := map[string]string{}
	for _, rel := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if strings.EqualFold(filepath.Base(rel), "package-lock.json") {
			versions[rel] = readNpmLockVersion(path)
		} else if strings.EqualFold(filepath.Base(rel), "Cargo.lock") {
			versions[rel], _ = readConfiguredVersion(repo, releaseconfig.VersionFile{Path: rel, Format: "cargo-lock"})
		} else {
			versions[rel] = readVersion(path, strings.HasSuffix(rel, "Cargo.toml"))
		}
	}
	return strategy, files, versions
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func updateVersionFiles(repo string, files []string, version string) (map[string][]byte, error) {
	return updateConfiguredVersionFiles(repo, legacyVersionFiles(files), version)
}

func legacyVersionFiles(paths []string) []releaseconfig.VersionFile {
	files := make([]releaseconfig.VersionFile, 0, len(paths))
	for _, path := range paths {
		file := releaseconfig.VersionFile{Path: path, Format: "json", JSONPointer: "/version"}
		switch {
		case strings.EqualFold(filepath.Base(path), "package-lock.json"):
			file.Format, file.JSONPointer = "npm-lock", ""
		case strings.EqualFold(filepath.Base(path), "Cargo.lock"):
			file.Format, file.JSONPointer = "cargo-lock", ""
		case strings.EqualFold(filepath.Base(path), "Cargo.toml"):
			file.Format, file.JSONPointer = "cargo", ""
		}
		files = append(files, file)
	}
	return files
}

func expectedLegacyVersionWrites(repo string, files []string, originals map[string][]byte, version string) map[string][]byte {
	return expectedConfiguredVersionWrites(repo, legacyVersionFiles(files), originals, version)
}

func readNpmLockVersion(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	top := jsonVersionRE.FindSubmatch(raw)
	if len(top) < 3 {
		return ""
	}
	nested := npmLockPackageVersionRE.FindSubmatch(raw)
	if len(nested) >= 3 && !bytes.Equal(top[2], nested[2]) {
		return ""
	}
	return string(top[2])
}

func replaceVersionBytesForPath(path string, content []byte, version string) ([]byte, bool) {
	if strings.EqualFold(filepath.Base(path), "package-lock.json") {
		updated, ok := replaceFirstVersion(content, jsonVersionRE, version)
		if !ok {
			return content, false
		}
		if npmLockPackageVersionRE.Match(updated) {
			updated, _ = replaceFirstVersion(updated, npmLockPackageVersionRE, version)
		}
		return updated, true
	}
	pattern := jsonVersionRE
	if strings.HasSuffix(path, "Cargo.toml") {
		pattern = cargoPackageRE
	}
	if !pattern.Match(content) {
		return content, false
	}
	return replaceFirstVersion(content, pattern, version)
}

func replaceFirstVersion(content []byte, pattern *regexp.Regexp, version string) ([]byte, bool) {
	match := pattern.FindSubmatchIndex(content)
	if len(match) < 8 || match[4] < 0 || match[5] < 0 {
		return content, false
	}
	out := make([]byte, 0, len(content)-match[5]+match[4]+len(version))
	out = append(out, content[:match[4]]...)
	out = append(out, version...)
	out = append(out, content[match[5]:]...)
	return out, true
}

// restoreFilesSafely only rolls back bytes that are still exactly what this
// release wrote. If a person or another tool edited the same file meanwhile,
// it is left untouched and reported for manual resolution.
func restoreFilesSafely(originals, expected map[string][]byte) []string {
	skipped := []string{}
	for path, content := range originals {
		written, ok := expected[path]
		current, err := os.ReadFile(path)
		if !ok || err != nil || !bytes.Equal(current, written) {
			skipped = append(skipped, path)
			continue
		}
		mode := os.FileMode(0o644)
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			skipped = append(skipped, path)
		}
	}
	sort.Strings(skipped)
	return skipped
}

func fileHashes(repo string, changes []FileChange, excluded map[string]bool) map[string]string {
	out := map[string]string{}
	for _, change := range changes {
		rel := filepath.ToSlash(change.Path)
		if excluded[rel] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
		if err != nil {
			out[rel] = "<missing>"
			continue
		}
		h := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(h[:])
	}
	return out
}

func sameHashes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func runCheckCommand(ctx context.Context, runner commandRunner, repo, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil
	}
	if runtime.GOOS == "windows" {
		return runner.Run(ctx, repo, "cmd.exe", "/d", "/s", "/c", command)
	}
	return runner.Run(ctx, repo, "/bin/sh", "-lc", command)
}

func redact(text string) string {
	re := regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)
	return re.ReplaceAllString(text, `${1}***@`)
}

func commandContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}

func replaceVersionForTest(content []byte, version string, cargo bool) ([]byte, bool) {
	re := jsonVersionRE
	if cargo {
		re = cargoPackageRE
	}
	if !re.Match(content) {
		return content, false
	}
	updated, ok := replaceFirstVersion(content, re, version)
	return bytes.Clone(updated), ok
}
