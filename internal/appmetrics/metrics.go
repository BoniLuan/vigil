package appmetrics

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type BuildInfo struct{ Version, Commit, Role string }

type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func New(pool *pgxpool.Pool, build BuildInfo) *Metrics {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vigil_http_requests_total", Help: "HTTP requests handled by Vigil."}, []string{"method", "route", "status_class"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "vigil_http_request_duration_seconds", Help: "HTTP request duration in seconds.", Buckets: prometheus.DefBuckets}, []string{"method", "route"})
	buildMetric := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "vigil_build_info", Help: "Vigil process build metadata."}, []string{"version", "commit", "role"})
	buildMetric.WithLabelValues(fallback(build.Version, "dev"), fallback(build.Commit, "none"), fallback(build.Role, "unknown")).Set(1)
	registry.MustRegister(requests, duration, buildMetric)
	if pool != nil {
		for _, state := range []string{"total", "acquired", "idle"} {
			state := state
			registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "vigil_db_pool_connections", Help: "Current pgx pool connections by state.", ConstLabels: prometheus.Labels{"state": state}}, func() float64 {
				stats := pool.Stat()
				switch state {
				case "acquired":
					return float64(stats.AcquiredConns())
				case "idle":
					return float64(stats.IdleConns())
				default:
					return float64(stats.TotalConns())
				}
			}))
		}
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "vigil_scheduler_lag_seconds", Help: "Seconds the oldest enabled due monitor is behind its intended schedule; zero when none are due."}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			var lag float64
			err := pool.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM (transaction_timestamp() - min(next_check_at))), 0)::double precision FROM monitors WHERE enabled AND archived_at IS NULL AND next_check_at <= transaction_timestamp()`).Scan(&lag)
			if err != nil {
				return math.NaN()
			}
			if lag < 0 {
				return 0
			}
			return lag
		}))
	}
	return &Metrics{registry: registry, requests: requests, duration: duration}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		statusClass := strconv.Itoa(recorder.status/100) + "xx"
		m.requests.WithLabelValues(r.Method, route, statusClass).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(started).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status, r.wroteHeader = status, true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}
func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
