package releaseconfig

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Delivery is an opt-in destination. Absence preserves local artifact-only builds.
// Account is an identity reference to the local gh credential provider, never a token.
type Delivery struct {
	Provider       string      `json:"provider"`
	Repository     string      `json:"repository"`
	Account        string      `json:"account"`
	Prerelease     bool        `json:"prerelease,omitempty"`
	MakeLatest     bool        `json:"makeLatest,omitempty"`
	WorkflowPolicy string      `json:"workflowPolicy"`
	Sync           *SyncCheck  `json:"sync,omitempty"`
	Deployment     *Deployment `json:"deployment,omitempty"`
}

// Deployment identifies the owner of post-publication deployment.
type Deployment struct {
	Strategy string `json:"strategy,omitempty"`
	Workflow string `json:"workflow,omitempty"`
}

type SyncCheck struct {
	URL         string `json:"url"`
	JSONPointer string `json:"jsonPointer"`
}

type ArtifactRule struct {
	Pattern string `json:"pattern"`
	Min     int    `json:"min"`
	Max     int    `json:"max"`
}

type Verification struct {
	Name           string `json:"name"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

var deliveryRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var deliveryAccountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

func validateDelivery(t Target) error {
	for step, seconds := range t.Timeouts {
		if !strings.Contains("|check|build|package|publish|deploy|", "|"+step+"|") || seconds < 1 || seconds > 86400 {
			return fmt.Errorf("%s：步骤超时必须为 1–86400 秒", t.Name)
		}
	}
	for _, v := range t.Verification {
		if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Command) == "" || v.TimeoutSeconds < 0 || v.TimeoutSeconds > 86400 {
			return fmt.Errorf("%s：产物验证必须包含名称、命令和有效超时", t.Name)
		}
	}
	for _, r := range t.ArtifactRules {
		if r.Pattern == "" || validateRelative(r.Pattern) != nil || r.Min < 1 || r.Max < r.Min || r.Max > 2000 {
			return fmt.Errorf("%s：每项必需产物必须声明有效路径和数量范围", t.Name)
		}
	}
	if t.Delivery == nil {
		return nil
	}
	d := t.Delivery
	if d.Provider != "github" || !deliveryRepoPattern.MatchString(d.Repository) || strings.Contains(d.Repository, "..") || !deliveryAccountPattern.MatchString(d.Account) {
		return fmt.Errorf("%s：发布目的地必须包含 GitHub owner/repo 和登录账号", t.Name)
	}
	if t.Runner.Type != RunnerLocal || t.Steps.Publish != "" || t.Steps.Deploy != "" {
		return fmt.Errorf("%s：内置 GitHub 交付使用本地构建，不能同时配置上传或部署命令", t.Name)
	}
	if d.WorkflowPolicy != "dispatch-only" {
		return fmt.Errorf("%s：请先将安装包构建工作流迁移为显式调用，并设置 workflowPolicy=dispatch-only", t.Name)
	}
	if len(t.ArtifactRules) == 0 || len(t.Verification) == 0 {
		return fmt.Errorf("%s：GitHub 交付必须声明必需产物和验证命令（版本、平台及签名策略）", t.Name)
	}
	if d.Sync != nil {
		u, err := url.Parse(d.Sync.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(d.Sync.JSONPointer, "/") {
			return fmt.Errorf("%s：同步检查需要无凭证的 HTTPS 地址和 JSON Pointer", t.Name)
		}
	}
	if d.Deployment != nil {
		name := d.Deployment.Workflow
		if d.Sync == nil {
			return fmt.Errorf("%s：服务器部署需要线上版本检查地址", t.Name)
		}
		if d.Deployment.Strategy == "server-pull" {
			if name != "" {
				return fmt.Errorf("%s：服务器拉取更新不能同时触发云端部署工作流", t.Name)
			}
		} else if (d.Deployment.Strategy != "" && d.Deployment.Strategy != "workflow") || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*\.ya?ml$`).MatchString(name) {
			return fmt.Errorf("%s：服务器部署方式必须为 server-pull 或有效工作流", t.Name)
		}
	}
	return nil
}
