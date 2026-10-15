// Package delivery owns immutable artifacts and GitHub delivery, independent of
// working trees, candidate objects and arbitrary project build commands.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/launcher-sidecar/internal/netproxy"
	"github.com/launcher-sidecar/internal/store"
)

type File struct {
	TargetID string `json:"targetId"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type Batch struct {
	SchemaVersion      int               `json:"schemaVersion"`
	RunID              string            `json:"runId"`
	AppID              string            `json:"appId"`
	GroupID            string            `json:"groupId"`
	Repository         string            `json:"repository"`
	Account            string            `json:"account"`
	Commit             string            `json:"commit"`
	Tag                string            `json:"tag"`
	Version            string            `json:"version"`
	Notes              string            `json:"notes"`
	Prerelease         bool              `json:"prerelease"`
	MakeLatest         bool              `json:"makeLatest"`
	SyncURL            string            `json:"syncUrl,omitempty"`
	SyncPointer        string            `json:"syncPointer,omitempty"`
	DeploymentWorkflow string            `json:"deploymentWorkflow,omitempty"`
	DeploymentStrategy string            `json:"deploymentStrategy,omitempty"`
	Workflows          map[string]string `json:"workflows"`
	Files              []File            `json:"files"`
}

type Source struct{ TargetID, Path, SHA256 string }

// GitHub is the external boundary for the CLI and hermetic end-to-end harness.
// Production never accepts an API base URL from project configuration.
type GitHub interface {
	Identity(context.Context) (string, error)
	JSON(context.Context, string, string, any, any) error
	Upload(context.Context, string, int64, string, string) error
	Digest(context.Context, string) (string, error)
}

type Engine struct {
	Store  *store.Store
	Client GitHub
}

func New(st *store.Store) *Engine { return &Engine{Store: st, Client: CLI{}} }

type Error struct{ Code, Message string }

func (e *Error) Error() string           { return e.Message }
func failure(code, message string) error { return &Error{code, message} }
func ErrorInfo(err error) (string, string) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, err.Error()
	}
	var proxyError *netproxy.UnavailableError
	if errors.As(err, &proxyError) {
		return "github_proxy_unavailable", proxyError.Error()
	}
	if errors.Is(err, context.Canceled) {
		return "delivery_cancelled", "交付已取消；可核对远端状态后继续"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "github_connection_timeout", "连接 GitHub 超时，请确认网络或代理可用后重试"
	}
	var httpError *HTTPError
	if errors.As(err, &httpError) {
		return fmt.Sprintf("github_http_%d", httpError.Status), err.Error()
	}
	return "delivery_unconfirmed", "交付结果尚未确认，请检查连接和账号后继续；系统会先核对远端状态"
}

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string {
	message := "GitHub 暂时不可用，请稍后重试"
	switch e.Status {
	case 401:
		message = "GitHub 登录已失效，请重新登录发布账号"
	case 403:
		message = "GitHub 拒绝访问，请检查账号权限或请求次数限制"
	case 404:
		message = "GitHub 未找到所需内容，请检查仓库地址及账号访问权限"
	case 409, 422:
		message = "GitHub 内容存在冲突，请核对版本和远端记录后重试"
	case 429:
		message = "GitHub 请求次数过多，请稍后重试"
	}
	return fmt.Sprintf("%s（HTTP %d）", message, e.Status)
}
func isStatus(err error, status int) bool {
	var e *HTTPError
	return errors.As(err, &e) && e.Status == status
}

var safePart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var safeAsset = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
