package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/google/uuid"
)

const maxRequestBody = 64 << 10

type Handler struct {
	service *monitor.Service
	logger  *slog.Logger
}

func New(service *monitor.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/monitors", h.create)
	mux.HandleFunc("GET /api/v1/monitors", h.list)
	mux.HandleFunc("GET /api/v1/monitors/{id}", h.get)
	mux.HandleFunc("PATCH /api/v1/monitors/{id}", h.update)
	mux.HandleFunc("DELETE /api/v1/monitors/{id}", h.delete)
	mux.HandleFunc("POST /api/v1/monitors/{id}/pause", h.pause)
	mux.HandleFunc("POST /api/v1/monitors/{id}/resume", h.resume)
}

type createRequest struct {
	Name              string  `json:"name"`
	Slug              string  `json:"slug"`
	Description       *string `json:"description"`
	Kind              string  `json:"kind"`
	URL               string  `json:"url"`
	HTTPMethod        string  `json:"http_method"`
	ExpectedStatusMin int     `json:"expected_status_min"`
	ExpectedStatusMax int     `json:"expected_status_max"`
	IntervalSeconds   int     `json:"interval_seconds"`
	TimeoutMS         int     `json:"timeout_ms"`
	FailureThreshold  int     `json:"failure_threshold"`
	RecoveryThreshold int     `json:"recovery_threshold"`
	Enabled           *bool   `json:"enabled"`
	Public            bool    `json:"public"`
}

type patchRequest struct {
	Name              *string         `json:"name"`
	Slug              *string         `json:"slug"`
	Description       json.RawMessage `json:"description"`
	URL               *string         `json:"url"`
	HTTPMethod        *string         `json:"http_method"`
	ExpectedStatusMin *int            `json:"expected_status_min"`
	ExpectedStatusMax *int            `json:"expected_status_max"`
	IntervalSeconds   *int            `json:"interval_seconds"`
	TimeoutMS         *int            `json:"timeout_ms"`
	FailureThreshold  *int            `json:"failure_threshold"`
	RecoveryThreshold *int            `json:"recovery_threshold"`
	Public            *bool           `json:"public"`
}

type monitorResponse struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	Slug              string     `json:"slug"`
	Description       *string    `json:"description"`
	Kind              string     `json:"kind"`
	URL               string     `json:"url"`
	HTTPMethod        string     `json:"http_method"`
	ExpectedStatusMin int        `json:"expected_status_min"`
	ExpectedStatusMax int        `json:"expected_status_max"`
	IntervalSeconds   int        `json:"interval_seconds"`
	TimeoutMS         int        `json:"timeout_ms"`
	FailureThreshold  int        `json:"failure_threshold"`
	RecoveryThreshold int        `json:"recovery_threshold"`
	Enabled           bool       `json:"enabled"`
	Public            bool       `json:"public"`
	Version           int64      `json:"version"`
	State             string     `json:"state"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	StateUpdatedAt    time.Time  `json:"state_updated_at"`
	ArchivedAt        *time.Time `json:"archived_at"`
}

type problem struct {
	Type   string            `json:"type"`
	Title  string            `json:"title"`
	Status int               `json:"status"`
	Detail string            `json:"detail,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var request createRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-request", "Invalid request", err.Error(), nil)
		return
	}
	value, err := h.service.Create(r.Context(), monitor.CreateInput{
		Name: request.Name, Slug: request.Slug, Description: request.Description,
		Kind: request.Kind, URL: request.URL, HTTPMethod: request.HTTPMethod,
		ExpectedStatusMin: request.ExpectedStatusMin, ExpectedStatusMax: request.ExpectedStatusMax,
		Interval:         time.Duration(request.IntervalSeconds) * time.Second,
		Timeout:          time.Duration(request.TimeoutMS) * time.Millisecond,
		FailureThreshold: request.FailureThreshold, RecoveryThreshold: request.RecoveryThreshold,
		Enabled: request.Enabled, Public: request.Public,
	})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/monitors/"+value.ID.String())
	writeJSON(w, http.StatusCreated, response(value))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50, 1, 100)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-query", "Invalid query", err.Error(), nil)
		return
	}
	offset, err := queryInt(r, "offset", 0, 0, 1_000_000)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-query", "Invalid query", err.Error(), nil)
		return
	}
	values, err := h.service.List(r.Context(), monitor.ListOptions{Limit: limit, Offset: offset})
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	items := make([]monitorResponse, 0, len(values))
	for _, value := range values {
		items = append(items, response(value))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	value, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response(value))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var request patchRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-request", "Invalid request", err.Error(), nil)
		return
	}
	patch, err := request.patch()
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-request", "Invalid request", err.Error(), nil)
		return
	}
	value, err := h.service.Update(r.Context(), id, patch)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response(value))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.service.Delete(r.Context(), id); err != nil {
		h.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) pause(w http.ResponseWriter, r *http.Request) {
	h.changeState(w, r, h.service.Pause)
}

func (h *Handler) resume(w http.ResponseWriter, r *http.Request) {
	h.changeState(w, r, h.service.Resume)
}

func (h *Handler) changeState(w http.ResponseWriter, r *http.Request, command func(context.Context, uuid.UUID) (monitor.Monitor, error)) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	value, err := command(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response(value))
}

func (r patchRequest) patch() (monitor.PatchInput, error) {
	patch := monitor.PatchInput{
		Name: r.Name, Slug: r.Slug, URL: r.URL, HTTPMethod: r.HTTPMethod,
		ExpectedStatusMin: r.ExpectedStatusMin, ExpectedStatusMax: r.ExpectedStatusMax,
		FailureThreshold: r.FailureThreshold, RecoveryThreshold: r.RecoveryThreshold, Public: r.Public,
	}
	set := r.Name != nil || r.Slug != nil || r.URL != nil || r.HTTPMethod != nil ||
		r.ExpectedStatusMin != nil || r.ExpectedStatusMax != nil || r.IntervalSeconds != nil ||
		r.TimeoutMS != nil || r.FailureThreshold != nil || r.RecoveryThreshold != nil || r.Public != nil
	if r.Description != nil {
		set = true
		patch.Description.Set = true
		if !bytes.Equal(bytes.TrimSpace(r.Description), []byte("null")) {
			var description string
			if err := json.Unmarshal(r.Description, &description); err != nil {
				return monitor.PatchInput{}, errors.New("description must be a string or null")
			}
			patch.Description.Value = &description
		}
	}
	if r.IntervalSeconds != nil {
		value := time.Duration(*r.IntervalSeconds) * time.Second
		patch.Interval = &value
	}
	if r.TimeoutMS != nil {
		value := time.Duration(*r.TimeoutMS) * time.Millisecond
		patch.Timeout = &value
	}
	if !set {
		return monitor.PatchInput{}, errors.New("request must include at least one mutable field")
	}
	return patch, nil
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	var validation *monitor.ValidationError
	switch {
	case errors.As(err, &validation):
		writeProblem(w, http.StatusBadRequest, "validation-error", "Validation failed", validation.Error(), validation.Fields)
	case errors.Is(err, monitor.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not-found", "Monitor not found", err.Error(), nil)
	case errors.Is(err, monitor.ErrSlugConflict):
		writeProblem(w, http.StatusConflict, "slug-conflict", "Slug already exists", err.Error(), map[string]string{"slug": "must be unique"})
	case errors.Is(err, monitor.ErrArchived):
		writeProblem(w, http.StatusConflict, "monitor-archived", "Monitor archived", err.Error(), nil)
	case errors.Is(err, monitor.ErrWriteConflict):
		writeProblem(w, http.StatusConflict, "write-conflict", "Concurrent modification", err.Error(), nil)
	default:
		h.logger.Error("monitor request failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "internal-error", "Internal server error", "an unexpected error occurred", nil)
	}
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-id", "Invalid monitor ID", "id must be a UUID", nil)
		return uuid.Nil, false
	}
	return id, true
}

func queryInt(r *http.Request, key string, fallback, minimum, maximum int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return errors.New("Content-Type must be application/json")
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func response(value monitor.Monitor) monitorResponse {
	return monitorResponse{
		ID: value.ID, Name: value.Name, Slug: value.Slug, Description: value.Description,
		Kind: value.Kind, URL: value.URL, HTTPMethod: value.HTTPMethod,
		ExpectedStatusMin: value.ExpectedStatusMin, ExpectedStatusMax: value.ExpectedStatusMax,
		IntervalSeconds: int(value.Interval.Seconds()), TimeoutMS: int(value.Timeout.Milliseconds()),
		FailureThreshold: value.FailureThreshold, RecoveryThreshold: value.RecoveryThreshold,
		Enabled: value.Enabled, Public: value.Public, Version: value.Version, State: value.State,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, StateUpdatedAt: value.StateUpdatedAt, ArchivedAt: value.ArchivedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, problemType, title, detail string, fields map[string]string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "https://vigil.boniluan.com/problems/" + problemType, Title: title, Status: status, Detail: detail, Fields: fields})
}
