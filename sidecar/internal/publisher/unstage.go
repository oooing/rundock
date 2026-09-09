package publisher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Unstage returns the index to HEAD without changing working files or commits.
// Keep an index backup, including partially staged content, before changing it.
func (s *Service) Unstage(ctx context.Context, appID, fingerprint string) (*Preflight, error) {
	pf, err := s.preflight(ctx, appID, false, false, false)
	if err != nil {
		return nil, err
	}
	if !s.reserve(pf.RepoRoot) {
		return nil, &Error{Code: "release_in_progress", Message: "该仓库正在发布，请稍后再试"}
	}
	defer s.release(pf.RepoRoot)
	pf, err = s.PreflightLocal(ctx, appID)
	if err != nil {
		return nil, err
	}
	if fingerprint == "" || fingerprint != pf.StatusFingerprint {
		return nil, &Error{Code: "status_changed", Message: "文件状态已变化，列表已刷新，请检查后重试", Preflight: pf}
	}
	staged := false
	for _, issue := range pf.BlockingIssues {
		if issue.Code == "staged_changes" {
			staged = true
		}
		if issue.Code == "repository_operation" || issue.Code == "merge_conflict" {
			return nil, &Error{Code: issue.Code, Message: issue.Message, Preflight: pf}
		}
	}
	if !staged {
		return s.PreflightLocal(ctx, appID)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	indexPath, err := s.git(ctx, pf.RepoRoot, "rev-parse", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(pf.RepoRoot, indexPath)
	}
	index, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, &Error{Code: "unstage_failed", Message: "无法备份暂存内容，尚未进行修改"}
	}
	backupDir := filepath.Join(filepath.Dir(indexPath), "rundock-index-backups")
	if err = os.MkdirAll(backupDir, 0700); err != nil {
		return nil, err
	}
	backup, err := os.CreateTemp(backupDir, "index-*")
	if err != nil {
		return nil, err
	}
	_, writeErr := backup.Write(index)
	closeErr := backup.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	// No -u: read-tree changes only the index, never the working tree.
	args := []string{"read-tree", "--reset", "HEAD"}
	if _, headErr := s.git(ctx, pf.RepoRoot, "rev-parse", "--verify", "--quiet", "HEAD"); headErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(headErr, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, &Error{Code: "unstage_failed", Message: "取消暂存失败，文件修改仍保留，请重试"}
		}
		args = []string{"read-tree", "--empty"}
	}
	if _, err = s.git(ctx, pf.RepoRoot, args...); err != nil {
		return nil, &Error{Code: "unstage_failed", Message: "取消暂存失败，文件修改仍保留，请重试"}
	}
	return s.PreflightLocal(ctx, appID)
}
