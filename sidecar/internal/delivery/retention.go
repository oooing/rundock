package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/launcher-sidecar/internal/store"
)

const RetainedSuccessfulVersions = 3
const PublishedArtifactLifetime = 30 * 24 * time.Hour

type RetentionCandidate struct{ RunID, GroupID, RepoRoot string }

// Both guards apply: keep the latest three successful versions of each stream,
// AND all copies younger than 30 days. Failed/unfinished/local-only runs never
// enter this list. No remote call, source-tree or build-cache traversal occurs.
func (e *Engine) RetentionCandidates(ctx context.Context, now time.Time) ([]RetentionCandidate, error) {
	ids, err := e.Store.SuccessfulDeliveryRunIDs()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var candidates []RetentionCandidate
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		run, err := e.Store.GetReleaseRun(id)
		if err != nil {
			return nil, err
		}
		if run == nil || run.Status != "succeeded" || run.FinishedAt == nil || run.Intent == "build-only" {
			continue
		}
		finished, err := time.Parse(time.RFC3339, *run.FinishedAt)
		if err != nil {
			continue
		}
		rows, err := e.Store.ReleaseDeliveries(id)
		if err != nil {
			return nil, err
		}
		if !retentionRunComplete(rows) {
			continue
		}
		batches, err := e.Load(id)
		if err != nil {
			continue
		} // Corrupt or legacy manifests are never deleted.
		allVerified := true
		for i, b := range batches {
			requiresSync := b.SyncURL != "" || b.DeploymentWorkflow != ""
			for _, component := range b.Components {
				requiresSync = requiresSync || component.SyncURL != ""
			}
			if requiresSync && rows[i].SyncState != "verified" {
				allVerified = false
			}
		}
		if !allVerified {
			continue
		}
		for i, b := range batches {
			if b.AppID != run.AppID || b.Commit != run.CommitSHA || b.Repository == "" || len(b.Files) == 0 {
				continue
			}
			key := retentionStream(b)
			counts[key]++
			if counts[key] <= RetainedSuccessfulVersions || now.Sub(finished) < PublishedArtifactLifetime || rows[i].LocalArtifactsState == "cleaned" {
				continue
			}
			candidates = append(candidates, RetentionCandidate{id, b.GroupID, run.RepoRoot})
		}
	}
	return candidates, nil
}

func retentionRunComplete(rows []*store.ReleaseDelivery) bool {
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if row.State != "published" || row.ReleaseID <= 0 || !strings.HasPrefix(row.URL, "https://github.com/") || row.ErrorCode != "" ||
			(row.SyncState != "unconfigured" && row.SyncState != "verified") {
			return false
		}
	}
	return true
}

func retentionStream(b Batch) string {
	// A Windows-only release must not evict the last Android/full-platform copy.
	targets := map[string]bool{}
	for _, f := range b.Files {
		targets[f.TargetID] = true
	}
	var ids []string
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	raw, _ := json.Marshal([]any{b.AppID, strings.ToLower(b.Repository), b.GroupID, b.Prerelease, ids})
	return string(raw)
}

// The publisher holds the repository reservation; this OS lock also excludes a
// second maintenance instance. Re-evaluate policy under the lock before removal.
func (e *Engine) PrunePublishedArtifacts(ctx context.Context, candidate RetentionCandidate, now time.Time) (bool, error) {
	unlock, err := Lock(e.Store.ReleaseDataDir(), "retention:"+candidate.RunID)
	if err != nil {
		return false, nil
	}
	defer unlock()
	candidates, err := e.RetentionCandidates(ctx, now)
	if err != nil {
		return false, err
	}
	eligible := false
	for _, c := range candidates {
		if c == candidate {
			eligible = true
			break
		}
	}
	if !eligible {
		return false, nil
	}
	rows, err := e.Store.ReleaseDeliveries(candidate.RunID)
	if err != nil {
		return false, err
	}
	batches, err := e.Load(candidate.RunID)
	if err != nil {
		return false, err
	}
	for i, b := range batches {
		if b.GroupID != candidate.GroupID {
			continue
		}
		dir, err := e.batchDir(b.RunID, b.GroupID)
		if err != nil {
			return false, err
		}
		// Validate every parent, manifest and payload BEFORE deleting any file.
		if err = plainRetentionDir(e.Store.ReleaseDataDir(), dir); err != nil {
			return false, err
		}
		manifestPath := filepath.Join(dir, "manifest.json")
		info, err := os.Lstat(manifestPath)
		if err != nil || !info.Mode().IsRegular() {
			return false, failure("retention_unsafe_path", "产物清单路径异常，已跳过自动清理")
		}
		raw, err := os.ReadFile(manifestPath)
		if err != nil || !bytes.Equal(raw, rows[i].Manifest) {
			return false, failure("manifest_corrupt", "产物清单不一致，已跳过自动清理")
		}
		paths := map[string]bool{}
		for _, f := range b.Files {
			if !hashPattern.MatchString(f.SHA256) {
				return false, failure("manifest_corrupt", "产物摘要无效，已跳过自动清理")
			}
			path := filepath.Join(dir, f.SHA256)
			if err = plainRetentionFile(path); err != nil {
				return false, err
			}
			paths[path] = true
		}
		if err = ctx.Err(); err != nil {
			return false, err
		}
		if err = e.Store.SetDeliveryStorageState(b.RunID, b.GroupID, "cleaning", ""); err != nil {
			return false, err
		}
		for path := range paths {
			if err = ctx.Err(); err != nil {
				return false, err
			}
			if err = plainRetentionDir(e.Store.ReleaseDataDir(), dir); err != nil {
				return false, err
			}
			if err = plainRetentionFile(path); err != nil {
				return false, err
			}
			// Deliberately non-recursive: ONLY immutable hash-named blobs in the
			// verified manifest. Keep manifest.json, unknown files and all records.
			if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
				return false, err
			}
		}
		if err = e.Store.SetDeliveryStorageState(b.RunID, b.GroupID, "cleaned", now.UTC().Format(time.RFC3339)); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func plainRetentionDir(root, dir string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return failure("retention_unsafe_path", "产物路径超出保存目录，已跳过自动清理")
	}
	for p := dir; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		resolved, resolveErr := filepath.EvalSymlinks(p)
		if err != nil || resolveErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Clean(p), filepath.Clean(resolved)) {
			return failure("retention_unsafe_path", "产物目录已变化，已跳过自动清理")
		}
		if strings.EqualFold(p, root) {
			break
		}
		if filepath.Dir(p) == p {
			return failure("retention_unsafe_path", "产物路径超出保存目录，已跳过自动清理")
		}
	}
	return nil
}

func plainRetentionFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	} // Resume after a crash or partial cleanup.
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return failure("retention_unsafe_path", "产物路径不是普通文件，已跳过自动清理")
	}
	return nil
}
