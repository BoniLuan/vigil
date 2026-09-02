package appmetrics

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
)

func TestWorkerMetricsObserveBoundedRuntimeSignals(t *testing.T) {
	metrics := NewWorker(nil, BuildInfo{Version: "0.1.0", Commit: "abc", Role: "worker"}, 5)
	metrics.ObserveClaim(2, nil)
	metrics.ObserveClaim(0, nil)
	metrics.ObserveClaim(0, errors.New("database unavailable"))
	metrics.CheckStarted()
	metrics.ObserveCheck(check.OutcomeSuccess, 125*time.Millisecond)
	metrics.CheckStopped()
	metrics.CompletionFailed()
	metrics.PanicRecovered()
	metrics.LeaseStartRejected()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"vigil_scheduler_claim_attempts_total", `result="claimed"`, `result="empty"`, `result="error"`,
		"vigil_scheduler_claimed_executions_total 2", `vigil_checks_completed_total{outcome="success"} 1`,
		"vigil_check_duration_seconds_count", "vigil_worker_active_checks 0", "vigil_worker_capacity 5",
		"vigil_worker_completion_failures_total 1", "vigil_worker_panic_recoveries_total 1", "vigil_worker_lease_start_rejections_total 1",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics missing %q", expected)
		}
	}
	for _, prohibited := range []string{"monitor_id", "execution_id", "url=", "hostname=", "error="} {
		if strings.Contains(body, prohibited) {
			t.Errorf("prohibited label %q present", prohibited)
		}
	}
}
