package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Sync reconciles an explicitly configured deployment, then observes the public
// version endpoint. A pending deployment never makes a published artifact fail.
func (e *Engine) Sync(ctx context.Context, b Batch) error {
	if len(b.Components) > 0 {
		states, messages := []string{}, []string{}
		for _, component := range b.Components {
			member := b
			member.Version, member.SyncURL, member.SyncPointer = component.Version, component.SyncURL, component.SyncPointer
			state, message, err := checkSync(ctx, member)
			if err != nil {
				return err
			}
			states = append(states, state)
			messages = append(messages, component.Name+"："+message)
		}
		return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, combinedSyncState(states), strings.Join(messages, "；"))
	}
	if b.DeploymentStrategy == "server-pull" && b.DeploymentWorkflow != "" {
		return failure("deployment_strategy_conflict", "服务器拉取更新不能同时触发云端部署")
	}
	if b.DeploymentWorkflow != "" {
		ready, err := e.deploymentReady(ctx, b)
		if err != nil || !ready {
			return err
		}
	}
	state, message, err := checkSync(ctx, b)
	if err != nil {
		return err
	}
	return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, state, message)
}

func combinedSyncState(states []string) string {
	// Never report fully verified when any component remains unverified.
	for _, candidate := range []string{"unverified", "pending", "unconfigured"} {
		for _, state := range states {
			if state == candidate {
				return candidate
			}
		}
	}
	return "verified"
}

func checkSync(ctx context.Context, b Batch) (string, string, error) {
	if b.SyncURL == "" {
		return "unconfigured", "未配置服务器同步验证", nil
	}
	u, err := url.Parse(b.SyncURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || !strings.HasPrefix(b.SyncPointer, "/") {
		return "unverified", "同步检查配置无效", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", b.SyncURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return "unverified", "尚无法连接服务器，发布结果不受影响", nil
	}
	defer res.Body.Close()
	var doc any
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc) != nil {
		return "unverified", "服务器尚未返回可验证的版本信息", nil
	}
	for _, part := range strings.Split(strings.TrimPrefix(b.SyncPointer, "/"), "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		obj, ok := doc.(map[string]any)
		if !ok {
			doc = nil
			break
		}
		doc = obj[key]
	}
	version, ok := doc.(string)
	if !ok || version != b.Version {
		return "pending", "服务器尚未同步到本次版本", nil
	}
	return "verified", "服务器版本已核验：" + b.Version, nil
}
