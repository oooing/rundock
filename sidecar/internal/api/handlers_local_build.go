package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/launcher-sidecar/internal/publisher"
	"github.com/launcher-sidecar/internal/store"
)

func writeLocalBuildError(w http.ResponseWriter, err error) {
	var typed *publisher.Error
	if !errors.As(err, &typed) {
		writeError(w, http.StatusInternalServerError, "本地构建操作失败，请重试")
		return
	}
	status := http.StatusBadRequest
	switch typed.Code {
	case "app_not_found", "local_build_not_found", "local_artifact_not_found":
		status = http.StatusNotFound
	case "local_build_config_changed", "local_build_request_conflict", "release_in_progress", "local_build_in_progress", "local_manifest_conflict", "local_artifact_changed", "local_artifact_missing":
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": typed.Message, "code": typed.Code})
}

func readLocalBuildJSON(w http.ResponseWriter, r *http.Request, value any, optional bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}

func localBuildQueryAllowed(r *http.Request, allowed string) bool {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	for key, value := range values {
		if key != allowed || allowed == "" || len(value) != 1 {
			return false
		}
	}
	return true
}

func (s *Server) handleAppLocalBuilds(w http.ResponseWriter, r *http.Request, appID string) {
	if !s.allowLocalOperation(w, r) {
		return
	}
	if !localBuildQueryAllowed(r, "") {
		writeError(w, http.StatusBadRequest, "不支持的查询参数")
		return
	}
	if app, err := s.Store.GetApp(appID); err != nil || app == nil {
		writeError(w, http.StatusNotFound, "项目不存在")
		return
	}
	switch r.Method {
	case http.MethodGet:
		// History belongs to completed tasks, not to today's editable manifest.
		// A broken current configuration must not hide already saved packages.
		runs, err := s.Publisher.ListLocalBuilds(appID)
		if err != nil {
			writeLocalBuildError(w, err)
			return
		}
		if runs == nil {
			runs = []*store.ReleaseRun{}
		}
		preparation, err := s.Publisher.PrepareLocalBuild(r.Context(), appID)
		if err != nil {
			message := "当前构建配置不可用，请检查配置后重试；已有产物仍可取回"
			var typed *publisher.Error
			if errors.As(err, &typed) {
				message = typed.Message
			}
			writeJSON(w, http.StatusOK, map[string]any{"preparation": nil, "preparationError": message, "recentRuns": runs})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preparation": preparation, "recentRuns": runs})
	case http.MethodPost:
		var body publisher.LocalBuildRequest
		if err := readLocalBuildJSON(w, r, &body, false); err != nil {
			writeError(w, http.StatusBadRequest, "构建请求格式无效")
			return
		}
		run, err := s.Publisher.StartLocalBuild(r.Context(), appID, body)
		if err != nil {
			writeLocalBuildError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, run)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleLocalBuildDetail(w http.ResponseWriter, r *http.Request) {
	if !s.allowLocalOperation(w, r) {
		return
	}
	runID, rest := pathTail("/api/local-builds/", r.URL.Path)
	if runID == "" || strings.ContainsAny(runID, "\\\x00") {
		writeError(w, http.StatusNotFound, "本地构建记录不存在")
		return
	}
	query := ""
	if rest == "" && r.Method == http.MethodGet {
		query = "sinceLogId"
	}
	if !localBuildQueryAllowed(r, query) {
		writeError(w, http.StatusBadRequest, "不支持的查询参数")
		return
	}
	// Never let the local cancel/open/download API operate on a formal release.
	run, err := s.Publisher.CheckLocalBuildRun(runID)
	if err != nil {
		writeLocalBuildError(w, err)
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		since := int64(0)
		if value := r.URL.Query().Get("sinceLogId"); value != "" {
			var err error
			since, err = strconv.ParseInt(value, 10, 64)
			if err != nil || since < 0 {
				writeError(w, http.StatusBadRequest, "日志位置无效")
				return
			}
		}
		view, err := s.Publisher.GetLocalBuild(run.ID, since)
		if err != nil {
			writeLocalBuildError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	case rest == "cancel" && r.Method == http.MethodPost:
		if err := readLocalBuildJSON(w, r, &struct{}{}, true); err != nil {
			writeError(w, http.StatusBadRequest, "取消请求格式无效")
			return
		}
		if err := s.Publisher.Cancel(runID); err != nil {
			writeLocalBuildError(w, err)
			return
		}
		view, err := s.Publisher.GetLocalBuild(runID, 0)
		if err != nil {
			writeLocalBuildError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	case rest == "open-dir" && r.Method == http.MethodPost:
		s.handleSavedBuildOpenDirectory(w, r, runID)
	case strings.HasPrefix(rest, "artifacts/") && r.Method == http.MethodGet:
		artifactID := strings.TrimPrefix(rest, "artifacts/")
		s.handleSavedBuildDownload(w, r, runID, artifactID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
