package publisher

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// The supported Windows desktop supplies no-delete-sharing directory handles.
// Keep the data root, run ancestors and every output directory stable through
// copy, verification and metadata publication. Releasing a lease only closes
// handles; it never removes or renames an incomplete diagnostic directory.
type localArtifactDirectoryLease struct {
	paths   map[string]bool
	closers []func()
}

func lockLocalArtifactVault(root, runID string) (*localArtifactDirectoryLease, error) {
	lease := &localArtifactDirectoryLease{paths: map[string]bool{}}
	for _, path := range []string{root, filepath.Join(root, "local-builds"), filepath.Join(root, "local-builds", runID)} {
		if err := lease.hold(path); err != nil {
			lease.release()
			return nil, err
		}
	}
	return lease, nil
}

func (l *localArtifactDirectoryLease) hold(path string) error {
	path = filepath.Clean(path)
	if l.paths[path] {
		return nil
	}
	if len(l.closers) >= 100000 {
		return errors.New("本地产物目录过多，未登记产物")
	}
	close, err := lockLocalArtifactDirectory(path)
	if err != nil {
		return err
	}
	l.paths[path] = true
	l.closers = append(l.closers, close)
	return nil
}

func (l *localArtifactDirectoryLease) holdTree(ctx context.Context, root string) error {
	count := 0
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > 100000 {
			return errors.New("本地产物目录条目过多，未登记产物")
		}
		info, err := os.Lstat(path)
		if err != nil || isPathLink(info) || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("本地产物目录不能包含链接或非普通文件")
		}
		if info.IsDir() {
			return l.hold(path)
		}
		return nil
	})
}

func (l *localArtifactDirectoryLease) release() {
	for i := len(l.closers) - 1; i >= 0; i-- {
		l.closers[i]()
	}
	l.closers = nil
}

func (l *localArtifactDirectoryLease) verifyAndHoldManifest(dir string, expected []byte) error {
	file, err := openCheckedLocalFile(dir, "manifest.json")
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(file, localManifestLimit+1))
	if err != nil || !bytes.Equal(raw, expected) {
		file.Close()
		return &Error{Code: "local_manifest_invalid", Message: "保存的产物清单在登记前发生变化，未登记产物"}
	}
	l.closers = append(l.closers, func() { _ = file.Close() })
	return nil
}

func (l *localArtifactDirectoryLease) verifyAndHoldFiles(ctx context.Context, dir string, manifest localArtifactManifest) error {
	for _, value := range manifest.Files {
		file, err := verifyLocalFile(ctx, dir, value)
		if err != nil {
			return err
		}
		// Windows disallows writes and deletion for the lifetime of this handle.
		// Keep the exact verified bytes pinned until their DB visibility commit.
		l.closers = append(l.closers, func() { _ = file.Close() })
	}
	return nil
}
