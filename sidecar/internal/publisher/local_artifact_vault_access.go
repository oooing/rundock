package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/launcher-sidecar/internal/store"
)

func (s *Service) localArtifactRun(runID string) (*store.ReleaseRun, *executionPlan, error) {
	if !localIdentityPattern.MatchString(runID) {
		return nil, nil, &Error{Code: "local_build_not_found", Message: "本地构建记录不存在"}
	}
	run, err := s.store.GetReleaseRun(runID)
	if err != nil {
		return nil, nil, err
	}
	if run == nil {
		return nil, nil, &Error{Code: "local_build_not_found", Message: "构建记录不存在"}
	}
	plan, err := parseExecutionPlan(run.ExecutionPlan)
	if err != nil {
		return nil, nil, err
	}
	app, err := s.store.GetApp(run.AppID)
	if err != nil {
		return nil, nil, err
	}
	if app == nil {
		return nil, nil, &Error{Code: "app_not_found", Message: "产物所属项目不存在"}
	}
	return run, plan, nil
}

// The standalone API validates the local-only intent separately. Sealed local
// artifacts may also belong to a formal release that selected no publication.
func (s *Service) CheckLocalBuildRun(runID string) (*store.ReleaseRun, error) {
	run, _, err := s.localArtifactRun(runID)
	if err != nil {
		return nil, err
	}
	if _, err := parseLocalBuildPlan(run); err != nil {
		return nil, err
	}
	return run, nil
}

func localArtifactTargetBelongs(plan *executionPlan, targetID string) bool {
	for _, target := range plan.Targets {
		if target.ID == targetID && strings.EqualFold(strings.TrimSpace(target.Runner.Type), "local") && !target.Selection.Publish && !target.Selection.Deploy {
			return true
		}
	}
	return false
}

func (s *Service) loadLocalArtifactManifest(run *store.ReleaseRun, plan *executionPlan, row *store.LocalBuildArtifacts) (localArtifactManifest, string, error) {
	manifest := localArtifactManifest{}
	if row.RunID != run.ID || !localArtifactTargetBelongs(plan, row.TargetID) || len(row.Manifest) > localManifestLimit || !validLocalDigest(row.ManifestSHA256) {
		return manifest, "", errors.New("本地产物清单所属任务无效")
	}
	sum := sha256.Sum256(row.Manifest)
	if hex.EncodeToString(sum[:]) != row.ManifestSHA256 {
		return manifest, "", errors.New("保存的本地产物清单校验失败")
	}
	if err := json.Unmarshal(row.Manifest, &manifest); err != nil || manifest.SchemaVersion != 1 || manifest.RunID != run.ID || manifest.AppID != run.AppID || manifest.TargetID != row.TargetID || len(manifest.Files) == 0 || len(manifest.Files) > 2000 {
		return localArtifactManifest{}, "", errors.New("保存的本地产物清单无效")
	}
	names := map[string]bool{}
	for _, file := range manifest.Files {
		key := strings.ToLower(file.Name)
		if !validLocalArtifactName(file.Name) || !validLocalDigest(file.SHA256) || file.SizeBytes <= 0 || file.ID != localArtifactID(run.ID, row.TargetID, file.Name) || names[key] {
			return localArtifactManifest{}, "", errors.New("本地产物清单包含无效或重复文件")
		}
		names[key] = true
	}
	dir, err := s.localArtifactTargetDirectory(run.ID, row.TargetID)
	if err != nil {
		return manifest, "", err
	}
	if _, err := checkedLocalPath(s.store.ReleaseDataDir(), filepath.Join("local-builds", run.ID, row.TargetID), true); err != nil {
		return manifest, "", err
	}
	raw, err := readLocalManifest(dir)
	if err != nil {
		return manifest, "", errors.New("保存的本地产物清单不存在或不可读取")
	}
	diskSum := sha256.Sum256(raw)
	if hex.EncodeToString(diskSum[:]) != row.ManifestSHA256 {
		return manifest, "", errors.New("本地产物磁盘清单已被修改")
	}
	return manifest, dir, nil
}

// Return an unavailable row rather than hiding a missing/corrupt artifact.
// Only a structurally valid authoritative DB manifest supplies public names;
// the disk copy must then match that manifest before any file can be opened.
func (s *Service) LocalBuildArtifacts(ctx context.Context, runID string) ([]LocalBuildArtifact, error) {
	run, plan, err := s.localArtifactRun(runID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.LocalBuildArtifactManifests(runID)
	if err != nil {
		return nil, err
	}
	out := []LocalBuildArtifact{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		manifest, dir, manifestErr := s.loadLocalArtifactManifest(run, plan, row)
		if len(manifest.Files) == 0 {
			out = append(out, LocalBuildArtifact{TargetID: row.TargetID, Name: "产物清单", Error: "本地产物清单无效或已损坏"})
			continue
		}
		for _, file := range manifest.Files {
			view := LocalBuildArtifact{ID: file.ID, TargetID: manifest.TargetID, Name: file.Name, SizeBytes: file.SizeBytes, SHA256: file.SHA256}
			if manifestErr != nil {
				view.Error = manifestErr.Error()
			} else if handle, err := verifyLocalFile(ctx, dir, file); err != nil {
				view.Error = err.Error()
			} else {
				handle.Close()
				view.Available = true
			}
			out = append(out, view)
		}
	}
	return out, nil
}

// The handle is already hash-checked and rewound. Stream this same handle;
// reopening a path after verification would reintroduce a replacement race.
func (s *Service) OpenLocalBuildArtifact(ctx context.Context, runID, artifactID string) (*os.File, LocalBuildArtifact, error) {
	view := LocalBuildArtifact{}
	if !validLocalDigest(artifactID) {
		return nil, view, &Error{Code: "local_artifact_not_found", Message: "本地产物不存在"}
	}
	run, plan, err := s.localArtifactRun(runID)
	if err != nil {
		return nil, view, err
	}
	rows, err := s.store.LocalBuildArtifactManifests(runID)
	if err != nil {
		return nil, view, err
	}
	for _, row := range rows {
		manifest, dir, err := s.loadLocalArtifactManifest(run, plan, row)
		for _, file := range manifest.Files {
			if file.ID != artifactID {
				continue
			}
			if err != nil {
				return nil, view, &Error{Code: "local_artifact_changed", Message: err.Error()}
			}
			handle, err := verifyLocalFile(ctx, dir, file)
			if err != nil {
				return nil, view, err
			}
			view = LocalBuildArtifact{ID: file.ID, TargetID: manifest.TargetID, Name: file.Name, SizeBytes: file.SizeBytes, SHA256: file.SHA256, Available: true}
			return handle, view, nil
		}
	}
	return nil, view, &Error{Code: "local_artifact_not_found", Message: "本地产物不属于此构建任务"}
}

// This computes a display path only from server-owned IDs, never a client path.
// A native open additionally verifies every saved file through the API helper.
func (s *Service) LocalBuildOutputDirectory(runID string) (string, error) {
	run, plan, err := s.localArtifactRun(runID)
	if err != nil {
		return "", err
	}
	rows, err := s.store.LocalBuildArtifactManifests(run.ID)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", &Error{Code: "local_artifacts_missing", Message: "还没有已保存的本地产物"}
	}
	for _, row := range rows {
		manifest, _, err := s.loadLocalArtifactManifest(run, plan, row)
		if err != nil {
			return "", &Error{Code: "local_artifact_changed", Message: err.Error()}
		}
		if len(rows) == 1 {
			parent := filepath.Dir(filepath.FromSlash(manifest.Files[0].Name))
			shared := true
			for _, file := range manifest.Files[1:] {
				if filepath.Dir(filepath.FromSlash(file.Name)) != parent {
					shared = false
					break
				}
			}
			if shared {
				return checkedLocalPath(s.store.ReleaseDataDir(), filepath.Join("local-builds", run.ID, manifest.TargetID, "files", parent), true)
			}
		}
	}
	return checkedLocalPath(s.store.ReleaseDataDir(), filepath.Join("local-builds", run.ID), true)
}

func (s *Service) VerifiedLocalBuildDirectory(ctx context.Context, runID string) (string, error) {
	files, err := s.LocalBuildArtifacts(ctx, runID)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", &Error{Code: "local_artifacts_missing", Message: "还没有已保存的本地产物"}
	}
	for _, file := range files {
		if !file.Available {
			return "", &Error{Code: "local_artifact_changed", Message: file.Error}
		}
	}
	return s.LocalBuildOutputDirectory(runID)
}
