// Package delivery owns immutable artifacts and GitHub delivery, independent of
// working trees, candidate objects and arbitrary project build commands.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"github.com/launcher-sidecar/internal/store"
	"regexp"
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
		return e.Code, e.Message
	}
	if errors.Is(err, context.Canceled) {
		return "delivery_cancelled", "交付已取消；可核对远端状态后继续"
	}
	return "delivery_unconfirmed", "交付结果尚未确认，请检查连接和账号后继续；系统会先核对远端状态"
}

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("GitHub HTTP %d", e.Status) }
func isStatus(err error, status int) bool {
	var e *HTTPError
	return errors.As(err, &e) && e.Status == status
}

var safePart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var safeAsset = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,199}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
