package publisher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const managedIgnoreStart = "# BEGIN RunDock managed ignores v1"
const managedIgnoreEnd = "# END RunDock managed ignores v1"

type ManagedIgnoreGroup struct {
	ID       string   `json:"id"`
	Patterns []string `json:"patterns"`
	Enabled  bool     `json:"enabled"`
}

type ManagedIgnorePolicy struct {
	File     string               `json:"file"`
	Revision string               `json:"revision"`
	Managed  bool                 `json:"managed"`
	Groups   []ManagedIgnoreGroup `json:"groups"`
}

type ManagedIgnoreUpdate struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision"`
}

type ManagedIgnoreResult struct {
	Policy    *ManagedIgnorePolicy `json:"policy"`
	Preflight *Preflight           `json:"preflight"`
}

func defaultIgnoreGroups() []ManagedIgnoreGroup {
	return []ManagedIgnoreGroup{
		{ID: "demos", Enabled: true, Patterns: []string{"/[dD][eE][mM][oO]/", "/[dD][eE][mM][oO][sS]/"}},
		{ID: "verification", Enabled: true, Patterns: []string{"/artifacts/", "/outputs/", "/reports/"}},
		// Deliberately omit generic build/, dist/, target/ and tests/: these
		// names can contain real source. Only known cache/output paths belong here.
		{ID: "caches", Enabled: true, Patterns: []string{"node_modules/", "__pycache__/", ".pytest_cache/", ".mypy_cache/", ".ruff_cache/", ".cache/", ".gradle/", ".next/", ".nuxt/", ".vite/", ".wxt/", ".svelte-kit/", ".turbo/", ".parcel-cache/", ".tmp/", "**/src-tauri/target/"}},
	}
}

func ignoreRevision(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func renderManagedIgnores(groups []ManagedIgnoreGroup, newline string) string {
	lines := []string{managedIgnoreStart, "# Local files stay on disk. Restore groups in RunDock; tracked files are unchanged."}
	for _, group := range groups {
		if !group.Enabled {
			lines = append(lines, "# restored: "+group.ID)
			continue
		}
		lines = append(lines, "# ignored: "+group.ID)
		lines = append(lines, group.Patterns...)
	}
	lines = append(lines, managedIgnoreEnd)
	return strings.Join(lines, newline) + newline
}

// Parse only our exact block. Unknown/manual edits fail closed rather than
// replacing user rules. Prefix/suffix bytes and line endings are retained.
func parseManagedIgnores(raw []byte) (*ManagedIgnorePolicy, int, int, error) {
	policy := &ManagedIgnorePolicy{File: ".gitignore", Revision: ignoreRevision(raw), Groups: defaultIgnoreGroups()}
	text := string(raw)
	start, end := strings.Index(text, managedIgnoreStart), strings.Index(text, managedIgnoreEnd)
	invalid := &Error{Code: "ignore_policy_modified", Message: "RunDock 的忽略规则被手动修改，已保留原内容。请核对 .gitignore 中的 RunDock 区块后重新加载。"}
	if start == -1 && end == -1 {
		return policy, -1, -1, nil
	}
	if start < 0 || end < start || strings.Count(text, managedIgnoreStart) != 1 || strings.Count(text, managedIgnoreEnd) != 1 {
		return nil, 0, 0, invalid
	}
	if start != 0 && text[start-1] != '\n' && !(start == 3 && bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf})) {
		return nil, 0, 0, invalid
	}
	end += len(managedIgnoreEnd)
	newline := "\n"
	if strings.Contains(text[start:end], "\r\n") {
		newline = "\r\n"
	}
	if !strings.HasPrefix(text[end:], newline) {
		return nil, 0, 0, invalid
	}
	end += len(newline)
	for i := range policy.Groups {
		policy.Groups[i].Enabled = !strings.Contains(text[start:end], "# restored: "+policy.Groups[i].ID+newline)
	}
	if text[start:end] != renderManagedIgnores(policy.Groups, newline) {
		return nil, 0, 0, invalid
	}
	policy.Managed = true
	return policy, start, end, nil
}

func (s *Service) managedIgnoreRoot(ctx context.Context, appID string) (string, error) {
	app, err := s.store.GetApp(appID)
	if err != nil {
		return "", err
	}
	if app == nil {
		return "", &Error{Code: "app_not_found", Message: "项目不存在"}
	}
	root, err := s.git(ctx, app.Cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return "", &Error{Code: "not_repository", Message: "项目目录不在 Git 仓库中"}
	}
	return filepath.Clean(canonicalRepositoryPath(root)), nil
}

// Called explicitly when opening the release form, not from read-only preflight
// or notes generation. Git itself prunes untracked ignored directories before
// any content hashing. No index, tracked file, commit or local output is removed.
func (s *Service) EnsureManagedIgnores(ctx context.Context, appID string) (*ManagedIgnorePolicy, error) {
	return s.editManagedIgnores(ctx, appID, nil)
}

func (s *Service) UpdateManagedIgnores(ctx context.Context, appID string, req ManagedIgnoreUpdate) (*ManagedIgnoreResult, error) {
	policy, err := s.editManagedIgnores(ctx, appID, &req)
	if err != nil {
		return nil, err
	}
	pf, err := s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, &Error{Code: "ignore_refresh_failed", Message: fmt.Sprintf("忽略设置已保存，但刷新文件失败。请重新加载；本地文件未删除。原因：%v", err)}
	}
	return &ManagedIgnoreResult{Policy: policy, Preflight: pf}, nil
}

func (s *Service) editManagedIgnores(ctx context.Context, appID string, req *ManagedIgnoreUpdate) (*ManagedIgnorePolicy, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	root, err := s.managedIgnoreRoot(ctx, appID)
	if err != nil {
		return nil, err
	}
	edit, err := readIgnoreEdit(root, ".gitignore")
	if err != nil {
		return nil, err
	}
	policy, start, end, err := parseManagedIgnores(edit.original)
	if err != nil {
		return nil, err
	}
	if req == nil && policy.Managed {
		return policy, nil
	}
	if !s.reserve(root) {
		return nil, &Error{Code: "release_in_progress", Message: "正在发布或检查，暂不能修改忽略清单；任务结束后再试。"}
	}
	defer s.release(root)
	if req != nil {
		if !policy.Managed || req.Revision == "" || req.Revision != policy.Revision {
			return nil, &Error{Code: "ignore_policy_stale", Message: "忽略清单已变化，请重新加载后再操作。"}
		}
		found := false
		for i := range policy.Groups {
			if policy.Groups[i].ID == req.ID {
				policy.Groups[i].Enabled = req.Enabled
				found = true
			}
		}
		if !found {
			return nil, &Error{Code: "ignore_invalid_group", Message: "未知的忽略分组"}
		}
	}
	newline := "\n"
	if bytes.Contains(edit.original, []byte("\r\n")) {
		newline = "\r\n"
	}
	block := []byte(renderManagedIgnores(policy.Groups, newline))
	if start >= 0 {
		edit.updated = append(append(append([]byte{}, edit.original[:start]...), block...), edit.original[end:]...)
	} else {
		// Prepend so the user's later exception rules keep their precedence.
		bom := 0
		if bytes.HasPrefix(edit.original, []byte{0xef, 0xbb, 0xbf}) {
			bom = 3
		}
		edit.updated = append(append(append([]byte{}, edit.original[:bom]...), block...), edit.original[bom:]...)
	}
	if len(edit.updated) > 1024*1024 {
		return nil, &Error{Code: "ignore_unsafe_file", Message: "忽略清单超过 1 MB，请先整理规则"}
	}
	if !bytes.Equal(edit.original, edit.updated) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := writeIgnoreEdit(root, edit); err != nil {
			return nil, &Error{Code: "ignore_write_failed", Message: "无法保存忽略清单：" + err.Error()}
		}
	}
	policy.Managed = true
	policy.Revision = ignoreRevision(edit.updated)
	return policy, nil
}
