package checkresult

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
)

func TestMonitorSummariesUseFixedCompletedCheckWindows(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	created := createMonitor(t, ctx, monitorService, "Summary windows", 1, 1)
	service := NewService(pool)

	empty, err := service.Summaries(ctx, created.ID)
	if err != nil || len(empty) != 4 {
		t.Fatalf("empty summaries=%+v error=%v", empty, err)
	}
	for _, summary := range empty {
		if summary.UptimePercent != nil || summary.AverageLatency != nil || summary.CompletedChecks != 0 {
			t.Fatalf("empty summary=%+v", summary)
		}
	}

	now := time.Now().UTC()
	applySummaryResult(t, ctx, service, created.ID, now.Add(-time.Hour), 100*time.Millisecond, check.OutcomeSuccess)
	applySummaryResult(t, ctx, service, created.ID, now.Add(-2*time.Hour), 300*time.Millisecond, check.OutcomeConnectionError)
	applySummaryResult(t, ctx, service, created.ID, now.Add(-8*24*time.Hour), 500*time.Millisecond, check.OutcomeSuccess)
	applySummaryResult(t, ctx, service, created.ID, now.Add(-100*24*time.Hour), 900*time.Millisecond, check.OutcomeSuccess)

	summaries, err := service.Summaries(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSummary(t, summaries[0], "24h", 2, 1, 50, 200*time.Millisecond)
	assertSummary(t, summaries[1], "7d", 2, 1, 50, 200*time.Millisecond)
	assertSummary(t, summaries[2], "30d", 3, 2, 200.0/3, 300*time.Millisecond)
	assertSummary(t, summaries[3], "90d", 3, 2, 200.0/3, 300*time.Millisecond)

	if err := monitorService.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	archived, err := service.Summaries(ctx, created.ID)
	if err != nil || archived[0].CompletedChecks != 2 {
		t.Fatalf("archived summaries=%+v error=%v", archived, err)
	}
}

func applySummaryResult(t *testing.T, ctx context.Context, service *Service, monitorID uuid.UUID, finished time.Time, duration time.Duration, outcome check.Outcome) {
	t.Helper()
	result := check.Result{MonitorID: monitorID, StartedAt: finished.Add(-duration), FinishedAt: finished, Duration: duration, Outcome: outcome}
	if outcome != check.OutcomeSuccess {
		detail := check.NewErrorDetail(check.ErrorCodeConnectionFailed, "connection failed")
		result.Error = &detail
	}
	if _, _, err := service.ApplyResult(ctx, result); err != nil {
		t.Fatal(err)
	}
}

func assertSummary(t *testing.T, got Summary, window string, completed, successful int64, uptime float64, latency time.Duration) {
	t.Helper()
	if got.Window != window || got.CompletedChecks != completed || got.SuccessfulChecks != successful || got.UptimePercent == nil || math.Abs(*got.UptimePercent-uptime) > 0.01 || got.AverageLatency == nil || *got.AverageLatency != latency {
		t.Fatalf("summary=%+v", got)
	}
}
