package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/launcher-sidecar/internal/diagnostics"
	"github.com/launcher-sidecar/internal/publisher"
	"github.com/launcher-sidecar/internal/store"
)

func writePublisherError(w http.ResponseWriter, err error) {
	if pe, ok := err.(*publisher.Error); ok {
		status := http.StatusBadRequest
		if pe.Code == "app_not_found" || pe.Code == "release_not_found" {
			status = http.StatusNotFound
		} else if pe.Code == "status_changed" || pe.Code == "release_in_progress" || pe.Code == "tag_exists" || pe.Code == "staged_changes" {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": pe.Message, "code": pe.Code})
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func (s *Server) handleReleasePreflight(w http.ResponseWriter, r *http.Request, appID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	started := time.Now()
	var pf *publisher.Preflight
	var err error
	if r.URL.Query().Get("remote") == "false" {
		pf, err = s.Publisher.PreflightLocal(r.Context(), appID)
	} else {
		pf, err = s.Publisher.Preflight(r.Context(), appID)
	}
	if err != nil {
		s.recordPublisherFailure(appID, "release.preflight", err, time.Since(started))
		writePublisherError(w, err)
		return
	}
	if s.Diagnostics != nil {
		s.Diagnostics.Record(diagnostics.Event{
			AppID: appID, Kind: "performance", Severity: "info", Source: "release",
			Operation: "release.preflight", Status: "succeeded", DurationMS: time.Since(started).Milliseconds(),
			Message: "发布检查完成",
		})
	}
	writeJSON(w, http.StatusOK, pf)
}

func (s *Server) handleReleaseNotesDraft(w http.ResponseWriter, r *http.Request, appID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body publisher.NotesDraftRequest
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	started := time.Now()
	draft, err := s.Publisher.DraftReleaseNotes(r.Context(), appID, body)
	if err != nil {
		s.recordPublisherFailure(appID, "release.notes_draft", err, time.Since(started))
		writePublisherError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (s *Server) handleReleaseProfile(w http.ResponseWriter, r *http.Request, appID string) {
	if a, _ := s.Store.GetApp(appID); a == nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := s.Store.GetReleaseProfile(appID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
	case http.MethodPatch:
		p := store.DefaultReleaseProfile(appID)
		if err := readJSON(r, p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		p.AppID = appID
		if err := s.Publisher.SaveProfile(p); err != nil {
			writePublisherError(w, err)
			return
		}
		updated, _ := s.Store.GetReleaseProfile(appID)
		writeJSON(w, http.StatusOK, updated)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAppReleases(w http.ResponseWriter, r *http.Request, appID string) {
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		runs, err := s.Store.ListReleaseRuns(appID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if runs == nil {
			runs = []*store.ReleaseRun{}
		}
		writeJSON(w, http.StatusOK, runs)
	case http.MethodPost:
		started := time.Now()
		var body publisher.CreateRequest
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		run, err := s.Publisher.Start(r.Context(), appID, body)
		if err != nil {
			s.recordPublisherFailure(appID, "release.create", err, time.Since(started))
			writePublisherError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, run)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) recordPublisherFailure(appID, operation string, err error, duration time.Duration) {
	if s.Diagnostics == nil || err == nil {
		return
	}
	code := "release_error"
	message := err.Error()
	if typed, ok := err.(*publisher.Error); ok {
		code = typed.Code
		message = typed.Message
	}
	s.Diagnostics.Record(diagnostics.Event{
		AppID: appID, Kind: "error", Severity: "error", Source: "release",
		Operation: operation, Status: "failed", DurationMS: duration.Milliseconds(),
		ErrorCode: code, Message: message,
	})
}

func (s *Server) handleReleaseDetail(w http.ResponseWriter, r *http.Request) {
	runID, rest := pathTail("/api/releases/", r.URL.Path)
	if runID == "" {
		writeError(w, http.StatusNotFound, "release id required")
		return
	}
	if rest == "retry" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body publisher.RetryRequest
		if err := readJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		run, err := s.Publisher.Retry(runID, body)
		if err != nil {
			writePublisherError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, run)
		return
	}
	if rest != "" || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("sinceLogId"), 10, 64)
	view, err := s.Publisher.GetRun(runID, since)
	if err != nil {
		writePublisherError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
