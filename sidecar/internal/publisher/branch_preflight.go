package publisher

import (
	"context"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

// Check the actual push destinations before changing versions or building.
// Object-only fetches leave the worktree, tracking refs and FETCH_HEAD intact.
func (s *Service) checkReleaseBranch(parent context.Context, run *store.ReleaseRun, head string) *Error {
	ctx, cancel := commandContext(parent, remotePreflightTimeout)
	defer cancel()
	failure := func(output string, err error) *Error {
		issue := remoteFailure(ctx, "检查上传分支", output, err)
		return &Error{Code: issue.Code, Message: issue.Message}
	}
	urls, err := s.git(ctx, run.RepoRoot, "remote", "get-url", "--push", "--all", run.RemoteName)
	if err != nil {
		return failure(urls, err)
	}
	destinations := nonEmptyLines(urls)
	if len(destinations) == 0 {
		return &Error{Code: "remote_check_failed", Message: "没有可用的上传地址，已停止发布"}
	}
	branchRef := "refs/heads/" + run.Branch
	for _, destination := range destinations {
		raw, err := s.git(ctx, run.RepoRoot, "ls-remote", "--heads", destination, branchRef)
		if err != nil {
			return failure(raw, err)
		}
		if strings.TrimSpace(raw) == "" {
			continue // Publishing a new branch is allowed.
		}
		fields := strings.Fields(raw)
		if len(fields) != 2 || fields[1] != branchRef || (len(fields[0]) != 40 && len(fields[0]) != 64) {
			return &Error{Code: "remote_check_failed", Message: "上传分支返回了无效的代码记录，已停止发布"}
		}
		tip := fields[0]
		if _, err := hex.DecodeString(tip); err != nil {
			return &Error{Code: "remote_check_failed", Message: "上传分支返回了无效的代码记录，已停止发布"}
		}
		if _, err := s.git(ctx, run.RepoRoot, "cat-file", "-e", tip+"^{commit}"); err != nil {
			out, err := s.git(ctx, run.RepoRoot, "fetch", "--refmap=", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", destination, branchRef)
			if err != nil {
				return failure(out, err)
			}
		}
		if _, err := s.git(ctx, run.RepoRoot, "cat-file", "-e", tip+"^{commit}"); err != nil {
			return failure("", err)
		}
		out, err := s.git(ctx, run.RepoRoot, "merge-base", "--is-ancestor", tip, head)
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
				return failure(out, err)
			}
			return &Error{Code: "branch_behind", Message: "远端有尚未同步的代码，已在构建前停止。请先同步代码再发布。"}
		}
	}
	return nil
}
