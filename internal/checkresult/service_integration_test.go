package checkresult

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/testutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApplyResultPersistenceAndProjection(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	service := NewService(pool)
	ctx := context.Background()
	created := createMonitor(t, ctx, monitorService, "Persistence", 2, 2)
	started := time.Now().UTC().Add(-250 * time.Millisecond).Truncate(time.Microsecond)
	expires := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Microsecond)
	status := 200
	completed := check.Result{
		MonitorID: created.ID, StartedAt: started, FinishedAt: started.Add(125 * time.Millisecond),
		Duration: 125 * time.Millisecond, Outcome: check.OutcomeSuccess, StatusCode: &status,
		DialedIP: netip.MustParseAddr("1.1.1.1"), TLSExpiresAt: &expires,
	}
	stored, projection, err := service.ApplyResult(ctx, completed)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID.Version() != uuid.Version(7) || stored.MonitorID != created.ID || stored.Outcome != check.OutcomeSuccess ||
		stored.StatusCode == nil || *stored.StatusCode != status || stored.Error != nil || stored.DialedIP == nil ||
		*stored.DialedIP != completed.DialedIP || stored.TLSExpiresAt == nil || !stored.TLSExpiresAt.Equal(expires) {
		t.Fatalf("stored result = %+v", stored)
	}
	if projection.State != monitor.StateUp || projection.ConsecutiveSuccesses != 1 || projection.ConsecutiveFailures != 0 {
		t.Fatalf("projection = %+v", projection)
	}
	assertProjectionRow(t, pool, created.ID, stored.ID, monitor.StateUp, 0, 1, check.OutcomeSuccess)

	failure := failedResult(created.ID, check.OutcomeDNSError, check.ErrorCodeDNSLookupFailed)
	failure.DialedIP = netip.Addr{}
	storedFailure, projection, err := service.ApplyResult(ctx, failure)
	if err != nil {
		t.Fatal(err)
	}
	if storedFailure.StatusCode != nil || storedFailure.TLSExpiresAt != nil || storedFailure.Error == nil ||
		storedFailure.Error.Code != check.ErrorCodeDNSLookupFailed {
		t.Fatalf("stored failure = %+v", storedFailure)
	}
	if projection.State != monitor.StateUp || projection.ConsecutiveFailures != 1 || projection.ConsecutiveSuccesses != 0 {
		t.Fatalf("failure projection = %+v", projection)
	}
}

func TestApplyResultStateTransitionSequences(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	service := NewService(pool)
	ctx := context.Background()

	tests := []struct {
		name     string
		outcomes []check.Outcome
		states   []string
		failures []int64
		success  []int64
	}{
		{"pending failures reach threshold", []check.Outcome{check.OutcomeTimeout, check.OutcomeConnectionError}, []string{monitor.StatePending, monitor.StateDown}, []int64{1, 2}, []int64{0, 0}},
		{"pending success becomes up", []check.Outcome{check.OutcomeSuccess}, []string{monitor.StateUp}, []int64{0}, []int64{1}},
		{"up failures reach threshold", []check.Outcome{check.OutcomeSuccess, check.OutcomeHTTPFailure, check.OutcomeDNSError}, []string{monitor.StateUp, monitor.StateUp, monitor.StateDown}, []int64{0, 1, 2}, []int64{1, 0, 0}},
		{"down recovery reaches threshold", []check.Outcome{check.OutcomeTimeout, check.OutcomeTimeout, check.OutcomeSuccess, check.OutcomeSuccess}, []string{monitor.StatePending, monitor.StateDown, monitor.StateDown, monitor.StateUp}, []int64{1, 2, 0, 0}, []int64{0, 0, 1, 2}},
		{"up success increments", []check.Outcome{check.OutcomeSuccess, check.OutcomeSuccess}, []string{monitor.StateUp, monitor.StateUp}, []int64{0, 0}, []int64{1, 2}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created := createMonitor(t, ctx, monitorService, "Transitions "+string(rune('A'+index)), 2, 2)
			for step, outcome := range test.outcomes {
				var completed check.Result
				if outcome == check.OutcomeSuccess {
					completed = successfulResult(created.ID)
				} else {
					completed = failedResult(created.ID, outcome, errorCodeFor(outcome))
				}
				_, projection, err := service.ApplyResult(ctx, completed)
				if err != nil {
					t.Fatalf("step %d: %v", step, err)
				}
				if projection.State != test.states[step] || projection.ConsecutiveFailures != test.failures[step] || projection.ConsecutiveSuccesses != test.success[step] {
					t.Fatalf("step %d projection = %+v", step, projection)
				}
			}
		})
	}
}

func TestApplyResultPreservesPausedAndArchivedState(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	service := NewService(pool)
	ctx := context.Background()

	paused := createMonitor(t, ctx, monitorService, "Paused race", 1, 1)
	if _, err := monitorService.Pause(ctx, paused.ID); err != nil {
		t.Fatal(err)
	}
	stored, projection, err := service.ApplyResult(ctx, failedResult(paused.ID, check.OutcomeTimeout, check.ErrorCodeRequestTimeout))
	if err != nil {
		t.Fatal(err)
	}
	if projection.State != monitor.StatePaused || projection.ConsecutiveFailures != 0 {
		t.Fatalf("paused projection = %+v", projection)
	}
	assertProjectionRow(t, pool, paused.ID, stored.ID, monitor.StatePaused, 0, 0, check.OutcomeTimeout)

	archived := createMonitor(t, ctx, monitorService, "Archived race", 1, 1)
	if err := monitorService.Delete(ctx, archived.ID); err != nil {
		t.Fatal(err)
	}
	stored, projection, err = service.ApplyResult(ctx, successfulResult(archived.ID))
	if err != nil {
		t.Fatal(err)
	}
	if projection.State != monitor.StatePaused || projection.ConsecutiveSuccesses != 0 {
		t.Fatalf("archived projection = %+v", projection)
	}
	if _, err := monitorService.Resume(ctx, archived.ID); !errors.Is(err, monitor.ErrArchived) {
		t.Fatalf("Resume() error = %v", err)
	}
	got, err := monitorService.Get(ctx, archived.ID)
	if err != nil || got.ArchivedAt == nil || got.Enabled || got.Public {
		t.Fatalf("archived monitor = %+v, %v", got, err)
	}
	history, err := service.List(ctx, archived.ID, ListOptions{Limit: 10})
	if err != nil || len(history) != 1 || history[0].ID != stored.ID {
		t.Fatalf("archived history = %+v, %v", history, err)
	}
	listed, err := monitorService.List(ctx, monitor.ListOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range listed {
		if value.ID == archived.ID {
			t.Fatal("archived monitor appeared in active list")
		}
	}
}

func TestApplyResultIsAtomic(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	service := NewService(pool)
	ctx := context.Background()
	created := createMonitor(t, ctx, monitorService, "Atomic", 1, 1)
	if _, err := pool.Exec(ctx, `
CREATE FUNCTION vigil_test_reject_projection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'forced projection failure'; END $$;
CREATE TRIGGER vigil_test_reject_projection BEFORE UPDATE ON monitor_states
FOR EACH ROW EXECUTE FUNCTION vigil_test_reject_projection()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS vigil_test_reject_projection ON monitor_states")
		_, _ = pool.Exec(context.Background(), "DROP FUNCTION IF EXISTS vigil_test_reject_projection()")
	})
	if _, _, err := service.ApplyResult(ctx, successfulResult(created.ID)); err == nil {
		t.Fatal("ApplyResult() succeeded despite projection failure")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM check_results WHERE monitor_id = $1", created.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("history count after rollback = %d, %v", count, err)
	}
}

func TestCheckResultConstraintsAndForeignKey(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	service := NewService(pool)
	ctx := context.Background()
	missing := successfulResult(uuid.Must(uuid.NewV7()))
	if _, _, err := service.ApplyResult(ctx, missing); !errors.Is(err, monitor.ErrNotFound) {
		t.Fatalf("missing monitor error = %v", err)
	}

	id := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(ctx, `INSERT INTO check_results
(id, monitor_id, started_at, finished_at, duration_ms, outcome)
VALUES ($1, $2, now(), now(), 1, 'success')`, id, uuid.Must(uuid.NewV7()))
	if err == nil {
		t.Fatal("monitor foreign key accepted an unknown monitor")
	}
}

func TestListCheckResultsIsBoundedAndOrdered(t *testing.T) {
	pool := testutil.PostgreSQL(t)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	service := NewService(pool)
	ctx := context.Background()
	created := createMonitor(t, ctx, monitorService, "History", 3, 1)
	for index := 0; index < 3; index++ {
		result := successfulResult(created.ID)
		result.StartedAt = result.StartedAt.Add(time.Duration(index) * time.Second)
		result.FinishedAt = result.StartedAt.Add(result.Duration)
		if _, _, err := service.ApplyResult(ctx, result); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.List(ctx, created.ID, ListOptions{Limit: 1, Offset: 1})
	if err != nil || len(page) != 1 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	all, err := service.List(ctx, created.ID, ListOptions{Limit: 100})
	if err != nil || len(all) != 3 || !all[0].StartedAt.After(all[1].StartedAt) {
		t.Fatalf("history = %+v, %v", all, err)
	}
	if _, err := service.List(ctx, uuid.Must(uuid.NewV7()), ListOptions{}); !errors.Is(err, monitor.ErrNotFound) {
		t.Fatalf("missing monitor List() error = %v", err)
	}
}

func createMonitor(t *testing.T, ctx context.Context, service *monitor.Service, name string, failure, recovery int) monitor.Monitor {
	t.Helper()
	created, err := service.Create(ctx, monitor.CreateInput{
		Name: name, URL: "https://example.com/health", FailureThreshold: failure, RecoveryThreshold: recovery,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func successfulResult(monitorID uuid.UUID) check.Result {
	started := time.Now().UTC().Add(-20 * time.Millisecond)
	status := 200
	return check.Result{MonitorID: monitorID, StartedAt: started, FinishedAt: started.Add(10 * time.Millisecond), Duration: 10 * time.Millisecond, Outcome: check.OutcomeSuccess, StatusCode: &status}
}

func failedResult(monitorID uuid.UUID, outcome check.Outcome, code check.ErrorCode) check.Result {
	started := time.Now().UTC().Add(-20 * time.Millisecond)
	result := check.Result{MonitorID: monitorID, StartedAt: started, FinishedAt: started.Add(10 * time.Millisecond), Duration: 10 * time.Millisecond, Outcome: outcome, Error: errorDetail(code)}
	if outcome == check.OutcomeHTTPFailure {
		status := 503
		result.StatusCode = &status
	}
	return result
}

func errorDetail(code check.ErrorCode) *check.ErrorDetail {
	detail := check.NewErrorDetail(code, "safe deterministic failure")
	return &detail
}

func errorCodeFor(outcome check.Outcome) check.ErrorCode {
	switch outcome {
	case check.OutcomeHTTPFailure:
		return check.ErrorCodeUnexpectedStatus
	case check.OutcomeDNSError:
		return check.ErrorCodeDNSLookupFailed
	case check.OutcomeTLSError:
		return check.ErrorCodeTLSHandshakeFailed
	case check.OutcomeTimeout:
		return check.ErrorCodeRequestTimeout
	default:
		return check.ErrorCodeConnectionFailed
	}
}

func assertProjectionRow(t *testing.T, pool *pgxpool.Pool, monitorID, resultID uuid.UUID, state string, failures, successes int64, outcome check.Outcome) {
	t.Helper()
	var gotResultID uuid.UUID
	var gotState, gotOutcome string
	var gotFailures, gotSuccesses int64
	err := pool.QueryRow(context.Background(), `SELECT state, last_check_result_id, last_outcome,
consecutive_failures, consecutive_successes FROM monitor_states WHERE monitor_id = $1`, monitorID).
		Scan(&gotState, &gotResultID, &gotOutcome, &gotFailures, &gotSuccesses)
	if err != nil || gotState != state || gotResultID != resultID || gotOutcome != string(outcome) || gotFailures != failures || gotSuccesses != successes {
		t.Fatalf("projection row = %q %s %q %d %d, err=%v", gotState, gotResultID, gotOutcome, gotFailures, gotSuccesses, err)
	}
}
