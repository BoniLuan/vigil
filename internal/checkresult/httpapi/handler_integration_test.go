package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
)

func TestListCheckHistoryAPI(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	resultService := checkresult.NewService(pool)
	created, err := monitorService.Create(context.Background(), monitor.CreateInput{Name: "API history", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		started := time.Now().UTC().Add(time.Duration(index) * time.Second)
		detail := check.NewErrorDetail(check.ErrorCodeConnectionFailed, "safe failure")
		result := check.Result{
			MonitorID: created.ID, StartedAt: started, FinishedAt: started.Add(10 * time.Millisecond),
			Duration: 10 * time.Millisecond, Outcome: check.OutcomeConnectionError, Error: &detail,
		}
		if _, _, err := resultService.ApplyResult(context.Background(), result); err != nil {
			t.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	New(resultService, slog.New(slog.NewTextHandler(io.Discard, nil))).Register(mux)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/monitors/"+created.ID.String()+"/checks?limit=1&offset=1", nil)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Items  []resultResponse `json:"items"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Limit != 1 || body.Offset != 1 || body.Items[0].MonitorID != created.ID ||
		body.Items[0].ErrorCode == nil || *body.Items[0].ErrorCode != check.ErrorCodeConnectionFailed ||
		body.Items[0].ErrorDescription == nil || *body.Items[0].ErrorDescription != "safe failure" {
		t.Fatalf("body = %+v", body)
	}
}

func TestListCheckHistoryAPIErrors(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	mux := http.NewServeMux()
	New(checkresult.NewService(pool), slog.New(slog.NewTextHandler(io.Discard, nil))).Register(mux)

	tests := []struct {
		name string
		path string
		want int
	}{
		{"invalid id", "/api/v1/monitors/not-a-uuid/checks", http.StatusBadRequest},
		{"missing monitor", "/api/v1/monitors/" + uuid.Must(uuid.NewV7()).String() + "/checks", http.StatusNotFound},
		{"invalid pagination", "/api/v1/monitors/" + uuid.Must(uuid.NewV7()).String() + "/checks?limit=101", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != test.want || recorder.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
