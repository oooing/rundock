package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workflows rejects tag/release-triggered work before any Git remote mutation.
// Parsing YAML is necessary: regex cannot safely interpret inline event lists,
// quoted keys or aliases. Ordinary branch-only CI is allowed.
func Workflows(root string) (map[string]string, error) {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml")) {
			continue
		}
		p := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(p)
		if err != nil || !info.Mode().IsRegular() {
			return nil, failure("workflow_unverified", "工作流文件不可读取")
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if err = validateWorkflow(raw); err != nil {
			return nil, failure("duplicate_cloud_build", entry.Name()+"："+err.Error())
		}
		sum := sha256.Sum256([]byte(strings.ReplaceAll(string(raw), "\r\n", "\n")))
		out[entry.Name()] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

func validateWorkflow(raw []byte) error {
	if len(raw) > 1<<20 {
		return failure("workflow_unverified", "工作流超过检查大小限制")
	}
	var doc map[string]any
	if yaml.Unmarshal(raw, &doc) != nil {
		return failure("workflow_unverified", "工作流 YAML 无法验证")
	}
	events := map[string]any{}
	switch on := doc["on"].(type) {
	case string:
		events[on] = nil
	case []any:
		for _, v := range on {
			s, ok := v.(string)
			if !ok {
				return failure("workflow_unverified", "工作流事件声明无效")
			}
			events[s] = nil
		}
	case map[string]any:
		events = on
	default:
		return failure("workflow_unverified", "缺少明确的工作流事件声明")
	}
	for name, options := range events {
		switch name {
		case "release", "create", "workflow_run", "registry_package", "repository_dispatch":
			return failure("duplicate_cloud_build", "存在发布事件触发入口，请迁移为显式调用")
		case "push":
			filters, ok := options.(map[string]any)
			if !ok {
				return failure("duplicate_cloud_build", "push 未限制为分支，可能重复构建")
			}
			_, tags := filters["tags"]
			_, ignored := filters["tags-ignore"]
			_, branches := filters["branches"]
			_, branchesIgnored := filters["branches-ignore"]
			if tags || ignored || (!branches && !branchesIgnored) {
				return failure("duplicate_cloud_build", "仍存在 Tag 构建入口")
			}
		}
	}
	return nil
}

func (e *Engine) CheckRemoteWorkflows(ctx context.Context, b Batch) error {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := e.Client.JSON(ctx, "GET", "repos/"+b.Repository, nil, &repo); err != nil {
		return err
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	base := "repos/" + b.Repository + "/contents/.github/workflows"
	err := e.Client.JSON(ctx, "GET", base+"?ref="+url.QueryEscape(repo.DefaultBranch), nil, &entries)
	if isStatus(err, 404) && len(b.Workflows) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Type != "file" || !(strings.HasSuffix(entry.Name, ".yml") || strings.HasSuffix(entry.Name, ".yaml")) {
			continue
		}
		var file struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		if err = e.Client.JSON(ctx, "GET", base+"/"+url.PathEscape(entry.Name)+"?ref="+url.QueryEscape(repo.DefaultBranch), nil, &file); err != nil {
			return err
		}
		raw, decodeErr := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
		if decodeErr != nil || file.Encoding != "base64" {
			return failure("workflow_unverified", "无法核对远端工作流")
		}
		if err = validateWorkflow(raw); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(strings.ReplaceAll(string(raw), "\r\n", "\n")))
		if b.Workflows[entry.Name] != hex.EncodeToString(sum[:]) {
			return failure("workflow_changed", "本地与远端默认分支的工作流不一致；请先完成工作流迁移")
		}
		seen[entry.Name] = true
	}
	if len(seen) != len(b.Workflows) {
		return failure("workflow_changed", "远端尚未包含本次核验的全部工作流")
	}
	return nil
}
