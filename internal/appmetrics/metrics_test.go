package appmetrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposeBuildAndNormalizedHTTPLabels(t *testing.T) {
	metrics := New(nil, BuildInfo{Version: "1.2.3", Commit: "abc123", Role: "api"})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/monitors/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	id := "0198f79a-9432-7000-8000-000000000001"
	recorder := httptest.NewRecorder()
	metrics.Instrument(mux).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+id, nil))

	scrape := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	for _, expected := range []string{"vigil_build_info", `version="1.2.3"`, "vigil_http_requests_total", `route="GET /api/v1/monitors/{id}"`, `status_class="2xx"`, "vigil_http_request_duration_seconds"} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics missing %q", expected)
		}
	}
	if strings.Contains(body, id) {
		t.Fatal("raw monitor ID appeared in metric labels")
	}
}
