package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	return cmd, nil
}

func (c CLI) Identity(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := cliCommand(ctx, "auth", "status", "--active", "--hostname", "github.com")
	if err != nil {
		return "", err
	}
	var out cappedWriter
	out.max = 64 << 10
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err = cmd.Run(); err != nil {
		return "", failure("github_auth_required", "GitHub 登录不可用，请重新登录指定账号")
	}
	if !strings.Contains(out.String(), "(keyring)") {
		return "", failure("credential_store_required", "GitHub 凭证必须保存在系统密钥环；当前登录来源未通过检查")
	}
	var who struct {
		Login string `json:"login"`
	}
	if err = c.JSON(ctx, "GET", "user", nil, &who); err != nil {
		return "", err
	}
	return who.Login, nil
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
	cmd, err := cliCommand(ctx, args...)
	if err != nil {
		return err
	}
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var result cappedWriter
	result.max = 16 << 20
	cmd.Stdout = &result
	cmd.Stderr = io.Discard
	runErr := cmd.Run()
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
	if runErr != nil || !ok || status < 200 || status >= 300 || result.overflow {
		return failure("github_request_unconfirmed", "GitHub 请求结果尚未确认，请核对后重试")
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err = json.Unmarshal(raw, out); err != nil {
			return failure("github_response_invalid", "GitHub 返回了无法识别的响应")
		}
	}
	return nil
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
