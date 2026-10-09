package publisher

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type IgnoreFileRequest struct {
	Path               string `json:"path"`
	ContentFingerprint string `json:"contentFingerprint"`
}
type IgnoreFileResult struct {
	IgnoreFile string     `json:"ignoreFile"`
	Preflight  *Preflight `json:"preflight"`
}

// Ignore exactly one currently untracked file. Never stage, untrack, delete,
// commit or push anything. Existing repository rules and bytes are preserved.
func (s *Service) IgnoreFile(ctx context.Context, appID string, req IgnoreFileRequest) (*IgnoreFileResult, error) {
	rel := filepath.ToSlash(req.Path)
	invalid := &Error{Code: "ignore_invalid_path", Message: "只能忽略项目内未跟踪的普通文件，不能忽略 Git 元数据或忽略清单自身"}
	if rel == "" || strings.ContainsAny(rel, ":\x00\r\n") || path.IsAbs(rel) || path.Clean(rel) != rel {
		return nil, invalid
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || strings.EqualFold(strings.TrimRight(part, " ."), ".git") || strings.EqualFold(part, ".gitignore") || strings.TrimRight(part, " .") != part {
			return nil, invalid
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return nil, invalid
			}
		}
	}
	pf, err := s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, err
	}
	if !s.reserve(pf.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "正在发布或检查，请完成后再修改忽略清单"}
	}
	defer s.release(pf.RepoRoot)
	abs, err := secureProjectPath(pf.RepoRoot, rel, false)
	if err != nil {
		return nil, invalid
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || isPathLink(info) {
		return nil, invalid
	}
	if req.ContentFingerprint == "" || fileContentFingerprint(pf.RepoRoot, FileChange{Path: rel}) != req.ContentFingerprint {
		return nil, &Error{Code: "status_changed", Message: "文件内容已变化，请刷新后重新确认忽略"}
	}
	// Literal pathspec prevents brackets and wildcards in filenames matching siblings.
	tracked, err := s.git(ctx, pf.RepoRoot, "--literal-pathspecs", "ls-files", "--cached", "--", rel)
	if err != nil {
		return nil, err
	}
	if tracked != "" {
		return nil, &Error{Code: "ignore_tracked_file", Message: "文件已被 Git 跟踪；加入忽略清单不能移除远端文件，请使用“本次不提交”"}
	}
	for _, change := range pf.Changes {
		if change.Path == rel && change.Tracked {
			return nil, &Error{Code: "ignore_tracked_file", Message: "已跟踪文件不能通过忽略清单取消跟踪"}
		}
	}
	// Append to the deepest existing .gitignore, so a nested negation cannot
	// silently override the new rule. Otherwise use the repository root.
	ignoreRel := ".gitignore"
	for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
		candidate := path.Join(dir, ".gitignore")
		if _, err := os.Lstat(filepath.Join(pf.RepoRoot, filepath.FromSlash(candidate))); err == nil {
			ignoreRel = candidate
			break
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if _, err := s.git(ctx, pf.RepoRoot, "check-ignore", "--quiet", "--", rel); err == nil {
		return &IgnoreFileResult{IgnoreFile: ignoreRel, Preflight: pf}, nil
	}
	found := false
	for _, change := range pf.Changes {
		if change.Path == rel && !change.Tracked {
			found = true
		}
	}
	if !found {
		return nil, &Error{Code: "status_changed", Message: "文件已不在待提交列表，请刷新后重试"}
	}
	target := filepath.Join(pf.RepoRoot, filepath.FromSlash(ignoreRel))
	var original []byte
	mode := os.FileMode(0644)
	if info, statErr := os.Lstat(target); statErr == nil {
		if !info.Mode().IsRegular() || isPathLink(info) || info.Size() > 1024*1024 {
			return nil, &Error{Code: "ignore_unsafe_file", Message: "忽略清单不是可安全编辑的普通文本文件"}
		}
		if _, err := secureProjectPath(pf.RepoRoot, ignoreRel, false); err != nil {
			return nil, err
		}
		original, err = os.ReadFile(target)
		if err != nil {
			return nil, err
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if !utf8.Valid(original) || bytes.ContainsRune(original, 0) {
		return nil, &Error{Code: "ignore_unsafe_file", Message: "忽略清单不是 UTF-8 文本，请手动编辑以避免损坏原文件"}
	}
	localRel := rel
	if dir := path.Dir(ignoreRel); dir != "." {
		localRel = strings.TrimPrefix(rel, dir+"/")
	}
	rule := "/" + strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]", " ", "\\ ", "#", "\\#", "!", "\\!").Replace(localRel)
	newline := "\n"
	if bytes.Contains(original, []byte("\r\n")) {
		newline = "\r\n"
	}
	addition := rule + newline
	if len(original) > 0 && original[len(original)-1] != '\n' {
		addition = newline + addition
	}
	// Use a sibling temporary file and rename: no truncation or partial writes,
	// and replacing a hardlink will not modify its other targets.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".rundock-ignore-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(append(append([]byte{}, original...), []byte(addition)...))
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	current, readErr := os.ReadFile(target)
	if (readErr != nil && !os.IsNotExist(readErr)) || !bytes.Equal(current, original) {
		return nil, &Error{Code: "status_changed", Message: "忽略清单已被其他操作修改，请刷新后重试"}
	}
	if err = os.Rename(tmpName, target); err != nil {
		return nil, err
	}
	fresh, err := s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, err
	}
	return &IgnoreFileResult{IgnoreFile: ignoreRel, Preflight: fresh}, nil
}
