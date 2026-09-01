package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
)

func TestMonitorAPI(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	service := monitor.NewService(monitor.NewStore(pool))
	mux := http.NewServeMux()
	New(service, slog.New(slog.NewTextHandler(io.Discard, nil))).Register(mux)

	created := perform(t, mux, http.MethodPost, "/api/v1/monitors", `{
		"name":"FinPulse API","url":"https://example.com/health",
		"interval_seconds":60,"timeout_ms":2000,"public":true
	}`)
	if created.Code != http.StatusCreated || created.Header().Get("Location") == "" {
		t.Fatalf("create status = %d, location = %q, body = %s", created.Code, created.Header().Get("Location"), created.Body.String())
	}
	var value monitorResponse
	decodeResponse(t, created, &value)
	if value.Slug != "finpulse-api" || value.State != monitor.StatePending || !value.Public {
		t.Fatalf("created response = %+v", value)
	}
	id := value.ID.String()

	invalid := perform(t, mux, http.MethodPost, "/api/v1/monitors", `{"name":"Bad","url":"file:///etc/passwd"}`)
	if invalid.Code != http.StatusBadRequest || invalid.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("invalid create status = %d, content-type = %q, body = %s", invalid.Code, invalid.Header().Get("Content-Type"), invalid.Body.String())
	}
	duplicate := perform(t, mux, http.MethodPost, "/api/v1/monitors", `{"name":"Other","slug":"finpulse-api","url":"https://example.net"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, body = %s", duplicate.Code, duplicate.Body.String())
	}

	got := perform(t, mux, http.MethodGet, "/api/v1/monitors/"+id, "")
	if got.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", got.Code, got.Body.String())
	}
	listed := perform(t, mux, http.MethodGet, "/api/v1/monitors?limit=10&offset=0", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"finpulse-api"`) {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}

	patched := perform(t, mux, http.MethodPatch, "/api/v1/monitors/"+id, `{"description":null,"public":false,"timeout_ms":1500}`)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", patched.Code, patched.Body.String())
	}
	decodeResponse(t, patched, &value)
	if value.Description != nil || value.Public || value.TimeoutMS != 1500 || value.URL != "https://example.com/health" {
		t.Fatalf("patched response = %+v", value)
	}

	paused := perform(t, mux, http.MethodPost, "/api/v1/monitors/"+id+"/pause", "")
	decodeResponse(t, paused, &value)
	if paused.Code != http.StatusOK || value.Enabled || value.State != monitor.StatePaused {
		t.Fatalf("pause status = %d, response = %+v", paused.Code, value)
	}
	resumed := perform(t, mux, http.MethodPost, "/api/v1/monitors/"+id+"/resume", "")
	decodeResponse(t, resumed, &value)
	if resumed.Code != http.StatusOK || !value.Enabled || value.State != monitor.StatePending {
		t.Fatalf("resume status = %d, response = %+v", resumed.Code, value)
	}

	missing := perform(t, mux, http.MethodGet, "/api/v1/monitors/00000000-0000-0000-0000-000000000000", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body = %s", missing.Code, missing.Body.String())
	}
	invalidID := perform(t, mux, http.MethodGet, "/api/v1/monitors/not-a-uuid", "")
	if invalidID.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d", invalidID.Code)
	}

	deleted := perform(t, mux, http.MethodDelete, "/api/v1/monitors/"+id, "")
	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	archived := perform(t, mux, http.MethodGet, "/api/v1/monitors/"+id, "")
	decodeResponse(t, archived, &value)
	if archived.Code != http.StatusOK || value.ArchivedAt == nil || value.Enabled || value.Public || value.State != monitor.StatePaused {
		t.Fatalf("archived get status = %d, response = %+v", archived.Code, value)
	}
	activeAfterArchive := perform(t, mux, http.MethodGet, "/api/v1/monitors?limit=10", "")
	if strings.Contains(activeAfterArchive.Body.String(), "finpulse-api") {
		t.Fatalf("archived monitor remained in active list: %s", activeAfterArchive.Body.String())
	}
	resumeArchived := perform(t, mux, http.MethodPost, "/api/v1/monitors/"+id+"/resume", "")
	if resumeArchived.Code != http.StatusConflict {
		t.Fatalf("archived resume status = %d, body = %s", resumeArchived.Code, resumeArchived.Body.String())
	}
	deletedAgain := perform(t, mux, http.MethodDelete, "/api/v1/monitors/"+id, "")
	if deletedAgain.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d", deletedAgain.Code)
	}
}

func perform(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, recorder *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
}
