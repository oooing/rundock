package publisher

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

// Git's index lock serializes this final transaction with normal staging and
// checkout commands. Never stage mutable working files or reset the user's index.
func (s *Service) commitAcceptedCandidate(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, pf *Preflight, message string) (string, error) {
	cand := s.lookupCandidate(plan.CandidateID)
	if cand == nil {
		return "", &Error{Code: "candidate_not_found", Message: "验收候选已失效"}
	}
	cand.mu.Lock()
	defer cand.mu.Unlock()
	index := filepath.Join(cand.GitDir, "index")
	lock, err := os.OpenFile(index+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", &Error{Code: "index_busy", Message: "Git 暂存区正在使用，请稍后重新检查"}
	}
	defer func() { lock.Close(); os.Remove(index + ".lock") }()
	if err := validateCandidateIndex(ctx, run.RepoRoot); err != nil {
		return "", err
	}
	if err := s.validateCandidateBinding(ctx, cand, cand.Request); err != nil {
		return "", err
	}
	if _, err := gitOutput(ctx, run.RepoRoot, "diff", "--cached", "--quiet", "--ita-visible-in-index", cand.HeadSHA, "--"); err != nil {
		return "", &Error{Code: "staged_changes", Message: "暂存区已有改动，已停止提交并保留原内容"}
	}
	branch, err := gitOutput(ctx, run.RepoRoot, "symbolic-ref", "HEAD")
	if err != nil || branch != "refs/heads/"+pf.Branch {
		return "", &Error{Code: "status_changed", Message: "当前分支已变化，请重新检查"}
	}
	if err := validateCandidateBytes(cand); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(cand.Index)
	if err != nil {
		return "", err
	}
	if _, err = lock.Write(raw); err != nil {
		return "", err
	}
	if err = lock.Sync(); err != nil {
		return "", err
	}
	if err = lock.Close(); err != nil {
		return "", err
	}
	headTree, err := gitOutput(ctx, run.RepoRoot, "rev-parse", cand.HeadSHA+"^{tree}")
	if err != nil {
		return "", err
	}
	sha := cand.HeadSHA
	if headTree != cand.TreeHash {
		sha, err = isolatedGit(ctx, cand, "commit-tree", cand.TreeHash, "-p", cand.HeadSHA, "-m", message)
		if err != nil {
			return "", &Error{Code: "commit_failed", Message: "无法创建验收提交，请检查 Git 用户身份和仓库权限"}
		}
		sha = strings.TrimSpace(sha)
	}
	// Compare-and-swap cannot overwrite a commit made by another Git client.
	if _, err = s.git(ctx, run.RepoRoot, "update-ref", branch, sha, cand.HeadSHA); err != nil {
		return "", &Error{Code: "status_changed", Message: "分支在提交前已变化，未覆盖其他提交"}
	}
	if err = os.Rename(index+".lock", index); err != nil {
		// Only roll back our own ref update. Never reset an independently advanced ref.
		_, rollbackErr := s.git(ctx, run.RepoRoot, "update-ref", branch, cand.HeadSHA, sha)
		if rollbackErr != nil {
			run.CommitSHA = sha
			return sha, &Error{Code: "index_sync_failed", Message: "提交已创建，但暂存区同步失败；请检查 Git 状态后再继续"}
		}
		return "", &Error{Code: "index_sync_failed", Message: "无法同步暂存区，已撤回本次分支更新，工作区未改动"}
	}
	// Keep the development worktree byte-for-byte intact. In particular, syncing
	// generated versions here could overwrite an editor's concurrent replacement.
	for rel := range cand.VersionFiles {
		s.log(run.ID, "event", "计划版本已写入提交，工作区内容保留："+rel)
	}

	return sha, nil
}

func validateCandidateIndex(ctx context.Context, repo string) error {
	entries, err := gitOutput(ctx, repo, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(entries, "\x00") {
		if entry != "" && (entry[0] == 'S' || (entry[0] >= 'a' && entry[0] <= 'z')) {
			return &Error{Code: "index_flags_unsupported", Message: "暂存区包含 skip-worktree 或 assume-unchanged 标记，请先恢复普通跟踪再创建候选"}
		}
	}
	return nil
}
