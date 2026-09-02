package appmetrics

import (
	"net/http"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type WorkerMetrics struct {
	registry           *prometheus.Registry
	claims             *prometheus.CounterVec
	claimed            prometheus.Counter
	checks             *prometheus.CounterVec
	checkDuration      *prometheus.HistogramVec
	active             prometheus.Gauge
	completionFailures prometheus.Counter
	panics             prometheus.Counter
	leaseRejected      prometheus.Counter
}

func NewWorker(pool *pgxpool.Pool, build BuildInfo, capacity int) *WorkerMetrics {
	registry := prometheus.NewRegistry()
	metrics := &WorkerMetrics{
		registry:           registry,
		claims:             prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vigil_scheduler_claim_attempts_total", Help: "Scheduler claim polls by result."}, []string{"result"}),
		claimed:            prometheus.NewCounter(prometheus.CounterOpts{Name: "vigil_scheduler_claimed_executions_total", Help: "Executions claimed by this worker process."}),
		checks:             prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vigil_checks_completed_total", Help: "HTTP checks executed by outcome."}, []string{"outcome"}),
		checkDuration:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "vigil_check_duration_seconds", Help: "HTTP check duration by outcome.", Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 20, 30}}, []string{"outcome"}),
		active:             prometheus.NewGauge(prometheus.GaugeOpts{Name: "vigil_worker_active_checks", Help: "Checks currently executing."}),
		completionFailures: prometheus.NewCounter(prometheus.CounterOpts{Name: "vigil_worker_completion_failures_total", Help: "Failed durable completion attempts."}),
		panics:             prometheus.NewCounter(prometheus.CounterOpts{Name: "vigil_worker_panic_recoveries_total", Help: "Recovered per-job panics."}),
		leaseRejected:      prometheus.NewCounter(prometheus.CounterOpts{Name: "vigil_worker_lease_start_rejections_total", Help: "Claimed executions not started because lease time was insufficient."}),
	}
	capacityMetric := prometheus.NewGauge(prometheus.GaugeOpts{Name: "vigil_worker_capacity", Help: "Configured maximum concurrent checks."})
	capacityMetric.Set(float64(capacity))
	buildMetric := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "vigil_build_info", Help: "Vigil process build metadata."}, []string{"version", "commit", "role"})
	buildMetric.WithLabelValues(fallback(build.Version, "dev"), fallback(build.Commit, "none"), "worker").Set(1)
	registry.MustRegister(metrics.claims, metrics.claimed, metrics.checks, metrics.checkDuration, metrics.active,
		metrics.completionFailures, metrics.panics, metrics.leaseRejected, capacityMetric, buildMetric)
	registerDatabaseMetrics(registry, pool)
	return metrics
}

func (m *WorkerMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
func (m *WorkerMetrics) ObserveClaim(count int, err error) {
	result := "empty"
	if err != nil {
		result = "error"
	} else if count > 0 {
		result = "claimed"
		m.claimed.Add(float64(count))
	}
	m.claims.WithLabelValues(result).Inc()
}
func (m *WorkerMetrics) CheckStarted() { m.active.Inc() }
func (m *WorkerMetrics) CheckStopped() { m.active.Dec() }
func (m *WorkerMetrics) ObserveCheck(outcome check.Outcome, duration time.Duration) {
	label := string(outcome)
	m.checks.WithLabelValues(label).Inc()
	m.checkDuration.WithLabelValues(label).Observe(duration.Seconds())
}
func (m *WorkerMetrics) CompletionFailed()   { m.completionFailures.Inc() }
func (m *WorkerMetrics) PanicRecovered()     { m.panics.Inc() }
func (m *WorkerMetrics) LeaseStartRejected() { m.leaseRejected.Inc() }
