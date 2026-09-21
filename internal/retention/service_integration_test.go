package retention

import (
	"context"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
)

func TestRunOncePrunesOldHistoryAndCompletedLedger(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	created, err := monitor.NewService(monitor.NewStore(pool)).Create(ctx, monitor.CreateInput{Name: "Retention fixture", URL: "https://example.com/health"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-24 * time.Hour)
	oldExecution, recentExecution, pendingExecution := uuid.New(), uuid.New(), uuid.New()
	oldResult, recentResult, manualOldResult := uuid.New(), uuid.New(), uuid.New()
	for _, fixture := range []struct {
		id     uuid.UUID
		at     time.Time
		status string
	}{
		{oldExecution, old, "completed"},
		{recentExecution, recent, "completed"},
		{pendingExecution, old.Add(-time.Minute), "pending"},
	} {
		var finished any
		if fixture.status == "completed" {
			finished = fixture.at.Add(time.Second)
		}
		_, err := pool.Exec(ctx, `INSERT INTO scheduled_executions (id, monitor_id, scheduled_at, status, finished_at)
			VALUES ($1, $2, $3, $4, $5)`, fixture.id, created.ID, fixture.at, fixture.status, finished)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct {
		id        uuid.UUID
		execution any
		at        time.Time
	}{
		{oldResult, oldExecution, old},
		{recentResult, recentExecution, recent},
		{manualOldResult, nil, old.Add(time.Minute)},
	} {
		_, err := pool.Exec(ctx, `INSERT INTO check_results (id, monitor_id, execution_id, started_at, finished_at, duration_ms, outcome, status_code)
			VALUES ($1, $2, $3, $4, $5, 100, 'success', 200)`, fixture.id, created.ID, fixture.execution, fixture.at, fixture.at.Add(100*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = pool.Exec(ctx, `UPDATE monitor_states SET state='up', last_check_result_id=$1 WHERE monitor_id=$2`, oldResult, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(pool, 90)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := service.RunOnce(ctx)
	if err != nil || counts.Results != 2 || counts.Executions != 1 {
		t.Fatalf("first prune=%+v error=%v", counts, err)
	}
	counts, err = service.RunOnce(ctx)
	if err != nil || counts != (Counts{}) {
		t.Fatalf("second prune=%+v error=%v", counts, err)
	}
	var results, executions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM check_results WHERE monitor_id=$1`, created.ID).Scan(&results); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scheduled_executions WHERE monitor_id=$1`, created.ID).Scan(&executions); err != nil {
		t.Fatal(err)
	}
	if results != 1 || executions != 2 {
		t.Fatalf("remaining results=%d executions=%d", results, executions)
	}
	var state string
	var lastID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT state, last_check_result_id FROM monitor_states WHERE monitor_id=$1`, created.ID).Scan(&state, &lastID); err != nil {
		t.Fatal(err)
	}
	if state != monitor.StateUp || lastID != nil {
		t.Fatalf("state=%q last_result_id=%v", state, lastID)
	}
}

func TestRetentionDaysValidation(t *testing.T) {
	for _, days := range []int32{0, -1, 3651} {
		if _, err := New(nil, days); err == nil {
			t.Fatalf("accepted invalid retention days %d", days)
		}
	}
}
