package delivery

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// The stored requested state fences lost responses/restarts. A retry reconciles
// the existing run; only a definitive GitHub rejection allows another dispatch.
// The workflow itself verifies the frozen release and blocks stale promotion.
func (e *Engine) deploymentReady(ctx context.Context, b Batch) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	unlock, err := Lock(e.Store.ReleaseDataDir(), "deployment:"+b.Repository+":"+b.Tag)
	if err != nil {
		return false, nil // An existing check owns this batch; keep its status.
	}
	defer unlock()
	rows, err := e.Store.ReleaseDeliveries(b.RunID)
	if err != nil {
		return false, err
	}
	state := ""
	for _, row := range rows {
		if row.GroupID == b.GroupID && row.State == "published" {
			state = row.SyncState
		}
	}
	if state == "verified" {
		return true, nil
	}
	if state == "" {
		return false, failure("deployment_release_required", "请先完成 GitHub Release 发布")
	}
	base := "repos/" + b.Repository + "/actions/workflows/" + url.PathEscape(b.DeploymentWorkflow)
	marker := "rundock-deploy:" + b.RunID + ":" + b.GroupID
	for page := 1; page <= 20; page++ {
		var result struct {
			Runs []struct {
				Title      string `json:"display_title"`
				SHA        string `json:"head_sha"`
				Branch     string `json:"head_branch"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				URL        string `json:"html_url"`
			} `json:"workflow_runs"`
		}
		if err = e.Client.JSON(ctx, "GET", fmt.Sprintf("%s/runs?event=workflow_dispatch&head_sha=%s&per_page=100&page=%d", base, url.QueryEscape(b.Commit), page), nil, &result); err != nil {
			return false, err
		}
		for _, run := range result.Runs {
			if run.Title != marker || run.SHA != b.Commit || run.Branch != b.Tag {
				continue
			}
			if run.Status == "completed" && run.Conclusion == "success" {
				return true, nil // Still require the live version endpoint below.
			}
			if run.Status == "completed" {
				return false, e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "deployment_failed", "服务器更新未完成，请查看任务并重试："+run.URL)
			}
			return false, e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "deploying", "正在打包镜像并等待服务器更新："+run.URL)
		}
		if len(result.Runs) < 100 {
			break
		}
		if page == 20 {
			return false, failure("deployment_lookup_incomplete", "部署任务查询未完成，请稍后检查")
		}
	}
	if state == "deployment_requested" || state == "deploying" || state == "deployment_failed" {
		return false, nil // Eventual consistency is not permission to duplicate work.
	}
	if b.Workflows[b.DeploymentWorkflow] == "" {
		return false, failure("deployment_workflow_missing", "封存配置缺少服务器部署工作流")
	}
	if err = e.Preflight(ctx, b); err != nil {
		return false, err
	}
	if err = e.verifyTag(ctx, b); err != nil {
		return false, err
	}
	release, err := e.findRelease(ctx, b)
	if err != nil {
		return false, err
	}
	if release == nil || release.Draft {
		return false, failure("deployment_release_required", "GitHub Release 尚未公开")
	}
	if err = e.verifyAssets(ctx, b, release.ID); err != nil {
		return false, err
	}
	if err = e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "deployment_requested", "已请求服务器更新，正在确认任务"); err != nil {
		return false, err
	}
	err = e.Client.JSON(ctx, "POST", base+"/dispatches", map[string]any{
		"ref": b.Tag, "inputs": map[string]string{"release_tag": b.Tag, "release_commit": b.Commit, "release_run_id": b.RunID, "target_id": b.GroupID},
	}, nil)
	var rejected *HTTPError
	if errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 && rejected.Status != 408 {
		return false, e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "deployment_rejected", fmt.Sprintf("服务器更新请求被拒绝（HTTP %d），修复权限后重新检查", rejected.Status))
	}
	// Unknown outcome retains requested state. Later checks find the original run.
	return false, err
}
