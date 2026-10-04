package publisher

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type recoveryRemoval struct {
	path   string
	info   fs.FileInfo
	unlock func()
}

// Never glob the system Temp directory. Only an authoritative recorded source
// path is eligible, while the task's repository OS lock proves it is inactive.
func cleanupRecordedLocalSnapshot(ctx context.Context, plan *localBuildPlan) error {
	if plan.SnapshotRoot == "" {
		return nil // A crash before recording provenance is not safe to guess.
	}
	root, err := filepath.Abs(plan.SnapshotRoot)
	temp, tempErr := filepath.Abs(os.TempDir())
	parent := filepath.Dir(root)
	if err != nil || tempErr != nil || !filepath.IsAbs(plan.SnapshotRoot) || !validLocalDigest(plan.SourceSHA256) || filepath.Base(root) != "source" || !strings.EqualFold(filepath.Dir(parent), temp) || !strings.HasPrefix(filepath.Base(parent), "rundock-local-build-") || !validLocalArtifactName(filepath.Base(parent)) {
		return errors.New("记录的源码目录不符合临时快照归属规则")
	}
	if _, err := os.Lstat(parent); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	unlockTemp, err := lockRecoveryDirectory(temp)
	if err != nil {
		return err
	}
	defer unlockTemp()
	removals := []*recoveryRemoval{}
	defer func() {
		for _, item := range removals {
			if item.unlock != nil {
				item.unlock()
			}
		}
	}()
	// Hold every directory against rename/delete before walking below it. Then
	// remove children one at a time: unlike RemoveAll, this never follows a
	// replacement reparse point. New entries make directory removal fail safely.
	err = filepath.WalkDir(parent, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil || isPathLink(info) || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("临时目录含有链接或非普通文件，未清理")
		}
		item := &recoveryRemoval{path: path, info: info}
		if info.IsDir() {
			item.unlock, err = lockRecoveryDirectory(path)
			if err != nil {
				return err
			}
		}
		removals = append(removals, item)
		if len(removals) > 100000 {
			return errors.New("临时目录条目过多，未清理")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(removals) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := removals[i]
		latest, err := os.Lstat(item.path)
		if err != nil || isPathLink(latest) || !os.SameFile(item.info, latest) {
			return errors.New("临时目录在清理期间被替换，已停止")
		}
		if item.unlock != nil {
			item.unlock()
			item.unlock = nil
		}
		if err := os.Remove(item.path); err != nil {
			return errors.New("临时文件无法删除，已保留尚未清理的目录")
		}
	}
	return nil
}
