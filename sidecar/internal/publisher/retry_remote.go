package publisher

import (
	"context"
	"strings"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

type retryRemoteState struct {
	branchUploaded bool
	uploadedTags   map[string]bool
}

func retryNeedsGitUpload(run *store.ReleaseRun, plan *executionPlan) bool {
	if run == nil || !run.PushRemote {
		return false
	}
	return preTargetRetryStage(run.Stage) || run.Stage == "tagging" || run.Stage == "pushing_branch" || run.Stage == "pushing_tag" ||
		(postTargetRetryStage(run.Stage) && plan != nil && plan.requiresGitPush())
}

// Only commands that will actually run again need a renewed confirmation.
// Git pushes are checked automatically; completed custom steps remain skipped.
func retryCustomExternalTargets(run *store.ReleaseRun, plan *executionPlan, states []*store.ReleaseTargetRun) []string {
	if run == nil || run.Status != "failed" || run.CommitSHA == "" || !retryableStage(run.Stage) || run.ErrorCode == "build_changed_tree" || plan == nil {
		return nil
	}
	byID := map[string]*store.ReleaseTargetRun{}
	for _, state := range states {
		byID[state.TargetID] = state
	}
	result := []string{}
	for _, target := range plan.Targets {
		if strings.EqualFold(strings.TrimSpace(target.Runner.Type), releaseconfig.RunnerGitPush) {
			continue
		}
		state := byID[target.ID]
		if state == nil {
			continue
		}
		actions := []string{}
		attempted := (state.Status == "running" || state.Status == "failed") && state.ErrorCode != "artifact_changed" && state.ErrorCode != "target_working_dir_invalid"
		if attempted && state.Stage == "publish" && target.Selection.Publish && !state.PublishDone && strings.TrimSpace(target.Steps.Publish) != "" {
			actions = append(actions, "发布")
		}
		if attempted && state.Stage == "deploy" && target.Selection.Deploy && !state.DeployDone && strings.TrimSpace(target.Steps.Deploy) != "" {
			actions = append(actions, "部署")
		}
		if len(actions) == 0 {
			continue
		}
		name := strings.TrimSpace(target.Name)
		if name == "" {
			name = target.ID
		}
		result = append(result, name+"："+strings.Join(actions, "、"))
	}
	return result
}

// Inspect all relevant refs before any remote mutation, including every tag in
// a multi-version release. Object-only fetches keep HEAD, local tags, tracking
// refs and FETCH_HEAD unchanged while letting us compare previously unseen
// remote commits or annotated tag contents.
func (s *Service) inspectRetryRemote(parent context.Context, run *store.ReleaseRun, plan *executionPlan) (*retryRemoteState, error) {
	ctx, cancel := commandContext(parent, remotePreflightTimeout)
	defer cancel()
	if plan != nil && strings.TrimSpace(plan.RemoteURL) != "" {
		currentRemote, err := s.git(ctx, run.RepoRoot, "remote", "get-url", run.RemoteName)
		if err != nil {
			return nil, retryRemoteError(ctx, currentRemote, err)
		}
		if redact(strings.TrimSpace(currentRemote)) != strings.TrimSpace(plan.RemoteURL) {
			return nil, &Error{Code: "remote_destination_changed", Message: "远程仓库地址已改变，本次重试已停止。请重新发布以确认上传位置"}
		}
	}
	state := &retryRemoteState{uploadedTags: map[string]bool{}}
	branchRef := "refs/heads/" + run.Branch
	versions := []store.ReleaseVersion{}
	if run.CreateTag {
		versions = releaseVersionsForRun(run, plan)
	}
	args := []string{"ls-remote", run.RemoteName, branchRef}
	for _, version := range versions {
		args = append(args, "refs/tags/"+version.TagName, "refs/tags/"+version.TagName+"^{}")
	}
	raw, err := s.git(ctx, run.RepoRoot, args...)
	if err != nil {
		return nil, retryRemoteError(ctx, raw, err)
	}
	refs := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			refs[parts[1]] = parts[0]
		}
	}
	for _, version := range versions {
		ref := "refs/tags/" + version.TagName
		remoteObject := refs[ref]
		if remoteObject == "" {
			continue
		}
		// An existing lightweight tag is not the annotated release we froze.
		if refs[ref+"^{}"] != run.CommitSHA {
			return nil, retryTagConflict(version.TagName)
		}
		localObject, localErr := s.git(ctx, run.RepoRoot, "rev-parse", "--verify", ref)
		if localErr != nil {
			return nil, &Error{Code: "tag_collision", Message: "本地版本 Tag 已变化，请重新发布：" + version.TagName}
		}
		if remoteObject != localObject {
			if _, err := s.git(ctx, run.RepoRoot, "cat-file", "-e", remoteObject); err != nil {
				if out, err := s.fetchRetryObject(ctx, run, ref); err != nil {
					return nil, retryRemoteError(ctx, out, err)
				}
			}
		}
		contents, err := s.git(ctx, run.RepoRoot, "cat-file", "tag", remoteObject)
		if err != nil {
			return nil, retryRemoteError(ctx, contents, err)
		}
		expected, err := tagMessageForVersion(plan, version)
		if err != nil {
			return nil, err
		}
		parts := strings.SplitN(strings.ReplaceAll(contents, "\r\n", "\n"), "\n\n", 2)
		if len(parts) != 2 || !strings.Contains("\n"+parts[0]+"\n", "\nobject "+run.CommitSHA+"\n") ||
			!strings.Contains("\n"+parts[0]+"\n", "\ntype commit\n") ||
			!strings.Contains("\n"+parts[0]+"\n", "\ntag "+version.TagName+"\n") || normalizeTagMessage(parts[1]) != normalizeTagMessage(expected) {
			return nil, retryTagConflict(version.TagName)
		}
		state.uploadedTags[version.TagName] = true
	}
	remoteBranch := refs[branchRef]
	if remoteBranch == "" {
		return state, nil
	}
	if remoteBranch == run.CommitSHA {
		state.branchUploaded = true
		return state, nil
	}
	if _, err := s.git(ctx, run.RepoRoot, "cat-file", "-e", remoteBranch+"^{commit}"); err != nil {
		if out, err := s.fetchRetryObject(ctx, run, branchRef); err != nil {
			return nil, retryRemoteError(ctx, out, err)
		}
	}
	if _, err := s.git(ctx, run.RepoRoot, "cat-file", "-e", remoteBranch+"^{commit}"); err != nil {
		return nil, retryRemoteError(ctx, "", err)
	}
	if _, err := s.git(ctx, run.RepoRoot, "merge-base", "--is-ancestor", run.CommitSHA, remoteBranch); err == nil {
		state.branchUploaded = true
		return state, nil
	}
	if _, err := s.git(ctx, run.RepoRoot, "merge-base", "--is-ancestor", remoteBranch, run.CommitSHA); err == nil {
		return state, nil
	}
	if ctx.Err() != nil {
		return nil, retryRemoteError(ctx, "", ctx.Err())
	}
	return nil, &Error{Code: "remote_branch_diverged", Message: "远端分支已有不同的更新，本次没有继续上传。请同步代码后重新发布，以免覆盖他人的提交"}
}

func (s *Service) fetchRetryObject(ctx context.Context, run *store.ReleaseRun, ref string) (string, error) {
	return s.git(ctx, run.RepoRoot, "fetch", "--refmap=", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", run.RemoteName, ref)
}

func retryTagConflict(tag string) *Error {
	return &Error{Code: "remote_tag_conflict", Message: "远端版本 " + tag + " 已存在，但提交或版本说明与本次发布不同。本次没有继续上传，请使用新版本发布"}
}

func retryRemoteError(ctx context.Context, output string, err error) *Error {
	issue := remoteFailure(ctx, "核对上传结果", output, err)
	if issue.Code == "remote_timeout" {
		detail := strings.SplitN(issue.Message, "\n", 2)
		issue.Message = "连接远端超时，本次没有继续上传。请检查网络后重试"
		if len(detail) > 1 {
			issue.Message += "\n" + detail[1]
		}
	} else {
		issue.Message = "未能确认上传结果，本次没有继续上传。" + issue.Message
	}
	return &Error{Code: issue.Code, Message: issue.Message}
}
