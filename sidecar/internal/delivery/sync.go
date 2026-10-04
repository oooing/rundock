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

// Sync observes a public version endpoint. It never runs deployments and never
// changes a successful release to failed when the server is still catching up.
func (e *Engine) Sync(ctx context.Context, b Batch) error {
	if b.SyncURL == "" {
		return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "unconfigured", "未配置服务器同步验证")
	}
	u, err := url.Parse(b.SyncURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || !strings.HasPrefix(b.SyncPointer, "/") {
		return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "unverified", "同步检查配置无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", b.SyncURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "unverified", "尚无法连接服务器，发布结果不受影响")
	}
	defer res.Body.Close()
	var doc any
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc) != nil {
		return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "unverified", "服务器尚未返回可验证的版本信息")
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
		return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "pending", "服务器尚未同步到本次版本")
	}
	return e.Store.UpdateDeliverySync(b.RunID, b.GroupID, "verified", "服务器版本已核验："+b.Version)
}
