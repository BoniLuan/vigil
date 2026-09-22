package publicstatus

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
)

func TestPublicStatusFiltersPrivateAndArchivedConfiguration(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitors := monitor.NewService(monitor.NewStore(pool))
	private, err := monitors.Create(ctx, monitor.CreateInput{Name: "Secret service", URL: "https://private.example/health?token=status-private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	public, err := monitors.Create(ctx, monitor.CreateInput{Name: "Public service", URL: "https://public.example/health?token=status-public-secret", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := monitors.Create(ctx, monitor.CreateInput{Name: "Archived service", URL: "https://archived.example", Public: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := monitors.Delete(ctx, archived.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	status := 200
	_, _, err = checkresult.NewService(pool).ApplyResult(ctx, check.Result{MonitorID: public.ID, StartedAt: now.Add(-time.Second), FinishedAt: now, Duration: time.Second, Outcome: check.OutcomeSuccess, StatusCode: &status})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public/status", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "Public service") || !strings.Contains(body, "100.00%") {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
	for _, forbidden := range []string{"Secret service", "Archived service", "private.example", "public.example", "status-public-secret", "status-private-secret", private.ID.String(), public.ID.String(), "/monitors/", "Dialed IP"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("public response leaked %q", forbidden)
		}
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("public status should not be cached")
	}
}

func TestEmptyPublicStatus(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	mux := http.NewServeMux()
	New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/public/status", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "No public services") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
