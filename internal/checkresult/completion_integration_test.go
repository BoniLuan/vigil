package checkresult

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/scheduler"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCompleteExecutionIsAtomicAndIdempotent(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	created := createMonitor(t, ctx, monitorService, "Complete", 2, 2)
	worker := completionWorkerID(t)
	claims, err := scheduler.NewService(pool).ClaimDue(ctx, worker, scheduler.ClaimOptions{BatchSize: 5, LeaseDuration: time.Minute})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	service := NewService(pool)
	completed := successfulResult(created.ID)
	first, err := service.CompleteExecution(ctx, claims[0].ID, worker, completed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CompleteExecution(ctx, claims[0].ID, worker, completed)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.ExecutionID == nil || *first.ExecutionID != claims[0].ID {
		t.Fatalf("completion results = %+v / %+v", first, second)
	}
	var resultCount int
	var state, executionStatus string
	var successes int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM check_results WHERE execution_id = $1", claims[0].ID).Scan(&resultCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT state, consecutive_successes FROM monitor_states WHERE monitor_id = $1", created.ID).Scan(&state, &successes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM scheduled_executions WHERE id = $1", claims[0].ID).Scan(&executionStatus); err != nil {
		t.Fatal(err)
	}
	if resultCount != 1 || state != monitor.StateUp || successes != 1 || executionStatus != scheduler.StatusCompleted {
		t.Fatalf("count=%d state=%s successes=%d execution=%s", resultCount, state, successes, executionStatus)
	}
}

func TestCompleteExecutionRejectsLostLease(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	created := createMonitor(t, ctx, monitor.NewService(monitor.NewStore(pool)), "Lease completion", 1, 1)
	worker := completionWorkerID(t)
	claims, err := scheduler.NewService(pool).ClaimDue(ctx, worker, scheduler.ClaimOptions{LeaseDuration: time.Minute})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	service := NewService(pool)
	if _, err := service.CompleteExecution(ctx, claims[0].ID, completionWorkerID(t), successfulResult(created.ID)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong-owner completion error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE scheduled_executions SET lease_expires_at = now() - interval '1 second' WHERE id = $1", claims[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteExecution(ctx, claims[0].ID, worker, successfulResult(created.ID)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired completion error = %v", err)
	}
}

func TestOutOfOrderCompletionRetainsHistoryWithoutRollingProjectionBack(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	created := createMonitor(t, ctx, monitor.NewService(monitor.NewStore(pool)), "Ordering", 1, 1)
	worker := completionWorkerID(t)
	olderAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	newerAt := olderAt.Add(time.Minute)
	olderID := insertClaimedExecution(t, ctx, pool, created.ID, worker, olderAt)
	newerID := insertClaimedExecution(t, ctx, pool, created.ID, worker, newerAt)
	service := NewService(pool)
	newerResult, err := service.CompleteExecution(ctx, newerID, worker, successfulResult(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteExecution(ctx, olderID, worker, failedResult(created.ID, check.OutcomeConnectionError, check.ErrorCodeConnectionFailed)); err != nil {
		t.Fatal(err)
	}
	var state, outcome string
	var successes, failures int64
	var lastResultID uuid.UUID
	var lastScheduled time.Time
	if err := pool.QueryRow(ctx, `SELECT state, last_outcome, consecutive_successes, consecutive_failures,
last_check_result_id, last_applied_scheduled_at FROM monitor_states WHERE monitor_id = $1`, created.ID).
		Scan(&state, &outcome, &successes, &failures, &lastResultID, &lastScheduled); err != nil {
		t.Fatal(err)
	}
	var historyCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM check_results WHERE monitor_id = $1", created.ID).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 2 || state != monitor.StateUp || outcome != string(check.OutcomeSuccess) ||
		successes != 1 || failures != 0 || lastResultID != newerResult.ID || !lastScheduled.Equal(newerAt) {
		t.Fatalf("history=%d state=%s outcome=%s successes=%d failures=%d result=%s schedule=%s", historyCount, state, outcome, successes, failures, lastResultID, lastScheduled)
	}
}

func TestClaimedCompletionAfterPauseOrArchiveStaysPaused(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	service := NewService(pool)
	for _, archived := range []bool{false, true} {
		name := "Paused completion"
		if archived {
			name = "Archived completion"
		}
		t.Run(name, func(t *testing.T) {
			created := createMonitor(t, ctx, monitorService, name, 1, 1)
			worker := completionWorkerID(t)
			claims, err := scheduler.NewService(pool).ClaimDue(ctx, worker, scheduler.ClaimOptions{LeaseDuration: time.Minute})
			if err != nil || len(claims) != 1 {
				t.Fatalf("claims = %+v, %v", claims, err)
			}
			if archived {
				err = monitorService.Delete(ctx, created.ID)
			} else {
				_, err = monitorService.Pause(ctx, created.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			stored, err := service.CompleteExecution(ctx, claims[0].ID, worker, successfulResult(created.ID))
			if err != nil {
				t.Fatal(err)
			}
			var state string
			var successes int64
			if err := pool.QueryRow(ctx, "SELECT state, consecutive_successes FROM monitor_states WHERE monitor_id = $1", created.ID).Scan(&state, &successes); err != nil {
				t.Fatal(err)
			}
			if stored.ExecutionID == nil || state != monitor.StatePaused || successes != 0 {
				t.Fatalf("stored=%+v state=%s successes=%d", stored, state, successes)
			}
		})
	}
}

func TestCompleteExecutionRollsBackResultAndExecutionState(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	created := createMonitor(t, ctx, monitor.NewService(monitor.NewStore(pool)), "Completion atomic", 1, 1)
	worker := completionWorkerID(t)
	claims, err := scheduler.NewService(pool).ClaimDue(ctx, worker, scheduler.ClaimOptions{LeaseDuration: time.Minute})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims = %+v, %v", claims, err)
	}
	if _, err := pool.Exec(ctx, `
CREATE FUNCTION vigil_test_reject_scheduled_projection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'forced scheduled projection failure'; END $$;
CREATE TRIGGER vigil_test_reject_scheduled_projection BEFORE UPDATE ON monitor_states
FOR EACH ROW EXECUTE FUNCTION vigil_test_reject_scheduled_projection()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS vigil_test_reject_scheduled_projection ON monitor_states")
		_, _ = pool.Exec(context.Background(), "DROP FUNCTION IF EXISTS vigil_test_reject_scheduled_projection()")
	})
	if _, err := NewService(pool).CompleteExecution(ctx, claims[0].ID, worker, successfulResult(created.ID)); err == nil {
		t.Fatal("completion succeeded despite projection failure")
	}
	var count int
	var status string
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM check_results WHERE execution_id = $1", claims[0].ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM scheduled_executions WHERE id = $1", claims[0].ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if count != 0 || status != scheduler.StatusClaimed {
		t.Fatalf("result count=%d execution status=%s", count, status)
	}
}

func insertClaimedExecution(t *testing.T, ctx context.Context, pool *pgxpool.Pool, monitorID, workerID uuid.UUID, scheduledAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(ctx, `INSERT INTO scheduled_executions
(id, monitor_id, scheduled_at, status, lease_owner, lease_expires_at, claim_count)
VALUES ($1, $2, $3, 'claimed', $4, now() + interval '1 minute', 1)`, id, monitorID, scheduledAt, workerID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func completionWorkerID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := scheduler.NewWorkerID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
