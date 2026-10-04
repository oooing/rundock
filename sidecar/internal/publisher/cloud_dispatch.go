package publisher

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/launcher-sidecar/internal/delivery"
	"github.com/launcher-sidecar/internal/store"
)

func (p *executionPlan) requiresDispatch() bool {
	for _, t := range p.Targets {
		if strings.HasPrefix(t.Steps.Publish, "workflow-dispatch:") {
			return true
		}
	}
	return false
}

func (p *executionPlan) cloudOnly() bool {
	if len(p.Targets) == 0 {
		return false
	}
	for _, target := range p.Targets {
		if !strings.EqualFold(target.Runner.Type, "git-push") {
			return false
		}
	}
	return true
}

func (s *Service) dispatchCloudTarget(ctx context.Context, run *store.ReleaseRun, plan *executionPlan, target planTarget, state *store.ReleaseTargetRun) error {
	if state.PublishDone {
		return nil
	}
	if plan.Automation == nil || plan.Automation.Trigger != "dispatch" || plan.Automation.Account == "" {
		return &Error{Code: "cloud_dispatch_unconfigured", Message: "显式云端构建缺少工作流和账号配置"}
	}
	workflow := strings.TrimPrefix(target.Steps.Publish, "workflow-dispatch:")
	if strings.ContainsAny(workflow, "/\\?#") || (!strings.HasSuffix(workflow, ".yml") && !strings.HasSuffix(workflow, ".yaml")) {
		return &Error{Code: "cloud_dispatch_unconfigured", Message: "工作流文件名无效"}
	}
	version, ok := plan.releaseVersionForGroup(target.VersionGroup)
	if !ok {
		return &Error{Code: "cloud_tag_required", Message: "云端构建缺少冻结版本 Tag"}
	}
	repo := githubRepository(plan.RemoteURL)
	if repo == "" {
		return &Error{Code: "cloud_repository_invalid", Message: "显式工作流需要 GitHub 仓库"}
	}
	account, err := s.delivery.Client.Identity(ctx)
	if err != nil {
		return err
	}
	if !strings.EqualFold(account, plan.Automation.Account) {
		return &Error{Code: "github_account_changed", Message: "当前 GitHub 账号与工作流配置不一致"}
	}
	marker := "rundock:" + run.ID + ":" + target.ID
	base := "repos/" + repo + "/actions/workflows/" + url.PathEscape(workflow)
	found, err := s.findDispatchedRun(ctx, base, run.CommitSHA, version.TagName, marker)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if state.Stage == "dispatch_requested" || state.ErrorCode == "cloud_dispatch_unconfirmed" {
		return &Error{Code: "cloud_dispatch_unconfirmed", Message: "尚未找到上次调用对应的工作流；为避免重复构建，请稍后继续核对"}
	}
	if err = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "running", "dispatch_requested", "", "", true, false); err != nil {
		return err
	}
	err = s.delivery.Client.JSON(ctx, "POST", base+"/dispatches", map[string]any{"ref": version.TagName, "inputs": map[string]string{"release_tag": version.TagName, "release_commit": run.CommitSHA, "release_run_id": run.ID, "target_id": target.ID}}, nil)
	if err == nil {
		return nil
	}
	var rejected *delivery.HTTPError
	if errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 && rejected.Status != 408 {
		// A definitive rejection has not queued a workflow. Unlike a lost response,
		// it is safe to submit again after the account/configuration is corrected.
		message := fmt.Sprintf("GitHub 拒绝工作流调用（HTTP %d），修复权限或配置后可重试", rejected.Status)
		if saveErr := s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", "dispatch_rejected", "cloud_dispatch_rejected", message, true, true); saveErr != nil {
			return saveErr
		}
		return &Error{Code: "cloud_dispatch_rejected", Message: message}
	}
	found, checkErr := s.findDispatchedRun(ctx, base, run.CommitSHA, version.TagName, marker)
	if checkErr == nil && found {
		return nil
	}
	_ = s.store.UpdateReleaseTargetRun(run.ID, target.ID, "failed", "dispatch_requested", "cloud_dispatch_unconfirmed", "调用结果尚未确认", true, true)
	return &Error{Code: "cloud_dispatch_unconfirmed", Message: "工作流调用结果尚未确认；重试只核对原任务，不重复启动构建"}
}

func (s *Service) findDispatchedRun(ctx context.Context, base, commit, tag, marker string) (bool, error) {
	for page := 1; page <= 20; page++ {
		var result struct {
			Runs []struct {
				Title  string `json:"display_title"`
				SHA    string `json:"head_sha"`
				Branch string `json:"head_branch"`
			} `json:"workflow_runs"`
		}
		if err := s.delivery.Client.JSON(ctx, "GET", fmt.Sprintf("%s/runs?event=workflow_dispatch&head_sha=%s&per_page=100&page=%d", base, url.QueryEscape(commit), page), nil, &result); err != nil {
			return false, err
		}
		for _, r := range result.Runs {
			if r.SHA == commit && r.Branch == tag && r.Title == marker {
				return true, nil
			}
		}
		if len(result.Runs) < 100 {
			return false, nil
		}
	}
	return false, &Error{Code: "cloud_dispatch_unconfirmed", Message: "工作流记录查询未完成，请从 GitHub 核对结果"}
}
