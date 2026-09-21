// Package publicstatus serves a deliberately small, public-only projection.
package publicstatus

import (
	"log/slog"
	"net/http"
	"time"

	generated "github.com/BoniLuan/vigil/internal/platform/database/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	queries *generated.Queries
	logger  *slog.Logger
}

type service struct {
	Name        string
	State       string
	LastChecked string
	Uptime      string
}

type page struct {
	Services       []service
	Updated        string
	AllOperational bool
}

func New(pool *pgxpool.Pool, logger *slog.Logger) *Handler {
	return &Handler{queries: generated.New(pool), logger: logger}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /public/status", h.serve)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListPublicStatus(r.Context())
	if err != nil {
		h.logger.Error("load public status", "error", err)
		http.Error(w, "Status is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	data := page{Updated: time.Now().UTC().Format("2006-01-02 15:04 UTC"), AllOperational: len(rows) > 0}
	for _, row := range rows {
		entry := service{Name: row.Name, State: row.State, LastChecked: "Awaiting first check", Uptime: "N/A"}
		if row.LastCheckedAt.Valid {
			entry.LastChecked = row.LastCheckedAt.Time.UTC().Format("2006-01-02 15:04 UTC")
		}
		if row.Checks24h > 0 {
			entry.Uptime = uptimePercent(row.Successes24h, row.Checks24h)
		}
		if row.State != "up" {
			data.AllOperational = false
		}
		data.Services = append(data.Services, entry)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := statusTemplate.Execute(w, data); err != nil {
		h.logger.Error("render public status", "error", err)
	}
}
