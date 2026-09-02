package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/scheduler"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestScheduledWorkerFlowPersistsResultsAndThresholdTransitions(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	created := createWorkerMonitor(t, ctx, monitorService, "Worker threshold", 2, 2)
	workerID := uuid.New()
	checker := &sequenceChecker{outcomes: []check.Outcome{
		check.OutcomeConnectionError, check.OutcomeConnectionError,
		check.OutcomeSuccess, check.OutcomeSuccess,
	}}
	runner := integrationRunner(t, pool, workerID, monitorService, checker)

	wantStates := []string{monitor.StatePending, monitor.StateDown, monitor.StateDown, monitor.StateUp}
	for index, want := range wantStates {
		execution := claimOne(t, ctx, pool, workerID, created.ID)
		runClaim(t, runner, execution)
		var state, executionStatus string
		var resultCount int
		if err := pool.QueryRow(ctx, "SELECT state FROM monitor_states WHERE monitor_id = $1", created.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT status FROM scheduled_executions WHERE id = $1", execution.ID).Scan(&executionStatus); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM check_results WHERE execution_id = $1", execution.ID).Scan(&resultCount); err != nil {
			t.Fatal(err)
		}
		if state != want || executionStatus != scheduler.StatusCompleted || resultCount != 1 {
			t.Fatalf("step %d state=%s execution=%s results=%d", index, state, executionStatus, resultCount)
		}
	}
}

func TestWorkerLateCompletionPreservesPauseAndArchive(t *testing.T) {
	for _, archive := range []bool{false, true} {
		name := "pause"
		if archive {
			name = "archive"
		}
		t.Run(name, func(t *testing.T) {
			pool := testutil.PostgreSQL(t)
			ctx := context.Background()
			monitorService := monitor.NewService(monitor.NewStore(pool))
			created := createWorkerMonitor(t, ctx, monitorService, "Worker "+name, 1, 1)
			workerID := uuid.New()
			claimed := claimOne(t, ctx, pool, workerID, created.ID)
			checker := newBlockingChecker(1)
			runner := integrationRunner(t, pool, workerID, monitorService, checker)
			done := runClaimAsync(runner, claimed)
			checker.waitStarted(t, 1)
			var err error
			if archive {
				err = monitorService.Delete(ctx, created.ID)
			} else {
				_, err = monitorService.Pause(ctx, created.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			checker.release <- struct{}{}
			<-done
			var state string
			var archivedAt *time.Time
			var count int
			if err := pool.QueryRow(ctx, "SELECT s.state, m.archived_at FROM monitor_states s JOIN monitors m ON m.id=s.monitor_id WHERE m.id=$1", created.ID).Scan(&state, &archivedAt); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM check_results WHERE execution_id=$1", claimed.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if state != monitor.StatePaused || count != 1 || (archive && archivedAt == nil) {
				t.Fatalf("state=%s archived=%v results=%d", state, archivedAt, count)
			}
		})
	}
}

func TestWorkerReclaimsExpiredExecutionWithoutDuplicateResult(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	created := createWorkerMonitor(t, ctx, monitorService, "Worker reclaim", 1, 1)
	workerA, workerB := uuid.New(), uuid.New()
	first := claimOne(t, ctx, pool, workerA, created.ID)
	if _, err := pool.Exec(ctx, "UPDATE scheduled_executions SET lease_expires_at=now()-interval '1 second' WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	claims, err := scheduler.NewService(pool).ClaimDue(ctx, workerB, scheduler.ClaimOptions{BatchSize: 1, LeaseDuration: 45 * time.Second})
	if err != nil || len(claims) != 1 || claims[0].ID != first.ID {
		t.Fatalf("reclaim=%+v error=%v", claims, err)
	}
	runner := integrationRunner(t, pool, workerB, monitorService, &sequenceChecker{outcomes: []check.Outcome{check.OutcomeSuccess}})
	runClaim(t, runner, claims[0])
	var count, claimCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM check_results WHERE execution_id=$1", first.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT claim_count FROM scheduled_executions WHERE id=$1", first.ID).Scan(&claimCount); err != nil {
		t.Fatal(err)
	}
	if count != 1 || claimCount != 2 {
		t.Fatalf("results=%d claims=%d", count, claimCount)
	}
}

func TestWorkerDoesNotStartWithInsufficientLease(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	ctx := context.Background()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	created := createWorkerMonitor(t, ctx, monitorService, "Worker lease guard", 1, 1)
	workerID := uuid.New()
	claimed := claimOne(t, ctx, pool, workerID, created.ID)
	if _, err := pool.Exec(ctx, "UPDATE scheduled_executions SET lease_expires_at=now()+interval '1 second' WHERE id=$1", claimed.ID); err != nil {
		t.Fatal(err)
	}
	checker := &sequenceChecker{outcomes: []check.Outcome{check.OutcomeSuccess}}
	runner := integrationRunner(t, pool, workerID, monitorService, checker)
	runClaim(t, runner, claimed)
	if checker.calls != 0 {
		t.Fatalf("checker calls=%d", checker.calls)
	}
	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM scheduled_executions WHERE id=$1", claimed.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != scheduler.StatusClaimed {
		t.Fatalf("status=%s", status)
	}
}

func integrationRunner(t *testing.T, pool *pgxpool.Pool, workerID uuid.UUID, monitors *monitor.Service, checker Checker) *Runner {
	t.Helper()
	runner, err := New(Config{Concurrency: 2, PollInterval: time.Second, LeaseDuration: 45 * time.Second, ShutdownGrace: time.Second},
		workerID, scheduler.NewService(pool), monitors, checker, checkresult.NewService(pool), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func createWorkerMonitor(t *testing.T, ctx context.Context, service *monitor.Service, name string, failures, recoveries int) monitor.Monitor {
	t.Helper()
	created, err := service.Create(ctx, monitor.CreateInput{Name: name, URL: "https://worker.example/health", FailureThreshold: failures, RecoveryThreshold: recoveries})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func claimOne(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workerID, monitorID uuid.UUID) scheduler.Execution {
	t.Helper()
	if _, err := pool.Exec(ctx, "UPDATE monitors SET next_check_at=now() WHERE id=$1", monitorID); err != nil {
		t.Fatal(err)
	}
	claims, err := scheduler.NewService(pool).ClaimDue(ctx, workerID, scheduler.ClaimOptions{BatchSize: 1, LeaseDuration: 45 * time.Second})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%+v error=%v", claims, err)
	}
	return claims[0]
}

func runClaim(t *testing.T, runner *Runner, execution scheduler.Execution) {
	t.Helper()
	<-runClaimAsync(runner, execution)
}

func runClaimAsync(runner *Runner, execution scheduler.Execution) <-chan struct{} {
	done := make(chan struct{})
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	var jobs sync.WaitGroup
	jobs.Add(1)
	go func() { runner.runJob(context.Background(), execution, slots, &jobs); jobs.Wait(); close(done) }()
	return done
}

type sequenceChecker struct {
	outcomes []check.Outcome
	calls    int
}

func (s *sequenceChecker) Execute(_ context.Context, configured monitor.Monitor) check.Result {
	outcome := s.outcomes[s.calls]
	s.calls++
	now := time.Now().UTC()
	result := check.Result{MonitorID: configured.ID, StartedAt: now.Add(-time.Millisecond), FinishedAt: now, Duration: time.Millisecond, Outcome: outcome}
	if outcome != check.OutcomeSuccess {
		result.Error = &check.ErrorDetail{Code: check.ErrorCodeConnectionFailed, Description: "connection failed"}
	}
	return result
}
