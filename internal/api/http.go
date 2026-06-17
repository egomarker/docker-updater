package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/egomarker/docker-updater/internal/model"
	"github.com/egomarker/docker-updater/internal/update"
)

type Handler struct {
	token        string
	version      string
	service      *update.Service
	maxTailLines int
}

func NewHandler(token, version string, service *update.Service, maxTailLines int) http.Handler {
	return &Handler{
		token:        token,
		version:      version,
		service:      service,
		maxTailLines: maxTailLines,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/healthz" {
		if r.Method != http.MethodGet {
			h.writeMethodNotAllowed(w)
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"version": h.version,
		})
		return
	}

	if !h.authorized(r.Header.Get("Authorization")) {
		h.writeError(w, http.StatusUnauthorized, "unauthorized", nil)
		return
	}

	segments := splitPath(r.URL.Path)
	if len(segments) < 2 || segments[0] != "v1" {
		h.writeError(w, http.StatusNotFound, "not found", nil)
		return
	}

	ctx := r.Context()

	switch segments[1] {
	case "projects":
		if len(segments) < 4 {
			h.writeError(w, http.StatusNotFound, "not found", nil)
			return
		}
		projectID := segments[2]
		switch {
		case len(segments) == 4 && segments[3] == "deploy":
			h.handleDeploy(ctx, w, r, projectID)
		case len(segments) == 4 && segments[3] == "restart":
			h.handleRestart(ctx, w, r, projectID)
		case len(segments) == 4 && segments[3] == "backup":
			h.handleBackup(ctx, w, r, projectID)
		case len(segments) == 5 && segments[3] == "jobs" && segments[4] == "latest":
			h.handleLatestJob(ctx, w, r, projectID)
		case len(segments) == 6 && segments[3] == "jobs" && segments[4] == "latest" && segments[5] == "log":
			h.handleLatestJobLog(ctx, w, r, projectID)
		case len(segments) == 5 && segments[3] == "jobs":
			h.handleJob(ctx, w, r, projectID, segments[4])
		case len(segments) == 6 && segments[3] == "jobs" && segments[5] == "log":
			h.handleJobLog(ctx, w, r, projectID, segments[4])
		default:
			h.writeError(w, http.StatusNotFound, "not found", nil)
		}
	case "scripts":
		if len(segments) == 2 {
			h.handleListScripts(ctx, w, r)
			return
		}
		scriptName := segments[2]
		switch {
		case len(segments) == 3:
			h.handleStartScript(ctx, w, r, scriptName)
		case len(segments) == 5 && segments[3] == "jobs" && segments[4] == "latest":
			h.handleLatestScriptJob(ctx, w, r, scriptName)
		case len(segments) == 6 && segments[3] == "jobs" && segments[4] == "latest" && segments[5] == "log":
			h.handleLatestScriptJobLog(ctx, w, r, scriptName)
		case len(segments) == 5 && segments[3] == "jobs":
			h.handleScriptJob(ctx, w, r, scriptName, segments[4])
		case len(segments) == 6 && segments[3] == "jobs" && segments[5] == "log":
			h.handleScriptJobLog(ctx, w, r, scriptName, segments[4])
		default:
			h.writeError(w, http.StatusNotFound, "not found", nil)
		}
	case "notify":
		if len(segments) == 3 && segments[2] == "test" {
			h.handleNotifyTest(ctx, w, r)
			return
		}
		h.writeError(w, http.StatusNotFound, "not found", nil)
	default:
		h.writeError(w, http.StatusNotFound, "not found", nil)
	}
}

func (h *Handler) handleDeploy(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w)
		return
	}

	var req model.DeployRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	meta, err := h.service.StartDeploy(ctx, projectID, req)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusAccepted, acceptedResponse(meta))
}

func (h *Handler) handleRestart(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w)
		return
	}
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
	}

	meta, err := h.service.StartRestart(ctx, projectID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusAccepted, acceptedResponse(meta))
}

func (h *Handler) handleBackup(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w)
		return
	}
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
	}

	meta, err := h.service.StartBackup(ctx, projectID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusAccepted, acceptedResponse(meta))
}

func (h *Handler) handleListScripts(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"scripts": h.service.ListScripts(ctx),
	})
}

func (h *Handler) handleNotifyTest(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w)
		return
	}
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
	}
	if err := h.service.TestNotify(ctx); err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"status": "sent"})
}

func (h *Handler) handleStartScript(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w)
		return
	}
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
	}

	meta, err := h.service.StartScript(ctx, name)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusAccepted, acceptedResponse(meta))
}

func (h *Handler) handleJob(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID, jobID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	meta, err := h.service.GetJob(ctx, projectID, jobID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) handleLatestJob(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	meta, err := h.service.GetLatestJob(ctx, projectID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) handleJobLog(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID, jobID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	tail, err := h.parseTail(r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	logText, err := h.service.GetJobLog(ctx, projectID, jobID, tail)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeText(w, http.StatusOK, logText)
}

func (h *Handler) handleLatestScriptJob(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	meta, err := h.service.GetLatestScriptJob(ctx, name)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) handleScriptJob(ctx context.Context, w http.ResponseWriter, r *http.Request, name, jobID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	meta, err := h.service.GetScriptJob(ctx, name, jobID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) handleScriptJobLog(ctx context.Context, w http.ResponseWriter, r *http.Request, name, jobID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	tail, err := h.parseTail(r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	logText, err := h.service.GetScriptJobLog(ctx, name, jobID, tail)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeText(w, http.StatusOK, logText)
}

func (h *Handler) handleLatestScriptJobLog(ctx context.Context, w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	tail, err := h.parseTail(r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	logText, err := h.service.GetLatestScriptJobLog(ctx, name, tail)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeText(w, http.StatusOK, logText)
}

func (h *Handler) handleLatestJobLog(ctx context.Context, w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w)
		return
	}
	tail, err := h.parseTail(r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	logText, err := h.service.GetLatestJobLog(ctx, projectID, tail)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	h.writeText(w, http.StatusOK, logText)
}

func (h *Handler) parseTail(r *http.Request) (*int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("tail"))
	if value == "" {
		return nil, nil
	}
	tail, err := strconv.Atoi(value)
	if err != nil || tail <= 0 {
		return nil, fmt.Errorf("tail must be a positive integer")
	}
	if tail > h.maxTailLines {
		return nil, fmt.Errorf("tail exceeds max_tail_lines")
	}
	return &tail, nil
}

func (h *Handler) authorized(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" || h.token == "" {
		return false
	}
	if len(token) != len(h.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.token)) == 1
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	serviceErr, ok := update.AsServiceError(err)
	if !ok {
		h.writeError(w, http.StatusInternalServerError, "internal server error", nil)
		return
	}

	switch serviceErr.Kind {
	case update.ErrorKindBadRequest:
		h.writeError(w, http.StatusBadRequest, serviceErr.Message, nil)
	case update.ErrorKindNotFound:
		h.writeError(w, http.StatusNotFound, serviceErr.Message, nil)
	case update.ErrorKindConflict:
		extra := map[string]any{}
		if serviceErr.ActiveJobID != "" {
			extra["active_job_id"] = serviceErr.ActiveJobID
		}
		h.writeError(w, http.StatusConflict, serviceErr.Message, extra)
	default:
		h.writeError(w, http.StatusInternalServerError, serviceErr.Message, nil)
	}
}

func (h *Handler) writeMethodNotAllowed(w http.ResponseWriter) {
	h.writeError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) writeText(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, value)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string, extra map[string]any) {
	payload := map[string]any{
		"error": message,
	}
	for key, value := range extra {
		payload[key] = value
	}
	h.writeJSON(w, status, payload)
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func decodeJSON(body io.Reader, target any) error {
	if body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return errors.New("request body must contain a single JSON object")
	}
	return errors.New("request body must contain a single JSON object")
}

type acceptedJobResponse struct {
	JobID     string                `json:"job_id"`
	ProjectID string                `json:"project_id"`
	Kind      model.JobKind         `json:"kind"`
	Status    model.JobStatus       `json:"status"`
	Phase     model.JobPhase        `json:"phase"`
	CreatedAt time.Time             `json:"created_at"`
	StartedAt time.Time             `json:"started_at"`
	Request   *model.JobRequest     `json:"request,omitempty"`
	Script    *model.JobScriptState `json:"script,omitempty"`
}

func acceptedResponse(meta *model.JobMeta) acceptedJobResponse {
	return acceptedJobResponse{
		JobID:     meta.JobID,
		ProjectID: meta.ProjectID,
		Kind:      meta.Kind,
		Status:    meta.Status,
		Phase:     meta.Phase,
		CreatedAt: meta.CreatedAt,
		StartedAt: meta.StartedAt,
		Request:   meta.Request,
		Script:    meta.Script,
	}
}
