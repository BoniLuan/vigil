package scheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDurableExecutionIdentityAndRecovery(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	created := createScheduledMonitor(t, ctx, pool, "Identity")
	service := NewService(pool)
	workerA := mustWorkerID(t)
	claimed, err := service.ClaimDue(ctx, workerA, ClaimOptions{BatchSize: 5, LeaseDuration: time.Minute})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimDue() = %+v, %v", claimed, err)
	}
	first := claimed[0]
	if first.ID.Version() != uuid.Version(7) || first.MonitorID != created.ID || first.Status != StatusClaimed ||
		first.LeaseOwner == nil || *first.LeaseOwner != workerA || first.ClaimCount != 1 {
		t.Fatalf("claimed execution = %+v", first)
	}
	persisted, err := NewService(pool).Get(ctx, first.ID)
	if err != nil || persisted.ID != first.ID || !persisted.ScheduledAt.Equal(first.ScheduledAt) {
		t.Fatalf("persisted execution = %+v, %v", persisted, err)
	}

	if _, err := pool.Exec(ctx, "UPDATE scheduled_executions SET lease_expires_at = now() - interval '1 second' WHERE id = $1", first.ID); err != nil {
		t.Fatal(err)
	}
	workerB := mustWorkerID(t)
	reclaimed, err := service.ClaimDue(ctx, workerB, ClaimOptions{BatchSize: 5, LeaseDuration: time.Minute})
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != first.ID || reclaimed[0].ClaimCount != 2 ||
		reclaimed[0].LeaseOwner == nil || *reclaimed[0].LeaseOwner != workerB {
		t.Fatalf("reclaimed execution = %+v, %v", reclaimed, err)
	}

	duplicateID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "INSERT INTO scheduled_executions (id, monitor_id, scheduled_at) VALUES ($1, $2, $3)", duplicateID, created.ID, first.ScheduledAt); err == nil {
		t.Fatal("duplicate logical execution was accepted")
	}
	if _, err := pool.Exec(ctx, "INSERT INTO scheduled_executions (id, monitor_id, scheduled_at) VALUES ($1, $2, $3)", duplicateID, created.ID, first.ScheduledAt.Add(time.Minute)); err != nil {
		t.Fatalf("distinct scheduled execution rejected: %v", err)
	}
}

func TestClaimDueEligibilityAndBatchBound(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	service := NewService(pool)

	for index := 0; index < 4; index++ {
		createScheduledMonitor(t, ctx, pool, fmt.Sprintf("Due %d", index))
	}
	future := createScheduledMonitor(t, ctx, pool, "Future")
	setNextCheck(t, ctx, pool, future.ID, time.Now().UTC().Add(time.Hour))
	paused := createScheduledMonitor(t, ctx, pool, "Paused")
	if _, err := monitorService.Pause(ctx, paused.ID); err != nil {
		t.Fatal(err)
	}
	archived := createScheduledMonitor(t, ctx, pool, "Archived")
	if err := monitorService.Delete(ctx, archived.ID); err != nil {
		t.Fatal(err)
	}

	claimed, err := service.ClaimDue(ctx, mustWorkerID(t), ClaimOptions{BatchSize: 2, LeaseDuration: time.Minute})
	if err != nil || len(claimed) != 2 {
		t.Fatalf("bounded ClaimDue() = %+v, %v", claimed, err)
	}
	for _, execution := range claimed {
		if execution.MonitorID == future.ID || execution.MonitorID == paused.ID || execution.MonitorID == archived.ID {
			t.Fatalf("ineligible monitor claimed: %+v", execution)
		}
	}
	var ineligibleCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM scheduled_executions WHERE monitor_id IN ($1, $2, $3)", future.ID, paused.ID, archived.ID).Scan(&ineligibleCount); err != nil || ineligibleCount != 0 {
		t.Fatalf("ineligible execution count = %d, %v", ineligibleCount, err)
	}
}

func TestConcurrentClaimersNeverShareExecution(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	for index := 0; index < 12; index++ {
		createScheduledMonitor(t, ctx, pool, fmt.Sprintf("Concurrent %d", index))
	}
	workers := []uuid.UUID{mustWorkerID(t), mustWorkerID(t)}
	type response struct {
		items []Execution
		err   error
	}
	responses := make(chan response, len(workers))
	var start sync.WaitGroup
	start.Add(1)
	for _, worker := range workers {
		go func(workerID uuid.UUID) {
			start.Wait()
			items, err := NewService(pool).ClaimDue(ctx, workerID, ClaimOptions{BatchSize: 12, LeaseDuration: time.Minute})
			responses <- response{items, err}
		}(worker)
	}
	start.Done()
	seen := map[uuid.UUID]uuid.UUID{}
	for range workers {
		response := <-responses
		if response.err != nil {
			t.Fatal(response.err)
		}
		for _, execution := range response.items {
			if previous, exists := seen[execution.ID]; exists {
				t.Fatalf("execution %s claimed by %s and %s", execution.ID, previous, *execution.LeaseOwner)
			}
			seen[execution.ID] = *execution.LeaseOwner
		}
	}
	if len(seen) != 12 {
		t.Fatalf("unique claimed executions = %d, want 12", len(seen))
	}
}

func TestActiveLeaseAndFutureExecutionCannotBeClaimed(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	created := createScheduledMonitor(t, ctx, pool, "Lease")
	service := NewService(pool)
	first, err := service.ClaimDue(ctx, mustWorkerID(t), ClaimOptions{BatchSize: 10, LeaseDuration: time.Minute})
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	second, err := service.ClaimDue(ctx, mustWorkerID(t), ClaimOptions{BatchSize: 10, LeaseDuration: time.Minute})
	if err != nil || len(second) != 0 {
		t.Fatalf("active lease was stolen: %+v, %v", second, err)
	}
	futureID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `INSERT INTO scheduled_executions (id, monitor_id, scheduled_at)
VALUES ($1, $2, now() + interval '1 hour')`, futureID, created.ID); err != nil {
		t.Fatal(err)
	}
	third, err := service.ClaimDue(ctx, mustWorkerID(t), ClaimOptions{BatchSize: 10, LeaseDuration: time.Minute})
	if err != nil || len(third) != 0 {
		t.Fatalf("future execution was claimed: %+v, %v", third, err)
	}
}

func TestDowntimeCreatesOneCatchUpAndAdvancesFutureBoundary(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	created := createScheduledMonitor(t, ctx, pool, "Catch up")
	overdue := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	setNextCheck(t, ctx, pool, created.ID, overdue)
	claimed, err := NewService(pool).ClaimDue(ctx, mustWorkerID(t), ClaimOptions{BatchSize: 10, LeaseDuration: time.Minute})
	if err != nil || len(claimed) != 1 || !claimed[0].ScheduledAt.Equal(overdue) {
		t.Fatalf("catch-up claim = %+v, %v", claimed, err)
	}
	var count int
	var next time.Time
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM scheduled_executions WHERE monitor_id = $1", created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT next_check_at FROM monitors WHERE id = $1", created.ID).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !next.After(time.Now().UTC()) || next.Sub(overdue)%time.Minute != 0 {
		t.Fatalf("count=%d next=%s overdue=%s", count, next, overdue)
	}
}

func TestPauseSkipsPendingExecution(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	created := createScheduledMonitor(t, ctx, pool, "Skip pending")
	executionID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, "INSERT INTO scheduled_executions (id, monitor_id, scheduled_at) VALUES ($1, $2, now())", executionID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := monitorService.Pause(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM scheduled_executions WHERE id = $1", executionID).Scan(&status); err != nil || status != StatusSkipped {
		t.Fatalf("pending execution status = %q, %v", status, err)
	}
	claimed, err := NewService(pool).ClaimDue(ctx, mustWorkerID(t), ClaimOptions{})
	if err != nil || len(claimed) != 0 {
		t.Fatalf("paused execution claimed = %+v, %v", claimed, err)
	}
}

func TestWorkerIdentityIsUniqueAndOptionsAreValidated(t *testing.T) {
	first := mustWorkerID(t)
	second := mustWorkerID(t)
	if first == second || first == uuid.Nil || second == uuid.Nil {
		t.Fatalf("worker identities = %s, %s", first, second)
	}
	pool := testutil.PostgreSQL(t)
	if _, err := NewService(pool).ClaimDue(context.Background(), uuid.Nil, ClaimOptions{}); err != ErrInvalidClaimOptions {
		t.Fatalf("nil worker error = %v", err)
	}
}

func createScheduledMonitor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) monitor.Monitor {
	t.Helper()
	created, err := monitor.NewService(monitor.NewStore(pool)).Create(ctx, monitor.CreateInput{
		Name: name, URL: "https://example.com", Interval: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func setNextCheck(t *testing.T, ctx context.Context, pool *pgxpool.Pool, monitorID uuid.UUID, value time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, "UPDATE monitors SET next_check_at = $2 WHERE id = $1", monitorID, value); err != nil {
		t.Fatal(err)
	}
}

func mustWorkerID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := NewWorkerID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
