package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (e *Engine) batchDir(runID, groupID string) (string, error) {
	if !safePart.MatchString(runID) || !safePart.MatchString(groupID) {
		return "", failure("invalid_delivery_identity", "交付标识无效")
	}
	return filepath.Join(e.Store.ReleaseDataDir(), runID, groupID), nil
}

// Seal commits only complete bytes. A crash before rename leaves unreferenced
// pending files; a crash after rename can finish the idempotent DB insert.
func (e *Engine) Seal(ctx context.Context, b Batch, sources []Source) error {
	dir, err := e.batchDir(b.RunID, b.GroupID)
	if err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".pending-")
	if err != nil {
		return err
	}
	defer func() {
		if filepath.Dir(tmp) == parent && strings.HasPrefix(filepath.Base(tmp), ".pending-") {
			_ = os.RemoveAll(tmp)
		}
	}()
	b.SchemaVersion = 1
	b.Files = []File{}
	names := map[string]bool{}
	for _, source := range sources {
		if err = ctx.Err(); err != nil {
			return err
		}
		name := filepath.Base(source.Path)
		if !safeAsset.MatchString(name) || names[strings.ToLower(name)] {
			return failure("artifact_name_conflict", "产物文件名无效或重复："+name)
		}
		names[strings.ToLower(name)] = true
		info, err := os.Lstat(source.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return failure("artifact_invalid", "产物不是可读取的非空普通文件："+name)
		}
		in, err := os.Open(source.Path)
		if err != nil {
			return err
		}
		out, err := os.CreateTemp(tmp, "copy-")
		if err != nil {
			in.Close()
			return err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, hash), &contextReader{ctx, in})
		in.Close()
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return failure("artifact_changed", "封存过程中产物发生变化："+name)
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		if source.SHA256 != "" && source.SHA256 != digest {
			return failure("artifact_changed", "产物在验证后发生变化："+name)
		}
		if err = os.Rename(out.Name(), filepath.Join(tmp, digest)); err != nil {
			// Identical payloads with different public names share one blob.
			if _, statErr := os.Stat(filepath.Join(tmp, digest)); statErr != nil {
				return err
			}
			_ = os.Remove(out.Name())
		}
		b.Files = append(b.Files, File{source.TargetID, name, n, digest})
	}
	if len(b.Files) == 0 {
		return failure("artifacts_missing", "没有可发布的产物")
	}
	sort.Slice(b.Files, func(i, j int) bool { return b.Files[i].Name < b.Files[j].Name })
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	manifest, err := os.OpenFile(filepath.Join(tmp, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := manifest.Write(raw)
	syncErr := manifest.Sync()
	closeErr := manifest.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(tmp, dir); err != nil {
		existing, readErr := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if readErr != nil || string(existing) != string(raw) {
			return failure("sealed_manifest_conflict", "已封存产物与本次结果不同，请创建新的构建任务")
		}
	}
	if err = e.Verify(ctx, b); err != nil {
		return err
	}
	return e.Store.SealReleaseDelivery(b.RunID, b.GroupID, raw)
}

func (e *Engine) Load(runID string) ([]Batch, error) {
	rows, err := e.Store.ReleaseDeliveries(runID)
	if err != nil {
		return nil, err
	}
	out := []Batch{}
	for _, row := range rows {
		sum := sha256.Sum256(row.Manifest)
		if hex.EncodeToString(sum[:]) != row.ManifestSHA256 {
			return nil, failure("manifest_corrupt", "产物清单校验失败")
		}
		var b Batch
		if err = json.Unmarshal(row.Manifest, &b); err != nil || b.SchemaVersion != 1 || b.RunID != runID || b.GroupID != row.GroupID {
			return nil, failure("manifest_corrupt", "产物清单无效")
		}
		out = append(out, b)
	}
	return out, nil
}

func (e *Engine) FilePath(b Batch, f File) (string, error) {
	dir, err := e.batchDir(b.RunID, b.GroupID)
	if err != nil {
		return "", err
	}
	if !hashPattern.MatchString(f.SHA256) {
		return "", failure("manifest_corrupt", "产物摘要无效")
	}
	path := filepath.Join(dir, f.SHA256)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.EqualFold(filepath.Clean(path), filepath.Clean(resolved)) {
		return "", failure("artifact_changed", "保存的产物路径已变化")
	}
	return path, nil
}

func (e *Engine) Verify(ctx context.Context, b Batch) error {
	if len(b.Files) == 0 {
		return failure("artifacts_missing", "缺少封存产物")
	}
	for _, f := range b.Files {
		path, err := e.FilePath(b, f)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return failure("artifact_missing", "保存的产物已被删除："+f.Name)
		}
		info, statErr := file.Stat()
		hash := sha256.New()
		n, copyErr := io.Copy(hash, &contextReader{ctx, file})
		file.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if statErr != nil || !info.Mode().IsRegular() || copyErr != nil || n != f.Size || hex.EncodeToString(hash.Sum(nil)) != f.SHA256 {
			return failure("artifact_changed", "保存的产物校验失败："+f.Name)
		}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
