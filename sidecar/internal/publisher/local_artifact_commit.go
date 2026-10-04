package publisher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/launcher-sidecar/internal/store"
)

// Windows may deny directory rename while a scanner holds a legitimate read
// handle to a package. A stable, exclusively created directory avoids that
// operation entirely. Public APIs enumerate DB manifests, not disk directories;
// an incomplete copy therefore never supplies download IDs or an opener path.
func (s *Service) writeLocalArtifactDirectory(ctx context.Context, authoritative *store.ReleaseRun, plan *executionPlan, source *store.ReleaseRun, dir, targetID string, manifest []byte, files []localArtifactFile, sources []string, lease *localArtifactDirectoryLease) error {
	rows, err := s.store.LocalBuildArtifactManifests(authoritative.ID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.TargetID != targetID {
			continue
		}
		if !bytes.Equal(row.Manifest, manifest) {
			return localArtifactManifestConflict()
		}
		if err := lease.holdTree(ctx, dir); err != nil {
			return err
		}
		// Once published, a missing/replaced directory cannot be copied into:
		// its existing DB row would expose individual files during the copy.
		if _, _, err := s.loadLocalArtifactManifest(authoritative, plan, row); err != nil {
			return &Error{Code: "local_manifest_invalid", Message: "已登记产物目录或清单无效，未覆盖已有记录：" + redact(err.Error())}
		}
		return nil // The caller verifies all sealed bytes before any DB repair.
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if _, err := checkedLocalPath(s.store.ReleaseDataDir(), filepath.Join("local-builds", authoritative.ID, targetID), true); err != nil {
			return err
		}
		if err := lease.holdTree(ctx, dir); err != nil {
			return err
		}
		// Never append into a partial or foreign directory. Only identical,
		// completed bytes may repair a lost metadata insert.
		return reuseLocalArtifactManifest(dir, manifest)
	}
	if err := lease.hold(dir); err != nil {
		return err
	}
	// Create and lease all planned directories before copying any file. Their
	// delete-sharing leases remain held through hashing and the DB commit.
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ensureLocalDirectory(dir, filepath.Dir(filepath.Join("files", filepath.FromSlash(file.Name)))); err != nil {
			return err
		}
	}
	if err := lease.holdTree(ctx, dir); err != nil {
		return err
	}
	for i, file := range files {
		if _, err := checkedLocalPath(s.store.ReleaseDataDir(), filepath.Join("local-builds", authoritative.ID, targetID), true); err != nil {
			return err
		}
		if err := copyLocalArtifact(ctx, source.RepoRoot, sources[i], dir, file); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The manifest is written last, flushed and closed. Recovery additionally
	// requires captured metadata plus a hash check of every declared final file.
	return writeLocalManifest(dir, manifest)
}

func reuseLocalArtifactManifest(target string, manifest []byte) error {
	existing, err := readLocalManifest(target)
	if err != nil {
		return &Error{Code: "local_manifest_invalid", Message: "产物目录已存在但清单不完整，未覆盖；请开始新的构建：" + redact(err.Error())}
	}
	if !bytes.Equal(existing, manifest) {
		return localArtifactManifestConflict()
	}
	return nil
}

func localArtifactManifestConflict() error {
	return &Error{Code: "local_manifest_conflict", Message: "已保存产物与本次结果不同，请开始新的构建"}
}
