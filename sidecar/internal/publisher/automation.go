package publisher

import (
	"net/url"
	"strings"

	"github.com/launcher-sidecar/internal/releaseconfig"
	"github.com/launcher-sidecar/internal/store"
)

func automationHandoffApplies(run *store.ReleaseRun, plan *executionPlan) bool {
	if run == nil || plan == nil || !run.CreateTag || !run.PushRemote {
		return false
	}
	if plan.requiresTagPush() {
		return true
	}
	return plan.Automation != nil &&
		strings.EqualFold(strings.TrimSpace(plan.Automation.Provider), releaseconfig.AutomationGitHubActions) &&
		strings.EqualFold(strings.TrimSpace(plan.Automation.Trigger), releaseconfig.AutomationTriggerTag)
}

func automationHandoffView(run *store.ReleaseRun, plan *executionPlan) *AutomationHandoff {
	if !automationHandoffApplies(run, plan) {
		return nil
	}
	action := automationAction(plan)
	state, message := "pending", "提交完成后将交给 GitHub "+action
	if run.Status == "succeeded" {
		state, message = "handed_off", "已交给 GitHub "+action
	} else if run.Status == "failed" {
		state, message = "not_confirmed", "本地发布失败，尚未确认 GitHub 自动构建结果"
	}
	provider, workflow := releaseconfig.AutomationGitHubActions, ""
	if plan.Automation != nil {
		provider = plan.Automation.Provider
		workflow = plan.Automation.Workflow
	}
	return &AutomationHandoff{
		Provider: provider,
		Workflow: workflow,
		URL:      githubWorkflowURL(plan.RemoteURL, workflow),
		State:    state,
		Message:  message,
	}
}

func automationAction(plan *executionPlan) string {
	if plan != nil && len(plan.Targets) > 0 {
		return "自动构建"
	}
	return "创建源码 Release"
}

func githubWorkflowURL(remote, workflow string) string {
	remote = strings.TrimSpace(remote)
	var repoPath string
	if strings.HasPrefix(remote, "git@github.com:") {
		repoPath = strings.TrimPrefix(remote, "git@github.com:")
	} else if parsed, err := url.Parse(remote); err == nil && strings.EqualFold(parsed.Hostname(), "github.com") {
		repoPath = strings.TrimPrefix(parsed.Path, "/")
	}
	repoPath = strings.TrimSuffix(repoPath, ".git")
	parts := strings.Split(repoPath, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	base := "https://github.com/" + parts[0] + "/" + parts[1] + "/actions"
	if workflow = strings.TrimSpace(workflow); workflow != "" {
		base += "/workflows/" + url.PathEscape(workflow)
	}
	return base
}
