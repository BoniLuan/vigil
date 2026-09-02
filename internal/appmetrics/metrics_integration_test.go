package appmetrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BoniLuan/vigil/internal/testutil"
)

func TestMetricsExposeCuratedDatabaseAndSchedulerFamilies(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	metrics := New(pool, BuildInfo{Version: "dev", Commit: "none", Role: "api"})
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("status=%d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	for _, family := range []string{"vigil_db_pool_connections", "vigil_scheduler_lag_seconds"} {
		if !strings.Contains(recorder.Body.String(), family) {
			t.Errorf("missing metric family %s", family)
		}
	}
}
