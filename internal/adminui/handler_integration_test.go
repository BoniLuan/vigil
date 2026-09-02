package adminui

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
)

func TestAdminMonitorLifecycleAndHistory(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	resultService := checkresult.NewService(pool)
	handler, err := New(monitorService, resultService, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)

	response := request(t, mux, http.MethodGet, "/monitors", nil)
	assertResponse(t, response, http.StatusOK, "No monitors configured")

	invalid := validForm()
	invalid.Set("name", "")
	invalid.Set("url", "not-a-url")
	response = request(t, mux, http.MethodPost, "/monitors", invalid)
	assertResponse(t, response, http.StatusBadRequest, "not-a-url")
	assertResponse(t, response, http.StatusBadRequest, "must not be empty")

	createdForm := validForm()
	response = request(t, mux, http.MethodPost, "/monitors", createdForm)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	location := response.Header().Get("Location")
	parts := strings.Split(location, "/")
	id, err := uuid.Parse(parts[len(parts)-1])
	if err != nil {
		t.Fatalf("location=%q", location)
	}

	response = request(t, mux, http.MethodGet, location, nil)
	assertResponse(t, response, http.StatusOK, "pending")
	assertResponse(t, response, http.StatusOK, "No completed checks yet")
	assertResponse(t, response, http.StatusOK, "N/A")

	configured, err := monitorService.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	status := 200
	if _, _, err := resultService.ApplyResult(context.Background(), check.Result{MonitorID: id, StartedAt: now.Add(-82 * time.Millisecond), FinishedAt: now, Duration: 82 * time.Millisecond, Outcome: check.OutcomeSuccess, StatusCode: &status}); err != nil {
		t.Fatal(err)
	}
	response = request(t, mux, http.MethodGet, location, nil)
	assertResponse(t, response, http.StatusOK, "state-up")
	assertResponse(t, response, http.StatusOK, "100.00%")
	assertResponse(t, response, http.StatusOK, "82 ms")
	assertResponse(t, response, http.StatusOK, "success")
	if strings.Contains(response.Body.String(), configured.URL+"?") {
		t.Fatal("unexpected unsanitized query in detail")
	}

	edit := validForm()
	edit.Set("name", "Edited Service")
	edit.Set("method", "HEAD")
	edit.Set("public", "on")
	response = request(t, mux, http.MethodPost, location, edit)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("edit status=%d body=%s", response.Code, response.Body.String())
	}
	updated, _ := monitorService.Get(context.Background(), id)
	if updated.Name != "Edited Service" || updated.HTTPMethod != monitor.MethodHEAD || !updated.Public {
		t.Fatalf("updated=%+v", updated)
	}

	response = request(t, mux, http.MethodPost, location+"/pause", url.Values{})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("pause status=%d", response.Code)
	}
	response = request(t, mux, http.MethodGet, location, nil)
	assertResponse(t, response, http.StatusOK, "state-paused")
	response = request(t, mux, http.MethodPost, location+"/resume", url.Values{})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("resume status=%d", response.Code)
	}

	response = request(t, mux, http.MethodPost, location+"/archive", url.Values{})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed archive status=%d", response.Code)
	}
	response = request(t, mux, http.MethodPost, location+"/archive", url.Values{"confirm": {"archive"}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("archive status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, mux, http.MethodGet, "/monitors", nil)
	if strings.Contains(response.Body.String(), "Edited Service") {
		t.Fatal("archived monitor appears in active list")
	}
	response = request(t, mux, http.MethodGet, location, nil)
	assertResponse(t, response, http.StatusOK, "Archived monitor")
	assertResponse(t, response, http.StatusOK, "success")
	response = request(t, mux, http.MethodPost, location+"/resume", url.Values{})
	if response.Code != http.StatusConflict {
		t.Fatalf("archived resume status=%d", response.Code)
	}
}

func TestAdminListRendersOperationalProjectionWithoutQueryString(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	created, err := monitorService.Create(context.Background(), monitor.CreateInput{Name: "Projected", URL: "https://example.com/health?token=sensitive", FailureThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	status := 503
	detail := check.NewErrorDetail(check.ErrorCodeUnexpectedStatus, "status failed")
	_, _, err = checkresult.NewService(pool).ApplyResult(context.Background(), check.Result{MonitorID: created.ID, StartedAt: now.Add(-10 * time.Millisecond), FinishedAt: now, Duration: 10 * time.Millisecond, Outcome: check.OutcomeHTTPFailure, StatusCode: &status, Error: &detail})
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := New(monitorService, checkresult.NewService(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)
	response := request(t, mux, http.MethodGet, "/monitors", nil)
	assertResponse(t, response, http.StatusOK, "Projected")
	assertResponse(t, response, http.StatusOK, "state-down")
	assertResponse(t, response, http.StatusOK, "http_failure")
	assertResponse(t, response, http.StatusOK, "10 ms")
	if strings.Contains(response.Body.String(), "sensitive") {
		t.Fatal("query value rendered in monitor list")
	}
}

func validForm() url.Values {
	return url.Values{"name": {"Example API"}, "slug": {"example-api"}, "url": {"https://example.com/health"}, "method": {"GET"}, "status_min": {"200"}, "status_max": {"200"}, "interval_seconds": {"60"}, "timeout_ms": {"2000"}, "failure_threshold": {"1"}, "recovery_threshold": {"1"}}
}
func request(t *testing.T, handler http.Handler, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request := httptest.NewRequest(method, target, body)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
func assertResponse(t *testing.T, response *httptest.ResponseRecorder, status int, text string) {
	t.Helper()
	if response.Code != status || !strings.Contains(response.Body.String(), text) {
		t.Fatalf("status=%d want=%d missing=%q body=%s", response.Code, status, text, response.Body.String())
	}
}
