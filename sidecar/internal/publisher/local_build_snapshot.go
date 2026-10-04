package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const localSnapshotFilesLimit = 100000
const localSnapshotBytesLimit int64 = 2 << 30

// This is source isolation, not a sandbox for arbitrary project commands. Only
// saved commands are executed; no dependencies are linked to the live project.
type localBuildSnapshot struct {
	Dir, Root string
	Files     map[string]string
	SHA256    string
}

func localSnapshotExcluded(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		switch strings.ToLower(part) {
		case ".git", "node_modules", ".cache", "cache", ".gradle", ".expo", ".next", ".turbo", ".tmp", "__pycache__", ".venv", "venv":
			return true
		}
	}
	return strings.HasPrefix(strings.ToLower(filepath.ToSlash(path)), ".launcher/diagnostics/")
}

func (s *Service) localSourceFiles(ctx context.Context, root string) ([]string, error) {
	out := []string{}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		gitCtx, cancel := commandContext(ctx, 30*time.Second)
		raw, gitErr := s.gitRaw(gitCtx, root, "-c", "core.fsmonitor=false", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		cancel()
		if gitErr != nil {
			return nil, &Error{Code: "local_snapshot_failed", Message: "无法只读列出源码文件；请检查 Git 是否可用"}
		}
		for _, rel := range strings.Split(raw, "\x00") {
			if rel != "" && !localSnapshotExcluded(rel) {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		// A saved portable configuration is an explicit build input even when
		// the project keeps .launcher out of Git. No other ignored files are added.
		if _, err := secureProjectPath(root, ".launcher/release.yaml", false); err == nil {
			out = append(out, ".launcher/release.yaml")
		}
	} else {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			if localSnapshotExcluded(rel) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := os.Lstat(path)
			if err != nil || isPathLink(info) {
				return fmt.Errorf("源码包含目录链接或无法读取的文件：%s", rel)
			}
			if !entry.IsDir() {
				out = append(out, filepath.ToSlash(rel))
			}
			if len(out) > localSnapshotFilesLimit {
				return fmt.Errorf("源码文件超过安全快照上限，请缩小项目目录")
			}
			return nil
		})
		if err != nil {
			return nil, &Error{Code: "local_snapshot_failed", Message: err.Error()}
		}
	}
	if len(out) > localSnapshotFilesLimit {
		return nil, &Error{Code: "local_snapshot_failed", Message: "源码文件超过安全快照上限，请缩小项目目录"}
	}
	sort.Strings(out)
	return dedupe(out), nil
}

func (s *Service) createLocalSnapshot(ctx context.Context, root string, outputPatterns []string) (*localBuildSnapshot, error) {
	files, err := s.localSourceFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "rundock-local-build-")
	if err != nil {
		return nil, &Error{Code: "local_snapshot_failed", Message: "无法创建独立构建目录"}
	}
	snapshot := &localBuildSnapshot{Dir: dir, Root: filepath.Join(dir, "source"), Files: map[string]string{}}
	if err = os.Mkdir(snapshot.Root, 0700); err != nil {
		snapshot.cleanup()
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			snapshot.cleanup()
		}
	}()
	var total int64
	for _, rel := range files {
		if localSnapshotExcluded(rel) || matchesAnyArtifact(rel, outputPatterns) {
			continue // Old declared outputs are never accepted as a new build result.
		}
		path, pathErr := secureProjectPath(root, rel, false)
		if os.IsNotExist(pathErr) {
			continue // A tracked deletion is part of the current working state.
		}
		if pathErr != nil || strings.Contains(rel, ":") {
			return nil, &Error{Code: "local_snapshot_failed", Message: "源码路径无效或经过链接：" + rel}
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || isPathLink(info) {
			return nil, &Error{Code: "local_snapshot_failed", Message: "源码包含非普通文件或子模块：" + rel}
		}
		total += info.Size()
		if total > localSnapshotBytesLimit {
			return nil, &Error{Code: "local_snapshot_failed", Message: "源码超过 2 GB 安全快照上限，请移除构建缓存或缩小项目目录"}
		}
		destination := filepath.Join(snapshot.Root, filepath.FromSlash(rel))
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return nil, err
		}
		hash, err := copyLocalSource(ctx, path, destination, info.Mode().Perm())
		if err != nil {
			return nil, &Error{Code: "local_snapshot_failed", Message: "复制源码失败：" + rel}
		}
		snapshot.Files[rel] = hash
	}
	current, err := s.localSourceFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, rel := range current {
		if localSnapshotExcluded(rel) || matchesAnyArtifact(rel, outputPatterns) {
			continue
		}
		path, err := secureProjectPath(root, rel, false)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, &Error{Code: "local_snapshot_changed", Message: "复制期间源码路径发生变化，请重新构建"}
		}
		digest, err := localSourceHash(ctx, path)
		if err != nil || snapshot.Files[rel] != digest {
			return nil, &Error{Code: "local_snapshot_changed", Message: "复制期间源码发生变化，请重新构建"}
		}
	}
	// Include paths as well as bytes. A file disappearing during the copy is stale.
	for rel, digest := range snapshot.Files {
		path, err := secureProjectPath(root, rel, false)
		if err != nil {
			return nil, &Error{Code: "local_snapshot_changed", Message: "复制期间源码文件消失，请重新构建"}
		}
		actual, err := localSourceHash(ctx, path)
		if err != nil || actual != digest {
			return nil, &Error{Code: "local_snapshot_changed", Message: "复制期间源码发生变化，请重新构建"}
		}
	}
	paths := make([]string, 0, len(snapshot.Files))
	for rel := range snapshot.Files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, rel := range paths {
		fmt.Fprintf(hash, "%s\x00%s\x00", rel, snapshot.Files[rel])
	}
	snapshot.SHA256 = hex.EncodeToString(hash.Sum(nil))
	failed = false
	return snapshot, nil
}

type localContextReader struct {
	ctx context.Context
	in  io.Reader
}

func (r localContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.in.Read(p)
}

func copyLocalSource(ctx context.Context, source, destination string, mode fs.FileMode) (string, error) {
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() || isPathLink(before) {
		return "", fmt.Errorf("源码文件类型已变化")
	}
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", fmt.Errorf("源码在打开期间已被替换")
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), localContextReader{ctx, in})
	closeErr := out.Close()
	if copyErr != nil {
		return "", copyErr
	}
	after, err := os.Lstat(source)
	if err != nil || isPathLink(after) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("源码在复制期间已变化")
	}
	return hex.EncodeToString(hash.Sum(nil)), closeErr
}

func localSourceHash(ctx context.Context, path string) (string, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || isPathLink(before) {
		return "", fmt.Errorf("源码文件类型已变化")
	}
	in, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", fmt.Errorf("源码在打开期间已被替换")
	}
	hash := sha256.New()
	_, err = io.Copy(hash, localContextReader{ctx, in})
	if err != nil {
		return "", err
	}
	after, err := os.Lstat(path)
	if err != nil || isPathLink(after) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return "", fmt.Errorf("源码在读取期间已变化")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (snapshot *localBuildSnapshot) cleanup() {
	if snapshot == nil || snapshot.Dir == "" {
		return
	}
	abs, err := filepath.Abs(snapshot.Dir)
	temp, tempErr := filepath.Abs(os.TempDir())
	if err == nil && tempErr == nil && strings.EqualFold(filepath.Dir(abs), temp) && strings.HasPrefix(filepath.Base(abs), "rundock-local-build-") {
		_ = os.RemoveAll(abs)
	}
}
