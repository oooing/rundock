package publisher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
type IgnoreFilesResult struct {
	IgnoreFiles []string   `json:"ignoreFiles"`
	Preflight   *Preflight `json:"preflight"`
}

// Single and bulk operations share the same validation and persistence rules.
func (s *Service) IgnoreFile(ctx context.Context, appID string, req IgnoreFileRequest) (*IgnoreFileResult, error) {
	result, err := s.IgnoreFiles(ctx, appID, []IgnoreFileRequest{req})
	if err != nil {
		return nil, err
	}
	return &IgnoreFileResult{IgnoreFile: result.IgnoreFiles[0], Preflight: result.Preflight}, nil
}

func validIgnorePath(rel string) bool {
	if rel == "" || strings.ContainsAny(rel, ":\x00\r\n") || path.IsAbs(rel) || path.Clean(rel) != rel {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || strings.EqualFold(strings.TrimRight(part, " ."), ".git") || strings.EqualFold(part, ".gitignore") || strings.TrimRight(part, " .") != part {
			return false
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return false
			}
		}
	}
	return true
}

type ignoreEdit struct {
	rel      string
	target   string
	original []byte
	updated  []byte
	mode     os.FileMode
	existed  bool
}

// IgnoreFiles performs two project scans for the entire request, not for each
// file. All inputs and destination files are validated before the first write.
// Each applicable .gitignore is replaced once. Cross-file writes are not a
// transaction; retries query Git and skip rules already applied successfully.
func (s *Service) IgnoreFiles(ctx context.Context, appID string, requests []IgnoreFileRequest) (*IgnoreFilesResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	invalid := &Error{Code: "ignore_invalid_path", Message: "只能忽略项目内未跟踪的普通文件，不能忽略 Git 元数据或忽略清单自身"}
	if len(requests) == 0 || len(requests) > 5000 {
		return nil, &Error{Code: "ignore_invalid_path", Message: "每次请选择 1 至 5000 个文件"}
	}
	unique := make([]IgnoreFileRequest, 0, len(requests))
	seen := map[string]string{}
	for _, req := range requests {
		req.Path = filepath.ToSlash(req.Path)
		if !validIgnorePath(req.Path) {
			return nil, invalid
		}
		key := gitPathKey(req.Path)
		if prior, ok := seen[key]; ok {
			if prior != req.ContentFingerprint {
				return nil, &Error{Code: "status_changed", Message: "同一文件的内容记录不一致，请刷新后重试"}
			}
			continue
		}
		seen[key] = req.ContentFingerprint
		unique = append(unique, req)
	}
	pf, err := s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, err
	}
	if !s.reserve(pf.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "正在发布或检查，请完成后再修改忽略清单"}
	}
	defer s.release(pf.RepoRoot)
	trackedRaw, err := s.gitRaw(ctx, pf.RepoRoot, "ls-files", "--cached", "-z")
	if err != nil {
		return nil, err
	}
	tracked := map[string]bool{}
	for _, name := range strings.Split(trackedRaw, "\x00") {
		tracked[gitPathKey(name)] = true
	}
	changes := map[string]FileChange{}
	for _, change := range pf.Changes {
		changes[gitPathKey(change.Path)] = change
	}
	var input bytes.Buffer
	for _, req := range unique {
		input.WriteString(req.Path)
		input.WriteByte(0)
	}
	ignoredRaw, err := s.gitRawWithInput(ctx, pf.RepoRoot, input.Bytes(), "check-ignore", "-z", "--stdin")
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, err
		}
	}
	ignored := map[string]bool{}
	for _, name := range strings.Split(ignoredRaw, "\x00") {
		ignored[gitPathKey(name)] = true
	}
	edits := map[string]*ignoreEdit{}
	destinations := map[string]bool{}
	parentRules := map[string]string{}
	for _, req := range unique {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, key := req.Path, gitPathKey(req.Path)
		abs, err := secureProjectPath(pf.RepoRoot, rel, false)
		if err != nil {
			return nil, invalid
		}
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() || isPathLink(info) {
			return nil, invalid
		}
		if req.ContentFingerprint == "" || fileContentFingerprint(pf.RepoRoot, FileChange{Path: rel}) != req.ContentFingerprint {
			return nil, &Error{Code: "status_changed", Message: fmt.Sprintf("文件内容已变化，请刷新后重试：%s", rel)}
		}
		change, found := changes[key]
		if tracked[key] || (found && change.Tracked) {
			return nil, &Error{Code: "ignore_tracked_file", Message: fmt.Sprintf("文件已被 Git 跟踪，不能直接忽略；可选择本次跳过：%s", rel)}
		}
		parent := path.Dir(rel)
		ignoreRel, ok := parentRules[parent]
		if !ok {
			ignoreRel = ".gitignore"
			for dir := parent; dir != "."; dir = path.Dir(dir) {
				candidate := path.Join(dir, ".gitignore")
				if _, statErr := os.Lstat(filepath.Join(pf.RepoRoot, filepath.FromSlash(candidate))); statErr == nil {
					ignoreRel = candidate
					break
				} else if !os.IsNotExist(statErr) {
					return nil, statErr
				}
			}
			parentRules[parent] = ignoreRel
		}
		destinations[ignoreRel] = true
		if ignored[key] {
			continue
		}
		if !found {
			return nil, &Error{Code: "status_changed", Message: fmt.Sprintf("文件已不在待提交列表，请刷新后重试：%s", rel)}
		}
		edit := edits[ignoreRel]
		if edit == nil {
			edit, err = readIgnoreEdit(pf.RepoRoot, ignoreRel)
			if err != nil {
				return nil, err
			}
			edits[ignoreRel] = edit
		}
		localRel := rel
		if dir := path.Dir(ignoreRel); dir != "." {
			localRel = strings.TrimPrefix(rel, dir+"/")
		}
		rule := "/" + strings.NewReplacer("\\", "\\\\", "*", "\\*", "?", "\\?", "[", "\\[", "]", "\\]", " ", "\\ ", "#", "\\#", "!", "\\!").Replace(localRel)
		newline := "\n"
		if bytes.Contains(edit.original, []byte("\r\n")) {
			newline = "\r\n"
		}
		if len(edit.updated) > 0 && edit.updated[len(edit.updated)-1] != '\n' {
			edit.updated = append(edit.updated, newline...)
		}
		edit.updated = append(edit.updated, (rule + newline)...)
		if len(edit.updated) > 1024*1024 {
			return nil, &Error{Code: "ignore_unsafe_file", Message: "忽略清单超过 1 MB，请减少所选文件或手动整理规则"}
		}
	}
	names := make([]string, 0, len(destinations))
	for name := range destinations {
		names = append(names, name)
	}
	sort.Strings(names)
	written := 0
	for _, name := range names {
		edit := edits[name]
		if edit == nil {
			continue
		}
		err := ctx.Err()
		if err == nil {
			err = writeIgnoreEdit(pf.RepoRoot, edit)
		}
		if err != nil {
			return nil, &Error{Code: "ignore_write_failed", Message: fmt.Sprintf("无法写入 %s；已完成 %d 份忽略清单。可重试，已生效规则不会重复写入。原因：%v", name, written, err)}
		}
		written++
	}
	if written == 0 {
		return &IgnoreFilesResult{IgnoreFiles: names, Preflight: pf}, nil
	}
	fresh, err := s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, &Error{Code: "ignore_refresh_failed", Message: fmt.Sprintf("忽略规则已写入，但刷新列表失败。请重新加载；重试不会重复写入。原因：%v", err)}
	}
	// Never report success if Git still lists a requested file.
	for _, change := range fresh.Changes {
		if _, requested := seen[gitPathKey(change.Path)]; requested {
			return nil, &Error{Code: "ignore_not_effective", Message: fmt.Sprintf("忽略规则已写入，但文件仍在列表中，请检查是否被跟踪或规则被其他操作修改：%s", change.Path)}
		}
	}
	return &IgnoreFilesResult{IgnoreFiles: names, Preflight: fresh}, nil
}

func readIgnoreEdit(repo, rel string) (*ignoreEdit, error) {
	edit := &ignoreEdit{rel: rel, target: filepath.Join(repo, filepath.FromSlash(rel)), mode: 0644}
	if info, err := os.Lstat(edit.target); err == nil {
		if !info.Mode().IsRegular() || isPathLink(info) || info.Size() > 1024*1024 {
			return nil, &Error{Code: "ignore_unsafe_file", Message: "忽略清单不是可安全编辑的普通文本文件"}
		}
		if _, err := secureProjectPath(repo, rel, false); err != nil {
			return nil, err
		}
		edit.original, err = os.ReadFile(edit.target)
		if err != nil {
			return nil, err
		}
		edit.mode = info.Mode().Perm()
		edit.existed = true
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if !utf8.Valid(edit.original) || bytes.ContainsRune(edit.original, 0) {
		return nil, &Error{Code: "ignore_unsafe_file", Message: "忽略清单不是 UTF-8 文本，请手动编辑以避免损坏原文件"}
	}
	edit.updated = append([]byte{}, edit.original...)
	return edit, nil
}

func validateIgnoreEdit(repo string, edit *ignoreEdit) error {
	// Recheck parents and the destination after preparing all groups.
	if _, err := secureProjectPath(repo, path.Dir(edit.rel), true); err != nil {
		return err
	}
	info, statErr := os.Lstat(edit.target)
	if statErr == nil && (!info.Mode().IsRegular() || isPathLink(info)) {
		return fmt.Errorf("忽略清单已被替换")
	}
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if (statErr == nil) != edit.existed {
		return fmt.Errorf("忽略清单已被其他操作修改")
	}
	current, readErr := os.ReadFile(edit.target)
	if (readErr != nil && !os.IsNotExist(readErr)) || !bytes.Equal(current, edit.original) {
		return fmt.Errorf("忽略清单已被其他操作修改")
	}
	return nil
}

func writeIgnoreEdit(repo string, edit *ignoreEdit) error {
	if err := validateIgnoreEdit(repo, edit); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(edit.target), ".rundock-ignore-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(edit.mode); err == nil {
		_, err = tmp.Write(edit.updated)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := validateIgnoreEdit(repo, edit); err != nil {
		return err
	}
	// Replacing a hardlink does not modify any other link target.
	return os.Rename(tmp.Name(), edit.target)
}
