package publisher

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/launcher-sidecar/internal/store"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) captureTargetArtifacts(run *store.ReleaseRun, target planTarget, workingDir string) error {
	canonicalRoot, err := secureProjectPath(run.RepoRoot, ".", true)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	count := 0
	patterns := append([]string{}, target.Artifacts...)
	for _, r := range target.ArtifactRules {
		patterns = append(patterns, r.Pattern)
	}
	for _, pattern := range patterns {
		pattern = strings.NewReplacer("${VERSION}", run.TargetVersion, "${TAG}", run.TagName).Replace(pattern)
		base := artifactWalkBase(workingDir, pattern)
		if _, err := os.Stat(base); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		baseRel, err := filepath.Rel(canonicalRoot, base)
		if err != nil || baseRel == ".." || strings.HasPrefix(baseRel, ".."+string(filepath.Separator)) {
			return errors.New("产物路径通过符号链接跳出了项目目录")
		}
		base, err = secureProjectPath(canonicalRoot, baseRel, true)
		if err != nil {
			return errors.New("产物路径无效：" + err.Error())
		}
		err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			linkInfo, linkErr := os.Lstat(path)
			if linkErr != nil {
				return linkErr
			}
			if isPathLink(linkInfo) {
				return errors.New("产物目录包含符号链接或目录联接")
			}
			entryRel, relErr := filepath.Rel(canonicalRoot, path)
			if relErr != nil || entryRel == ".." || strings.HasPrefix(entryRel, ".."+string(filepath.Separator)) {
				return errors.New("产物目录包含指向项目外部的链接")
			}
			if entry.IsDir() {
				if entry.Name() == ".git" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			relWorking, err := filepath.Rel(workingDir, path)
			if err != nil || !globPattern(pattern).MatchString(filepath.ToSlash(relWorking)) {
				return nil
			}
			relRepo, err := filepath.Rel(canonicalRoot, path)
			if err != nil || relRepo == ".." || strings.HasPrefix(relRepo, ".."+string(filepath.Separator)) || seen[relRepo] {
				return nil
			}
			seen[relRepo] = true
			count++
			if count > 2000 {
				return errors.New("产物文件超过 2000 个，请缩小产物匹配范围")
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			hash, err := hashArtifact(path)
			if err != nil {
				return err
			}
			return s.store.AddReleaseArtifact(run.ID, target.ID, filepath.ToSlash(relRepo), info.Size(), hash)
		})
		if err != nil {
			return err
		}
	}
	if len(target.Artifacts) > 0 && count == 0 {
		return errors.New(target.Name + " 未找到配置中声明的构建产物")
	}
	return nil
}

func (s *Service) verifyFrozenTargetArtifacts(run *store.ReleaseRun, target planTarget) error {
	artifacts, err := s.store.ReleaseArtifacts(run.ID)
	if err != nil {
		return err
	}
	found := 0
	for _, artifact := range artifacts {
		if artifact.TargetID != target.ID {
			continue
		}
		found++
		path, pathErr := secureProjectPath(run.RepoRoot, artifact.Path, false)
		if pathErr != nil {
			return fmt.Errorf("%s 不可读取", artifact.Path)
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != artifact.SizeBytes {
			return fmt.Errorf("%s 的大小或类型与构建完成时不一致", artifact.Path)
		}
		hash, hashErr := hashArtifact(path)
		if hashErr != nil || !strings.EqualFold(hash, artifact.SHA256) {
			return fmt.Errorf("%s 的内容与构建完成时不一致", artifact.Path)
		}
	}
	if len(target.Artifacts) > 0 && found == 0 {
		return errors.New("缺少构建完成时记录的产物")
	}
	return nil
}

func artifactWalkBase(workingDir, pattern string) string {
	pattern = filepath.FromSlash(pattern)
	index := strings.IndexAny(pattern, "*?")
	prefix := pattern
	if index >= 0 {
		prefix = pattern[:index]
	}
	base := ""
	if strings.HasSuffix(prefix, string(filepath.Separator)) {
		base = strings.TrimSuffix(prefix, string(filepath.Separator))
	} else {
		base = filepath.Dir(prefix)
	}
	if base == "" || base == "." {
		return workingDir
	}
	return filepath.Join(workingDir, base)
}

func hashArtifact(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func stepLabel(step string) string {
	return map[string]string{"check": "检查", "build": "构建", "package": "打包", "publish": "上传", "deploy": "部署"}[step]
}

func truncateTargetOutput(value string) string {
	const limit = 64 * 1024
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n…输出过长，已截断…"
}
