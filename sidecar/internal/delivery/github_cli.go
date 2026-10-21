package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/netproxy"
)

// CLI delegates credential access to gh's OS keyring. Tokens are never read,
// returned, added to argv, or forwarded to project commands.
type CLI struct{}

func cliCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		if home, e := os.UserHomeDir(); e == nil {
			candidate := filepath.Join(home, ".local", "bin", "gh.exe")
			if _, e = os.Stat(candidate); e == nil {
				path = candidate
				err = nil
			}
		}
	}
	if err != nil {
		return nil, failure("github_cli_missing", "请安装 GitHub CLI，并将指定账号登录到系统凭证存储")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	for _, v := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
		if key == "GH_TOKEN" || key == "GITHUB_TOKEN" || key == "GH_ENTERPRISE_TOKEN" || key == "GITHUB_ENTERPRISE_TOKEN" {
			continue
		}
		cmd.Env = append(cmd.Env, v)
	}
	cmd.Env = append(cmd.Env, "GH_PROMPT_DISABLED=1", "GH_PAGER=cat", "NO_COLOR=1")
	if githubNetworkCommand(args) {
		cmd.Env, err = netproxy.PrepareEnvironment(ctx, cmd.Env, apiDestination(args))
		if err != nil {
			return nil, err
		}
	}
	return cmd, nil
}

func githubNetworkCommand(args []string) bool {
	return len(args) > 0 && (args[0] == "api" || len(args) > 1 && args[0] == "auth" && args[1] == "status")
}

func apiDestination(args []string) string {
	for _, arg := range args {
		u, err := url.Parse(arg)
		if err == nil && u.Scheme == "https" && (u.Host == "api.github.com" || u.Host == "uploads.github.com") {
			return "https://" + u.Host + "/"
		}
	}
	return "https://api.github.com/"
}

func (c CLI) Identity(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := readRetry(ctx, "GET", func() error { return githubAuthAttempt(ctx) }, githubRetryDelay); err != nil {
		return "", err
	}
	var who struct {
		Login string `json:"login"`
	}
	if err := c.JSON(ctx, "GET", "user", nil, &who); err != nil {
		return "", err
	}
	if who.Login == "" {
		return "", failure("github_response_invalid", "GitHub 返回的账号信息不完整，请稍后重试")
	}
	return who.Login, nil
}

func githubAuthAttempt(ctx context.Context) error {
	// Leave time for one bounded retry and the authenticated /user read.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd, err := cliCommand(ctx, "auth", "status", "--active", "--hostname", "github.com")
	if err != nil {
		return err
	}
	var out cappedWriter
	out.max = 64 << 10
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	return decodeGitHubAuthStatus(ctx, err, &out)
}

// auth status performs network I/O too. Nonzero exit alone does NOT prove an
// expired credential. Never return its raw output, which may contain secrets.
func decodeGitHubAuthStatus(ctx context.Context, runErr error, out *cappedWriter) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return githubTransportError("timeout")
	}
	if runErr != nil {
		transport := githubTransportError(out.String())
		code, _ := ErrorInfo(transport)
		if code != "github_request_unconfirmed" {
			return transport
		}
		text := strings.ToLower(out.String())
		if strings.Contains(text, "not logged into") || strings.Contains(text, "not logged in to") ||
			strings.Contains(text, "token") && strings.Contains(text, "is invalid") ||
			strings.Contains(text, "http 401") || strings.Contains(text, "bad credentials") {
			return failure("github_auth_required", "GitHub 未登录或凭证已失效，请重新登录发布账号")
		}
		return transport
	}
	if out.overflow {
		return failure("github_response_invalid", "GitHub 返回的账号信息不完整，请稍后重试")
	}
	if !strings.Contains(out.String(), "(keyring)") {
		return failure("credential_store_required", "GitHub 凭证必须保存在系统密钥环；当前登录来源未通过检查")
	}
	return nil
}

func githubRetryDelay(ctx context.Context) error {
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c CLI) JSON(ctx context.Context, method, path string, body any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if strings.Contains(path, "://") || strings.HasPrefix(path, "/") {
		return failure("invalid_github_endpoint", "GitHub API 路径无效")
	}
	args := []string{"api", "--hostname", "github.com", "--include", "--method", method, "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28", path}
	var input []byte
	var err error
	if body != nil {
		input, err = json.Marshal(body)
		if err != nil {
			return err
		}
		args = append(args, "--input", "-")
	}
	err = readRetry(ctx, method, func() error {
		return githubJSONAttempt(ctx, args, input, out)
	}, githubRetryDelay)
	if err != nil {
		return fmt.Errorf("%s：%w", githubOperation(method, path), err)
	}
	return nil
}

func githubOperation(method, path string) string {
	if method != "GET" && method != "HEAD" {
		return "GitHub 操作结果未确认，继续前将核对远端状态"
	}
	if path == "user" {
		return "读取 GitHub 账号失败"
	}
	if strings.Contains(path, "/contents/.github/workflows") {
		return "读取 GitHub 自动发布配置失败"
	}
	if strings.Contains(path, "/releases") {
		return "读取 GitHub 版本信息失败"
	}
	return "读取 GitHub 仓库信息失败"
}

func githubJSONAttempt(ctx context.Context, args []string, input []byte, out any) error {
	cmd, err := cliCommand(ctx, args...)
	if err != nil {
		return err
	}
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var result, stderr cappedWriter
	result.max = 16 << 20
	stderr.max = 16 << 10
	cmd.Stdout = &result
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	return decodeGitHubResponse(ctx, runErr, &result, stderr.String(), out)
}

// Classify stderr into fixed messages; never persist raw CLI output, URLs or tokens.
func decodeGitHubResponse(ctx context.Context, runErr error, result *cappedWriter, stderr string, out any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	data := result.Bytes()
	head, raw, ok := bytes.Cut(data, []byte("\r\n\r\n"))
	if !ok {
		head, raw, ok = bytes.Cut(data, []byte("\n\n"))
	}
	fields := strings.Fields(string(head))
	status := 0
	if len(fields) > 1 {
		status, _ = strconv.Atoi(fields[1])
	}
	if status >= 400 {
		return &HTTPError{status}
	}
	if runErr != nil {
		return githubTransportError(stderr)
	}
	if !ok || status < 200 || status >= 300 || result.overflow {
		return failure("github_response_invalid", "GitHub 返回的信息不完整，请稍后重试")
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return failure("github_response_invalid", "GitHub 返回了无法识别的响应")
		}
	}
	return nil
}

func githubTransportError(stderr string) error {
	s := strings.ToLower(stderr)
	contains := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(s, part) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("x509", "certificate"):
		return failure("github_certificate_invalid", "GitHub 安全证书无法验证，请检查系统时间或代理证书后重试")
	case contains("timeout", "timed out", "deadline exceeded"):
		return failure("github_connection_timeout", "连接 GitHub 超时，请确认网络或代理可用后重试")
	case contains("no such host", "could not resolve"):
		return failure("github_dns_failed", "无法找到 GitHub 服务器，请检查网络或代理后重试")
	case contains("connection refused", "actively refused", "proxyconnect", "connectex"):
		return failure("github_connection_failed", "无法连接 GitHub 或代理，请确认网络或代理已开启后重试")
	case contains("eof", "connection reset", "stream error", "goaway", "broken pipe"):
		return failure("github_connection_interrupted", "读取 GitHub 信息时连接中断，请稍后重试")
	default:
		return failure("github_request_unconfirmed", "未收到 GitHub 的完整响应，请确认连接后重试")
	}
}

// Only repeat transient READ failures, at most once within the original deadline.
// Writes must be reconciled by the delivery engine to avoid duplicate releases.
func readRetry(ctx context.Context, method string, attempt func() error, wait func(context.Context) error) error {
	err := attempt()
	if err == nil || ctx.Err() != nil || (method != "GET" && method != "HEAD") || !transientGitHubRead(err) {
		return err
	}
	if err = wait(ctx); err != nil {
		return err
	}
	return attempt()
}

func transientGitHubRead(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 500 || httpErr.Status == 502 || httpErr.Status == 503 || httpErr.Status == 504
	}
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code {
	case "github_connection_timeout", "github_dns_failed", "github_connection_failed", "github_connection_interrupted":
		return true
	}
	return false
}

func (c CLI) Upload(ctx context.Context, repo string, id int64, path, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	u := "https://uploads.github.com/repos/" + repo + "/releases/" + strconv.FormatInt(id, 10) + "/assets?name=" + url.QueryEscape(name)
	cmd, err := cliCommand(ctx, "api", "--hostname", "github.com", "--method", "POST", "-H", "Content-Type: application/octet-stream", u, "--input", path)
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure("upload_unconfirmed", "文件上传结果尚未确认；继续时将先核对远端文件")
	}
	return nil
}

func (c CLI) Digest(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	cmd, err := cliCommand(ctx, "api", "--hostname", "github.com", "-H", "Accept: application/octet-stream", path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	cmd.Stdout = hash
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return "", failure("asset_verification_unconfirmed", "远端文件内容尚未核验")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type cappedWriter struct {
	bytes.Buffer
	max      int
	overflow bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.max - w.Len()
	if remaining < 0 {
		remaining = 0
	}
	if len(p) > remaining {
		p = p[:remaining]
		w.overflow = true
	}
	_, err := w.Buffer.Write(p)
	return n, err
}

func releasePath(repo string, id int64) string { return fmt.Sprintf("repos/%s/releases/%d", repo, id) }
