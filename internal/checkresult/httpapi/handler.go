package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/google/uuid"
)

type Handler struct {
	service *checkresult.Service
	logger  *slog.Logger
}

func New(service *checkresult.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/monitors/{id}/checks", h.list)
}

type resultResponse struct {
	ID               uuid.UUID        `json:"id"`
	MonitorID        uuid.UUID        `json:"monitor_id"`
	ExecutionID      *uuid.UUID       `json:"execution_id"`
	StartedAt        time.Time        `json:"started_at"`
	FinishedAt       time.Time        `json:"finished_at"`
	DurationMS       int64            `json:"duration_ms"`
	Outcome          check.Outcome    `json:"outcome"`
	StatusCode       *int             `json:"status_code"`
	ErrorCode        *check.ErrorCode `json:"error_code"`
	ErrorDescription *string          `json:"error_description"`
	DialedIP         *string          `json:"dialed_ip"`
	TLSExpiresAt     *time.Time       `json:"tls_expires_at"`
	CreatedAt        time.Time        `json:"created_at"`
}

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-id", "Invalid monitor ID", "id must be a UUID")
		return
	}
	limit, err := queryInt(r, "limit", 50, 1, 100)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-query", "Invalid query", err.Error())
		return
	}
	offset, err := queryInt(r, "offset", 0, 0, 1_000_000)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid-query", "Invalid query", err.Error())
		return
	}
	results, err := h.service.List(r.Context(), id, checkresult.ListOptions{Limit: limit, Offset: offset})
	if errors.Is(err, monitor.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "not-found", "Monitor not found", err.Error())
		return
	}
	if err != nil {
		h.logger.Error("list check results failed", "monitor_id", id, "error", err)
		writeProblem(w, http.StatusInternalServerError, "internal-error", "Internal server error", "an unexpected error occurred")
		return
	}
	items := make([]resultResponse, 0, len(results))
	for _, result := range results {
		items = append(items, response(result))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset})
}

func response(result checkresult.StoredResult) resultResponse {
	response := resultResponse{
		ID: result.ID, MonitorID: result.MonitorID, ExecutionID: result.ExecutionID, StartedAt: result.StartedAt,
		FinishedAt: result.FinishedAt, DurationMS: result.Duration.Milliseconds(),
		Outcome: result.Outcome, StatusCode: result.StatusCode,
		TLSExpiresAt: result.TLSExpiresAt, CreatedAt: result.CreatedAt,
	}
	if result.Error != nil {
		code := result.Error.Code
		description := result.Error.Description
		response.ErrorCode = &code
		response.ErrorDescription = &description
	}
	if result.DialedIP != nil {
		address := result.DialedIP.String()
		response.DialedIP = &address
	}
	return response
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProblem(w http.ResponseWriter, status int, problemType, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{
		Type:  "https://vigil.boniluan.com/problems/" + problemType,
		Title: title, Status: status, Detail: detail,
	})
}
