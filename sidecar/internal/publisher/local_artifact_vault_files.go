package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var localIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var localDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

const localManifestLimit = 8 * 1024 * 1024

func validLocalDigest(value string) bool { return localDigestPattern.MatchString(value) }

func validLocalArtifactName(name string) bool {
	if name == "" || len(name) > 1024 || strings.ContainsAny(name, "\\<>:\"|?*\x00") || filepath.IsAbs(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part {
			return false
		}
		for _, char := range part {
			if char < 32 {
				return false
			}
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func (s *Service) localArtifactTargetDirectory(runID, targetID string) (string, error) {
	if !localIdentityPattern.MatchString(runID) || !localIdentityPattern.MatchString(targetID) || !validLocalArtifactName(runID) || !validLocalArtifactName(targetID) {
		return "", &Error{Code: "local_artifact_identity_invalid", Message: "本地构建产物标识无效"}
	}
	return filepath.Join(s.store.ReleaseDataDir(), "local-builds", runID, targetID), nil
}

// The configured instance data directory is the trust boundary. Every vault
// component below it, including its releases root, must be a real directory.
func ensureLocalDirectory(root, relative string) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if _, err := checkedLocalPath(root, ".", true); err != nil {
		return err
	}
	if relative == "." || relative == "" {
		return nil
	}
	if !validLocalArtifactName(filepath.ToSlash(relative)) {
		return errors.New("本地产物目录路径无效")
	}
	current := root
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil || isPathLink(info) || !info.IsDir() {
			return errors.New("本地产物目录不能经过符号链接或目录联接")
		}
	}
	return nil
}

func checkedLocalPath(root, relative string, directory bool) (string, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || isPathLink(rootInfo) || !rootInfo.IsDir() {
		return "", errors.New("本地产物目录不存在或已被替换")
	}
	path, err := secureProjectPath(root, relative, directory)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || isPathLink(info) || (!directory && !info.Mode().IsRegular()) {
		return "", errors.New("本地产物必须是普通文件，不能经过目录链接")
	}
	return path, nil
}

func openCheckedLocalFile(root, relative string) (*os.File, error) {
	path, err := checkedLocalPath(root, relative, false)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	file, err := openLocalArtifactReadOnly(path)
	if err != nil {
		return nil, err
	}
	after, statErr := file.Stat()
	_, pathErr := checkedLocalPath(root, relative, false)
	latest, latestErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || latestErr != nil || !after.Mode().IsRegular() || isPathLink(after) || !os.SameFile(before, after) || !os.SameFile(latest, after) {
		file.Close()
		return nil, errors.New("本地产物路径在读取期间发生变化")
	}
	return file, nil
}

func verifyLocalFile(ctx context.Context, root string, value localArtifactFile) (*os.File, error) {
	file, err := openCheckedLocalFile(root, filepath.Join("files", filepath.FromSlash(value.Name)))
	if err != nil {
		return nil, &Error{Code: "local_artifact_missing", Message: "保存的产物不存在或路径无效：" + value.Name}
	}
	info, statErr := file.Stat()
	hash := sha256.New()
	n, readErr := io.Copy(hash, &localArtifactContextReader{ctx: ctx, reader: file})
	if statErr != nil || info.Size() != value.SizeBytes || n != value.SizeBytes || readErr != nil || hex.EncodeToString(hash.Sum(nil)) != value.SHA256 {
		file.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Code: "local_artifact_changed", Message: "保存的产物校验失败：" + value.Name}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func copyLocalArtifact(ctx context.Context, sourceRoot, sourceRelative, destinationRoot string, value localArtifactFile) error {
	in, err := openCheckedLocalFile(sourceRoot, sourceRelative)
	if err != nil {
		return err
	}
	defer in.Close()
	relative := filepath.Join("files", filepath.FromSlash(value.Name))
	if err := ensureLocalDirectory(destinationRoot, filepath.Dir(relative)); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(destinationRoot, relative), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, hash), &localArtifactContextReader{ctx: ctx, reader: in})
	syncErr, closeErr := out.Sync(), out.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != value.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != value.SHA256 {
		return &Error{Code: "local_artifact_changed", Message: "保存期间产物发生变化：" + value.Name}
	}
	return nil
}

func writeLocalManifest(dir string, raw []byte) error {
	if len(raw) > localManifestLimit {
		return errors.New("本地产物清单过大，请缩小匹配范围")
	}
	file, err := os.OpenFile(filepath.Join(dir, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func readLocalManifest(dir string) ([]byte, error) {
	file, err := openCheckedLocalFile(dir, "manifest.json")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, localManifestLimit+1))
	if err != nil || len(raw) > localManifestLimit {
		return nil, errors.New("本地产物清单不可读取")
	}
	return raw, nil
}

type localArtifactContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *localArtifactContextReader) Read(value []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(value)
}
