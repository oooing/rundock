package api

import (
	"context"
	"mime"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Shared by standalone builds and formal releases' local-only targets. The
// caller validates the operation intent; the vault validates run/app ownership.
func (s *Server) handleSavedBuildDownload(w http.ResponseWriter, r *http.Request, runID, artifactID string) {
	handle, artifact, err := s.Publisher.OpenLocalBuildArtifact(r.Context(), runID, artifactID)
	if err != nil {
		writeLocalBuildError(w, err)
		return
	}
	defer handle.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(artifact.Name)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	http.ServeContent(w, r, filepath.Base(artifact.Name), time.Time{}, handle)
}

func (s *Server) handleSavedBuildOpenDirectory(w http.ResponseWriter, r *http.Request, runID string) {
	if err := readLocalBuildJSON(w, r, &struct{}{}, true); err != nil {
		writeError(w, http.StatusBadRequest, "打开目录请求格式无效")
		return
	}
	dir, err := s.Publisher.VerifiedLocalBuildDirectory(r.Context(), runID)
	if err != nil {
		writeLocalBuildError(w, err)
		return
	}
	if err := openLocalBuildDirectory(dir); err != nil {
		writeError(w, http.StatusInternalServerError, "无法打开产物目录，请通过目录路径手动打开")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"opened": dir})
}

func (s *Server) handleReleaseSavedArtifact(w http.ResponseWriter, r *http.Request, runID, rest string) {
	if !s.allowLocalOperation(w, r) {
		return
	}
	if !localBuildQueryAllowed(r, "") {
		writeError(w, http.StatusBadRequest, "不支持的查询参数")
		return
	}
	if rest == "saved-artifacts" && r.Method == http.MethodGet {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		artifacts, err := s.Publisher.LocalBuildArtifacts(ctx, runID)
		if err != nil {
			writeLocalBuildError(w, err)
			return
		}
		directory, _ := s.Publisher.LocalBuildOutputDirectory(runID)
		writeJSON(w, http.StatusOK, map[string]any{"artifacts": artifacts, "outputDirectory": directory})
		return
	}
	if strings.HasPrefix(rest, "artifacts/") && r.Method == http.MethodGet {
		s.handleSavedBuildDownload(w, r, runID, strings.TrimPrefix(rest, "artifacts/"))
		return
	}
	if rest == "open-artifact-dir" && r.Method == http.MethodPost {
		s.handleSavedBuildOpenDirectory(w, r, runID)
		return
	}
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func openLocalBuildDirectory(dir string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", dir)
	case "darwin":
		command = exec.Command("open", dir)
	default:
		command = exec.Command("xdg-open", dir)
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }() // Reap only the explicitly requested native opener.
	return nil
}
