package publisher

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const previewTextLimit = 64 * 1024
const previewImageLimit = 4 * 1024 * 1024

type FilePreview struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Text      string `json:"text,omitempty"`
	DataURL   string `json:"dataUrl,omitempty"`
	Truncated bool   `json:"truncated"`
	Message   string `json:"message,omitempty"`
}

func (s *Service) PreviewFile(ctx context.Context, appID, relative string) (*FilePreview, error) {
	a, err := s.store.GetApp(appID)
	if err != nil || a == nil {
		return nil, &Error{Code: "app_not_found", Message: "项目不存在"}
	}
	ctx, cancel := commandContext(ctx, 5*time.Second)
	defer cancel()
	root, err := s.git(ctx, a.Cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return nil, &Error{Code: "not_repository", Message: "项目目录不在 Git 仓库中"}
	}
	return readFilePreview(canonicalRepositoryPath(root), relative)
}

func readFilePreview(root, relative string) (*FilePreview, error) {
	// Do not expose Git internals, Windows alternate streams, absolute paths,
	// traversal, device files or symlink/reparse-point targets through this API.
	invalid := func() (*FilePreview, error) {
		return nil, &Error{Code: "preview_invalid_path", Message: "只能预览项目内的普通文件"}
	}
	if relative == "" || strings.ContainsAny(relative, ":\x00") {
		return invalid()
	}
	for _, part := range strings.Split(strings.ReplaceAll(relative, "\\", "/"), "/") {
		if part == ".." || strings.EqualFold(strings.TrimRight(part, " ."), ".git") {
			return invalid()
		}
	}
	abs, err := secureProjectPath(root, relative, false)
	if os.IsNotExist(err) {
		return nil, &Error{Code: "preview_not_found", Message: "文件已删除或不存在"}
	}
	if err != nil {
		return invalid()
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return invalid()
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, &Error{Code: "preview_unreadable", Message: "无法读取此文件"}
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return invalid()
	}
	result := &FilePreview{Path: relative, Size: opened.Size(), Kind: "unsupported", Message: "此文件仅显示信息，暂不支持内容预览"}
	ext := strings.ToLower(filepath.Ext(relative))
	imageExtension := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".bmp" || ext == ".ico"
	limit := previewTextLimit
	if imageExtension {
		if opened.Size() > previewImageLimit {
			result.Message = "图片超过 4 MB，暂不预览"
			return result, nil
		}
		limit = previewImageLimit
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	if err != nil {
		return nil, &Error{Code: "preview_unreadable", Message: "无法读取此文件"}
	}
	if imageExtension {
		mime := http.DetectContentType(raw)
		switch mime {
		case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/x-icon":
			if len(raw) > previewImageLimit {
				result.Message = "图片超过 4 MB，暂不预览"
				return result, nil
			}
			result.Kind = "image"
			result.Message = ""
			result.DataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
			return result, nil
		}
	}
	if len(raw) > previewTextLimit {
		result.Truncated = true
		raw = raw[:previewTextLimit]
		// A UTF-8 character may straddle the bounded read.
		for i := 0; i < 3 && len(raw) > 0 && !utf8.Valid(raw); i++ {
			raw = raw[:len(raw)-1]
		}
	}
	if !utf8.Valid(raw) {
		return result, nil
	}
	for _, b := range raw {
		if b < 32 && b != '\n' && b != '\r' && b != '\t' {
			return result, nil
		}
	}
	// HTML, SVG and Markdown deliberately remain plain text: no scripts, embeds
	// or remote resources are executed or loaded by the preview.
	result.Kind = "text"
	result.Message = ""
	result.Text = string(raw)
	return result, nil
}
