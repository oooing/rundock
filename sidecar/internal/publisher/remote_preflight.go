package publisher

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// One shared budget covers branch fetch and (only when creating tags) the tag
// query. Used for explicitly requested remote checks and retry reconciliation;
// preparing a new release does not require this network round trip.
const remotePreflightTimeout = 20 * time.Second

func (s *Service) checkRemote(parent context.Context, pf *Preflight, checkTags bool) {
	ctx, cancel := commandContext(parent, remotePreflightTimeout)
	defer cancel()
	fail := func(operation, output string, err error) {
		pf.BlockingIssues = append(pf.BlockingIssues, remoteFailure(ctx, operation, output, err))
	}
	out, err := s.git(ctx, pf.RepoRoot, "fetch", pf.RemoteName, pf.Branch, "--no-tags", "--no-recurse-submodules")
	if err != nil {
		fail("获取远程分支", out, err)
		return
	}
	counts, err := s.git(ctx, pf.RepoRoot, "rev-list", "--left-right", "--count", "HEAD...FETCH_HEAD")
	parts := strings.Fields(counts)
	if err != nil || len(parts) != 2 {
		fail("比较远程分支", counts, err)
		return
	}
	localAhead, localErr := strconv.Atoi(parts[0])
	remoteAhead, remoteErr := strconv.Atoi(parts[1])
	if localErr != nil || remoteErr != nil {
		fail("比较远程分支", "", fmt.Errorf("Git 返回了无效的分支提交数量"))
		return
	}
	pf.AheadCount = localAhead
	if remoteAhead > 0 {
		pf.BlockingIssues = append(pf.BlockingIssues, Issue{Code: "branch_behind", Message: "当前分支落后或已与远程分叉，请先同步代码"})
		return
	}
	if localAhead > 0 {
		diffRaw, diffErr := s.gitRaw(ctx, pf.RepoRoot, "diff", "--name-status", "-z", "FETCH_HEAD...HEAD")
		if diffErr == nil {
			pf.UnpushedChanges = parseCommittedChanges(diffRaw)
		}
	}
	if !checkTags {
		return
	}
	remoteTags, err := s.git(ctx, pf.RepoRoot, "ls-remote", "--tags", pf.RemoteName)
	if err != nil {
		fail("检查远程 Tag", remoteTags, err)
		return
	}
	pf.remoteTags = parseTagRevisions(remoteTags)
}

func remoteFailure(ctx context.Context, operation, output string, err error) Issue {
	detail := strings.TrimSpace(output)
	if detail == "" && err != nil {
		detail = err.Error()
	}
	lower := strings.ToLower(detail)
	issue := Issue{Code: "remote_check_failed", Message: operation + "失败"}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		containsAny(lower, "timed out", "timeout was reached", "i/o timeout", "connection timeout"):
		issue = Issue{Code: "remote_timeout", Message: operation + "超时；请检查网络或代理，或关闭“提交后上传”仅保存在本地"}
	case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
		issue = Issue{Code: "remote_check_cancelled", Message: "远程检查已取消"}
	case containsAny(lower, "couldn't find remote ref", "remote ref does not exist", "no such ref"):
		issue = Issue{Code: "remote_branch_missing", Message: "远程仓库中没有当前分支；请确认分支名称或先建立远程分支"}
	case containsAny(lower, "authentication failed", "permission denied", "could not read username", "could not read password", "terminal prompts disabled", "access denied", "repository not found", "authentication required") ||
		regexp.MustCompile(`(?i)(?:returned error:|http(?:/[0-9.]+)?\s+|status(?: code)?[: ]+)\s*(?:401|403)\b`).MatchString(detail):
		issue = Issue{Code: "remote_auth_failed", Message: "远程仓库认证或权限检查失败；请检查 Git 凭据和仓库访问权限"}
	case containsAny(lower, "could not resolve", "couldn't resolve", "failed to connect", "could not connect", "connection refused", "connection reset", "network is unreachable", "unable to access", "proxy", "ssl", "tls"):
		issue = Issue{Code: "remote_network_failed", Message: "无法连接远程仓库；请检查网络、代理或证书设置"}
	}
	if detail != "" {
		detail = redact(detail)
		runes := []rune(detail)
		if len(runes) > 1200 {
			detail = string(runes[:1200]) + "…"
		}
		issue.Message += "\n" + detail
	}
	return issue
}

// These counts describe the last locally known remote state. They help show
// already committed work in the panel; only checkRemote can block a push based
// on the current remote branch.
func (s *Service) readCachedRemoteChanges(ctx context.Context, pf *Preflight) {
	if pf.Branch == "" || !contains(pf.Remotes, pf.RemoteName) {
		return
	}
	ref := "refs/remotes/" + pf.RemoteName + "/" + pf.Branch
	counts, err := s.git(ctx, pf.RepoRoot, "rev-list", "--left-right", "--count", "HEAD..."+ref)
	parts := strings.Fields(counts)
	if err != nil || len(parts) != 2 {
		return
	}
	ahead, err := strconv.Atoi(parts[0])
	if err != nil || ahead <= 0 {
		return
	}
	pf.AheadCount = ahead
	if raw, err := s.gitRaw(ctx, pf.RepoRoot, "diff", "--name-status", "-z", ref+"...HEAD"); err == nil {
		pf.UnpushedChanges = parseCommittedChanges(raw)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func uploadFailureMessage(output string, err error) string {
	issue := remoteFailure(context.Background(), "上传", output, err)
	lower := strings.ToLower(output)
	message := "上传未完成。本地提交和版本记录已保留，可以重试上传，也可以稍后再上传。"
	switch {
	case containsAny(lower, "already exists", "would clobber existing tag"):
		message = "这个版本号在远端已存在，没有覆盖它。本地提交已保留；请返回发布页面，换一个新版本号再发布。"
	case containsAny(lower, "non-fast-forward", "fetch first", "stale info"):
		message = "远端有尚未同步的代码，没有覆盖它。本地提交已保留；请用 Git 工具拉取并合并远端更新，再重新发布。"
	case issue.Code == "remote_auth_failed":
		message = "上传账号没有通过验证。本地提交和版本记录已保留；请在 Git 工具中登录有仓库权限的账号，再重试上传。"
	case issue.Code == "remote_network_failed" || issue.Code == "remote_timeout":
		message = "连接上传服务器失败。本地提交和版本记录已保留；网络恢复后点击“重试上传”，或选择“稍后再上传”退出。"
	}
	if detail := strings.SplitN(issue.Message, "\n", 2); len(detail) == 2 {
		message += "\n" + detail[1]
	}
	return message
}
