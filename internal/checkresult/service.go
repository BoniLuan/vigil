package checkresult

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/monitor"
	generated "github.com/BoniLuan/vigil/internal/platform/database/db"
	"github.com/BoniLuan/vigil/internal/scheduler"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, queries: generated.New(pool)}
}

// ApplyResult keeps manually initiated results supported. Once a scheduled
// result has advanced a projection, later unscheduled results remain history
// only so they cannot bypass schedule ordering.
func (s *Service) ApplyResult(ctx context.Context, completed check.Result) (StoredResult, Projection, error) {
	prepared, err := prepare(completed)
	if err != nil {
		return StoredResult{}, Projection{}, err
	}
	resultID, err := uuid.NewV7()
	if err != nil {
		return StoredResult{}, Projection{}, fmt.Errorf("generate check result id: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StoredResult{}, Projection{}, fmt.Errorf("begin apply check result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)
	locked, err := lockProjection(ctx, q, completed.MonitorID)
	if err != nil {
		return StoredResult{}, Projection{}, err
	}
	row, projection, err := applyPrepared(ctx, q, locked, resultID, uuid.Nil, nil, prepared)
	if err != nil {
		return StoredResult{}, Projection{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredResult{}, Projection{}, fmt.Errorf("commit check result: %w", err)
	}
	return stored(row), projection, nil
}

// CompleteExecution atomically records one result, conditionally advances the
// ordered projection, and completes the owned execution. A retry after a
// successful commit returns the existing result without applying it again.
func (s *Service) CompleteExecution(ctx context.Context, executionID, workerID uuid.UUID, completed check.Result) (StoredResult, error) {
	if executionID == uuid.Nil || workerID == uuid.Nil {
		return StoredResult{}, ErrExecutionNotClaimed
	}
	prepared, err := prepare(completed)
	if err != nil {
		return StoredResult{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StoredResult{}, fmt.Errorf("begin complete execution transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)
	execution, err := q.LockScheduledExecution(ctx, executionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredResult{}, ErrExecutionNotFound
	}
	if err != nil {
		return StoredResult{}, fmt.Errorf("lock scheduled execution: %w", err)
	}
	if execution.Status == scheduler.StatusCompleted {
		row, err := q.GetCheckResultByExecution(ctx, pgtype.UUID{Bytes: executionID, Valid: true})
		if err != nil {
			return StoredResult{}, fmt.Errorf("read completed execution result: %w", err)
		}
		return stored(row), nil
	}
	if execution.Status != scheduler.StatusClaimed {
		return StoredResult{}, ErrExecutionNotClaimed
	}
	owner := uuid.UUID(execution.LeaseOwner.Bytes)
	leaseActive, err := q.ScheduledExecutionLeaseActive(ctx, executionID)
	if err != nil {
		return StoredResult{}, fmt.Errorf("verify scheduled execution lease: %w", err)
	}
	if !execution.LeaseOwner.Valid || owner != workerID || !leaseActive {
		return StoredResult{}, ErrLeaseLost
	}
	if prepared.MonitorID != execution.MonitorID {
		return StoredResult{}, ErrInvalidResult
	}
	locked, err := lockProjection(ctx, q, execution.MonitorID)
	if err != nil {
		return StoredResult{}, err
	}
	resultID, err := uuid.NewV7()
	if err != nil {
		return StoredResult{}, fmt.Errorf("generate check result id: %w", err)
	}
	scheduledAt := timestamp(execution.ScheduledAt)
	row, _, err := applyPrepared(ctx, q, locked, resultID, executionID, &scheduledAt, prepared)
	if err != nil {
		return StoredResult{}, err
	}
	if _, err := q.CompleteScheduledExecution(ctx, executionID); err != nil {
		return StoredResult{}, fmt.Errorf("mark scheduled execution completed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredResult{}, fmt.Errorf("commit scheduled execution completion: %w", err)
	}
	return stored(row), nil
}

func (s *Service) Summaries(ctx context.Context, monitorID uuid.UUID) ([]Summary, error) {
	exists, err := s.queries.MonitorExists(ctx, monitorID)
	if err != nil {
		return nil, fmt.Errorf("check monitor existence: %w", err)
	}
	if !exists {
		return nil, monitor.ErrNotFound
	}
	rows, err := s.queries.GetMonitorSummaries(ctx, monitorID)
	if err != nil {
		return nil, fmt.Errorf("get monitor summaries: %w", err)
	}
	result := make([]Summary, 0, len(rows))
	for _, row := range rows {
		summary := Summary{Window: row.Window, CompletedChecks: row.CompletedChecks, SuccessfulChecks: row.SuccessfulChecks}
		if row.CompletedChecks > 0 {
			uptime := float64(row.SuccessfulChecks) * 100 / float64(row.CompletedChecks)
			latency := time.Duration(row.AverageDurationMs * float64(time.Millisecond))
			summary.UptimePercent, summary.AverageLatency = &uptime, &latency
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, monitorID uuid.UUID, options ListOptions) ([]StoredResult, error) {
	if options.Limit <= 0 {
		options.Limit = 50
	}
	if options.Limit > 100 {
		options.Limit = 100
	}
	if options.Offset < 0 {
		options.Offset = 0
	}
	exists, err := s.queries.MonitorExists(ctx, monitorID)
	if err != nil {
		return nil, fmt.Errorf("check monitor existence: %w", err)
	}
	if !exists {
		return nil, monitor.ErrNotFound
	}
	rows, err := s.queries.ListCheckResults(ctx, generated.ListCheckResultsParams{
		MonitorID: monitorID, Limit: int32(options.Limit), Offset: int32(options.Offset),
	})
	if err != nil {
		return nil, fmt.Errorf("list check results: %w", err)
	}
	results := make([]StoredResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, stored(row))
	}
	return results, nil
}

func lockProjection(ctx context.Context, q *generated.Queries, monitorID uuid.UUID) (generated.LockMonitorProjectionRow, error) {
	locked, err := q.LockMonitorProjection(ctx, monitorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return generated.LockMonitorProjectionRow{}, monitor.ErrNotFound
	}
	if err != nil {
		return generated.LockMonitorProjectionRow{}, fmt.Errorf("lock monitor projection: %w", err)
	}
	return locked, nil
}

func applyPrepared(ctx context.Context, q *generated.Queries, locked generated.LockMonitorProjectionRow, resultID, executionID uuid.UUID, scheduledAt *time.Time, prepared check.Result) (generated.CheckResult, Projection, error) {
	row, err := q.InsertCheckResult(ctx, insertParams(resultID, prepared.MonitorID, executionID, prepared))
	if err != nil {
		return generated.CheckResult{}, Projection{}, fmt.Errorf("insert check result: %w", err)
	}
	projection := Projection{
		State: locked.State, ConsecutiveFailures: locked.ConsecutiveFailures,
		ConsecutiveSuccesses: locked.ConsecutiveSuccesses,
	}
	shouldApply := false
	lastApplied := locked.LastAppliedScheduledAt
	if scheduledAt != nil {
		shouldApply = !lastApplied.Valid || scheduledAt.After(lastApplied.Time)
	} else {
		shouldApply = !lastApplied.Valid
	}
	if !shouldApply {
		return row, projection, nil
	}
	if !locked.ArchivedAt.Valid {
		projection = NextProjection(projection, prepared.Outcome, int(locked.FailureThreshold), int(locked.RecoveryThreshold))
	}
	if scheduledAt != nil {
		lastApplied = pgtype.Timestamptz{Time: scheduledAt.UTC(), Valid: true}
	}
	if err := q.UpdateMonitorProjection(ctx, generated.UpdateMonitorProjectionParams{
		MonitorID: prepared.MonitorID, State: projection.State,
		LastCheckResultID:      pgtype.UUID{Bytes: resultID, Valid: true},
		LastCheckedAt:          pgtype.Timestamptz{Time: prepared.FinishedAt, Valid: true},
		LastOutcome:            pgtype.Text{String: string(prepared.Outcome), Valid: true},
		LastStatusCode:         int2(prepared.StatusCode),
		LastDurationMs:         pgtype.Int8{Int64: prepared.Duration.Milliseconds(), Valid: true},
		ConsecutiveFailures:    projection.ConsecutiveFailures,
		ConsecutiveSuccesses:   projection.ConsecutiveSuccesses,
		LastAppliedScheduledAt: lastApplied,
	}); err != nil {
		return generated.CheckResult{}, Projection{}, fmt.Errorf("update monitor projection: %w", err)
	}
	return row, projection, nil
}

func prepare(value check.Result) (check.Result, error) {
	if value.MonitorID == uuid.Nil || value.StartedAt.IsZero() || value.FinishedAt.IsZero() ||
		value.FinishedAt.Before(value.StartedAt) || value.Duration < 0 || !value.Outcome.Valid() {
		return check.Result{}, ErrInvalidResult
	}
	if value.StatusCode != nil && (*value.StatusCode < 100 || *value.StatusCode > 599) {
		return check.Result{}, ErrInvalidResult
	}
	if value.Outcome == check.OutcomeHTTPFailure && value.StatusCode == nil {
		return check.Result{}, ErrInvalidResult
	}
	if value.Outcome == check.OutcomeSuccess {
		if value.Error != nil {
			return check.Result{}, ErrInvalidResult
		}
	} else {
		if value.Error == nil || !value.Error.Code.Valid() || strings.TrimSpace(value.Error.Description) == "" {
			return check.Result{}, ErrInvalidResult
		}
		detail := check.NewErrorDetail(value.Error.Code, value.Error.Description)
		value.Error = &detail
	}
	value.StartedAt = value.StartedAt.UTC()
	value.FinishedAt = value.FinishedAt.UTC()
	if value.TLSExpiresAt != nil {
		tlsExpiry := value.TLSExpiresAt.UTC()
		value.TLSExpiresAt = &tlsExpiry
	}
	return value, nil
}

func insertParams(id, monitorID, executionID uuid.UUID, value check.Result) generated.InsertCheckResultParams {
	params := generated.InsertCheckResultParams{
		ID: id, MonitorID: monitorID, ExecutionID: pgtype.UUID{Bytes: executionID, Valid: executionID != uuid.Nil},
		StartedAt:  pgtype.Timestamptz{Time: value.StartedAt, Valid: true},
		FinishedAt: pgtype.Timestamptz{Time: value.FinishedAt, Valid: true},
		DurationMs: value.Duration.Milliseconds(), Outcome: string(value.Outcome),
		StatusCode: int2(value.StatusCode), TlsExpiresAt: timestamptz(value.TLSExpiresAt),
	}
	if value.Error != nil {
		params.ErrorCode = pgtype.Text{String: string(value.Error.Code), Valid: true}
		params.ErrorDescription = pgtype.Text{String: value.Error.Description, Valid: true}
	}
	if value.DialedIP.IsValid() {
		address := value.DialedIP.Unmap()
		params.DialedIp = &address
	}
	return params
}

func stored(row generated.CheckResult) StoredResult {
	result := StoredResult{
		ID: row.ID, MonitorID: row.MonitorID, ExecutionID: uuidResultPointer(row.ExecutionID),
		StartedAt: timestamp(row.StartedAt), FinishedAt: timestamp(row.FinishedAt),
		Duration: time.Duration(row.DurationMs) * time.Millisecond,
		Outcome:  check.Outcome(row.Outcome), StatusCode: integer(row.StatusCode),
		TLSExpiresAt: timestampPointer(row.TlsExpiresAt), CreatedAt: timestamp(row.CreatedAt),
	}
	if row.ErrorCode.Valid {
		detail := check.NewErrorDetail(check.ErrorCode(row.ErrorCode.String), row.ErrorDescription.String)
		result.Error = &detail
	}
	if row.DialedIp != nil {
		address := row.DialedIp.Unmap()
		result.DialedIP = &address
	}
	return result
}

func uuidResultPointer(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := uuid.UUID(value.Bytes)
	return &result
}

func int2(value *int) pgtype.Int2 {
	if value == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*value), Valid: true}
}

func integer(value pgtype.Int2) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int16)
	return &result
}

func timestamptz(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func timestamp(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func timestampPointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
