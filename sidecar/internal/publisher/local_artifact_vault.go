package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

// Local artifacts are a separate consumer of captured outputs. They do not
// enter the GitHub delivery vault or acquire any publication authority.
type LocalBuildArtifact struct {
	ID        string `json:"id"`
	TargetID  string `json:"targetId"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

type localArtifactFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

type localArtifactManifest struct {
	SchemaVersion int                 `json:"schemaVersion"`
	RunID         string              `json:"runId"`
	AppID         string              `json:"appId"`
	TargetID      string              `json:"targetId"`
	Files         []localArtifactFile `json:"files"`
}

func localArtifactID(runID, targetID, name string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + targetID + "\x00" + name))
	return hex.EncodeToString(sum[:])
}

// The caller supplies a run copy whose RepoRoot is the build snapshot. This
// captures final bytes only after project verification. The directory is owned
// exclusively; the immutable DB manifest makes it visible only after every
// file is flushed, closed and verified. Identical completion repairs a lost DB
// insert without replacing sealed bytes. Failed partial directories remain
// invisible and are retained for diagnosis rather than guessed safe to erase.
func (s *Service) sealLocalBuildArtifacts(ctx context.Context, run *store.ReleaseRun, target planTarget, workingDir string) (resultErr error) {
	defer func() {
		var typed *Error
		if resultErr != nil && !errors.As(resultErr, &typed) && !errors.Is(resultErr, context.Canceled) && !errors.Is(resultErr, context.DeadlineExceeded) {
			resultErr = &Error{Code: "local_artifact_save_failed", Message: "保存产物失败：" + redact(resultErr.Error())}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	authoritative, plan, err := s.localArtifactRun(run.ID)
	if err != nil {
		return err
	}
	if authoritative.AppID != run.AppID || !localArtifactTargetBelongs(plan, target.ID) {
		return &Error{Code: "local_artifact_identity_invalid", Message: "产物不属于此任务的本地构建目标"}
	}
	if err := s.captureTargetArtifacts(run, target, workingDir); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	files, sources, err := s.localArtifactSources(run, target, workingDir)
	if err != nil {
		return err
	}
	dir, err := s.localArtifactTargetDirectory(run.ID, target.ID)
	if err != nil {
		return err
	}
	if err := ensureLocalDirectory(s.store.ReleaseDataDir(), filepath.Join("local-builds", run.ID)); err != nil {
		return err
	}
	lease, err := lockLocalArtifactVault(s.store.ReleaseDataDir(), run.ID)
	if err != nil {
		return err
	}
	defer lease.release()
	manifest := localArtifactManifest{SchemaVersion: 1, RunID: run.ID, AppID: run.AppID, TargetID: target.ID, Files: files}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := s.writeLocalArtifactDirectory(ctx, authoritative, plan, run, dir, target.ID, raw, files, sources, lease); err != nil {
		return err
	}
	if err := lease.verifyAndHoldManifest(dir, raw); err != nil {
		return err
	}
	if err := lease.verifyAndHoldFiles(ctx, dir, manifest); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.store.SealLocalBuildArtifacts(run.ID, target.ID, raw); err == nil {
		return nil
	}
	// One bounded metadata retry repairs a transient DB failure. The first DB
	// insert is the API visibility commit; all files already passed verification.
	// Never rerun commands, recopy partial bytes or replace the saved directory.
	if err := lease.verifyAndHoldFiles(ctx, dir, manifest); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.store.SealLocalBuildArtifacts(run.ID, target.ID, raw)
}

func (s *Service) localArtifactSources(run *store.ReleaseRun, target planTarget, workingDir string) ([]localArtifactFile, []string, error) {
	artifacts, err := s.store.ReleaseArtifacts(run.ID)
	if err != nil {
		return nil, nil, err
	}
	matched := map[string]*store.ReleaseArtifact{}
	// artifactRules describe final packages. Intermediate artifacts remain in
	// capture metadata but do not crowd the standalone build's download list.
	if len(target.ArtifactRules) > 0 {
		for _, rule := range target.ArtifactRules {
			pattern := strings.NewReplacer("${VERSION}", run.TargetVersion, "${TAG}", run.TagName).Replace(rule.Pattern)
			count := 0
			for _, artifact := range artifacts {
				if artifact.TargetID != target.ID {
					continue
				}
				path, err := secureProjectPath(run.RepoRoot, artifact.Path, false)
				if err != nil {
					return nil, nil, err
				}
				rel, err := filepath.Rel(workingDir, path)
				if err != nil {
					return nil, nil, err
				}
				if globPattern(pattern).MatchString(filepath.ToSlash(rel)) {
					matched[artifact.Path] = artifact
					count++
				}
			}
			if count < rule.Min || count > rule.Max {
				return nil, nil, &Error{Code: "required_artifact_missing", Message: fmt.Sprintf("%s 必需产物 %s 需要 %d–%d 个，实际 %d 个", target.Name, pattern, rule.Min, rule.Max, count)}
			}
		}
	} else {
		for _, artifact := range artifacts {
			if artifact.TargetID == target.ID {
				matched[artifact.Path] = artifact
			}
		}
	}
	paths := make([]string, 0, len(matched))
	for path := range matched {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) == 0 || len(paths) > 2000 {
		return nil, nil, &Error{Code: "local_artifacts_missing", Message: "没有找到可保存的最终产物，请检查产物配置"}
	}
	files, sources := []localArtifactFile{}, []string{}
	names := map[string]bool{}
	for _, path := range paths {
		artifact := matched[path]
		source, err := secureProjectPath(run.RepoRoot, path, false)
		if err != nil {
			return nil, nil, err
		}
		rel, err := filepath.Rel(workingDir, source)
		name := filepath.ToSlash(rel)
		if err != nil || !validLocalArtifactName(name) || artifact.SizeBytes <= 0 || !validLocalDigest(artifact.SHA256) {
			return nil, nil, errors.New("产物必须是目标目录内的非空普通文件")
		}
		key := strings.ToLower(name)
		if names[key] {
			return nil, nil, errors.New("产物名称冲突：" + name)
		}
		names[key] = true
		files = append(files, localArtifactFile{ID: localArtifactID(run.ID, target.ID, name), Name: name, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256})
		sources = append(sources, path)
	}
	return files, sources, nil
}
