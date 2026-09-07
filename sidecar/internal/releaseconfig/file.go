package releaseconfig

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed release.example.yaml
var ExampleFile string

type ConfigFile struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Exists   bool   `json:"exists"`
	Revision string `json:"revision"`
}

func fileRevision(raw []byte, exists bool) string {
	if !exists {
		return "missing"
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Read raw text even when invalid, so users can repair a broken manifest.
// Missing files are proposed drafts only; viewing never creates a file.
func (s *Service) GetFile(ctx context.Context, appID string) (*ConfigFile, error) {
	_, root, repoFound, err := s.project(ctx, appID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, filepath.FromSlash(ManifestPath))
	raw, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, &Error{Code: "config_read_failed", Message: "无法读取发布配置：" + err.Error()}
	}
	revision := fileRevision(raw, exists)
	if !exists {
		cfg := cloneForManifest(s.scanRoot(root, repoFound))
		document := struct {
			SchemaVersion int            `json:"schemaVersion"`
			VersionGroups []VersionGroup `json:"versionGroups"`
			Targets       []Target       `json:"targets"`
			Automation    *Automation    `json:"automation,omitempty"`
		}{cfg.SchemaVersion, cfg.VersionGroups, cfg.Targets, cfg.Automation}
		raw, err = json.MarshalIndent(document, "", "  ")
		if err != nil {
			return nil, err
		}
		raw = append(raw, '\n')
	}
	return &ConfigFile{Path: path, Content: string(raw), Exists: exists, Revision: revision}, nil
}

// Validate before writing, preserve comments, and reject stale edits.
func (s *Service) PutFile(ctx context.Context, appID, content, revision string) (*Config, error) {
	_, root, _, err := s.project(ctx, appID)
	if err != nil {
		return nil, err
	}
	cfg, err := decode([]byte(content))
	if err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	if err := validate(cfg); err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	addSiblingCargoLocks(cfg, root)
	if err := validate(cfg); err != nil {
		return nil, &Error{Code: "config_invalid", Message: err.Error()}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ManifestPath)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, &Error{Code: "config_read_failed", Message: "无法读取发布配置：" + err.Error()}
	}
	if revision != fileRevision(raw, err == nil) {
		return nil, &Error{Code: "config_conflict", Message: "配置文件已在其他地方修改。请先保留你的编辑内容，关闭后重新打开文件再合并修改。"}
	}
	return writeManifest(root, cfg, []byte(content))
}
