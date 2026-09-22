// Package publicstatus serves a deliberately small, public-only projection.
package publicstatus

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	generated "github.com/BoniLuan/vigil/internal/platform/database/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	Name        string
	State       string
	LastChecked string
	Uptime      string
}

type Snapshot struct {
	Services       []Service
	Updated        string
	AllOperational bool
	Total          int
	Operational    int
}

type Reader struct{ queries *generated.Queries }

func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{queries: generated.New(pool)} }

func (r *Reader) Snapshot(ctx context.Context) (Snapshot, error) {
	rows, err := r.queries.ListPublicStatus(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	data := Snapshot{Updated: time.Now().UTC().Format("2006-01-02 15:04 UTC"), AllOperational: len(rows) > 0}
	for _, row := range rows {
		entry := Service{Name: row.Name, State: row.State, LastChecked: "Awaiting first check", Uptime: "N/A"}
		if row.LastCheckedAt.Valid {
			entry.LastChecked = row.LastCheckedAt.Time.UTC().Format("2006-01-02 15:04 UTC")
		}
		if row.Checks24h > 0 {
			entry.Uptime = uptimePercent(row.Successes24h, row.Checks24h)
		}
		if row.State != "up" {
			data.AllOperational = false
		}
		if row.State == "up" {
			data.Operational++
		}
		data.Services = append(data.Services, entry)
		data.Total++
	}
	return data, nil
}

type Handler struct {
	reader *Reader
	logger *slog.Logger
}

func New(pool *pgxpool.Pool, logger *slog.Logger) *Handler {
	return &Handler{reader: NewReader(pool), logger: logger}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /public/status", h.serve)
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	data, err := h.reader.Snapshot(r.Context())
	if err != nil {
		h.logger.Error("load public status", "error", err)
		http.Error(w, "Status is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := statusTemplate.Execute(w, data); err != nil {
		h.logger.Error("render public status", "error", err)
	}
}
