package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	generated "github.com/BoniLuan/vigil/internal/platform/database/db"
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

// ClaimDue materializes at most one overdue identity per due monitor, advances
// schedule boundaries, then claims available pending or expired work.
func (s *Service) ClaimDue(ctx context.Context, workerID uuid.UUID, options ClaimOptions) ([]Execution, error) {
	options, err := normalizeOptions(workerID, options)
	if err != nil {
		return nil, err
	}
	if err := s.materializeDue(ctx, options.BatchSize); err != nil {
		return nil, err
	}
	rows, err := s.queries.ClaimAvailableExecutions(ctx, generated.ClaimAvailableExecutionsParams{
		LeaseOwner:   pgtype.UUID{Bytes: workerID, Valid: true},
		LeaseSeconds: options.LeaseDuration.Seconds(), BatchSize: int32(options.BatchSize),
	})
	if err != nil {
		return nil, fmt.Errorf("claim scheduled executions: %w", err)
	}
	result := make([]Execution, 0, len(rows))
	for _, row := range rows {
		result = append(result, execution(row))
	}
	return result, nil
}

func (s *Service) CanStartExecution(ctx context.Context, executionID, workerID uuid.UUID, required time.Duration) (bool, error) {
	if executionID == uuid.Nil || workerID == uuid.Nil || required <= 0 {
		return false, ErrInvalidClaimOptions
	}
	allowed, err := s.queries.CanStartScheduledExecution(ctx, generated.CanStartScheduledExecutionParams{
		ExecutionID: executionID, LeaseOwner: pgtype.UUID{Bytes: workerID, Valid: true}, RequiredSeconds: required.Seconds(),
	})
	if err != nil {
		return false, fmt.Errorf("verify scheduled execution start lease: %w", err)
	}
	return allowed, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Execution, error) {
	row, err := s.queries.GetScheduledExecution(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, pgx.ErrNoRows
	}
	if err != nil {
		return Execution{}, fmt.Errorf("get scheduled execution: %w", err)
	}
	return execution(row), nil
}

func (s *Service) materializeDue(ctx context.Context, batchSize int) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin due schedule transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)
	monitors, err := q.LockDueMonitors(ctx, int32(batchSize))
	if err != nil {
		return fmt.Errorf("lock due monitors: %w", err)
	}
	for _, due := range monitors {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate scheduled execution id: %w", err)
		}
		_, err = q.InsertScheduledExecution(ctx, generated.InsertScheduledExecutionParams{
			ID: id, MonitorID: due.ID, ScheduledAt: due.NextCheckAt,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("materialize scheduled execution: %w", err)
		}
		if err := q.AdvanceMonitorSchedule(ctx, due.ID); err != nil {
			return fmt.Errorf("advance monitor schedule: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit due schedule transaction: %w", err)
	}
	return nil
}

func normalizeOptions(workerID uuid.UUID, options ClaimOptions) (ClaimOptions, error) {
	if workerID == uuid.Nil {
		return ClaimOptions{}, ErrInvalidClaimOptions
	}
	if options.BatchSize == 0 {
		options.BatchSize = DefaultBatchSize
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = DefaultLeaseDuration
	}
	if options.BatchSize < 1 || options.BatchSize > MaximumBatchSize ||
		options.LeaseDuration < time.Second || options.LeaseDuration > 15*time.Minute {
		return ClaimOptions{}, ErrInvalidClaimOptions
	}
	return options, nil
}

func execution(row generated.ScheduledExecution) Execution {
	return Execution{
		ID: row.ID, MonitorID: row.MonitorID, ScheduledAt: timestamp(row.ScheduledAt),
		Status: row.Status, LeaseOwner: uuidPointer(row.LeaseOwner),
		LeaseExpiresAt: timePointer(row.LeaseExpiresAt), ClaimCount: int(row.ClaimCount),
		FinishedAt: timePointer(row.FinishedAt), CreatedAt: timestamp(row.CreatedAt), UpdatedAt: timestamp(row.UpdatedAt),
	}
}

func timestamp(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func uuidPointer(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := uuid.UUID(value.Bytes)
	return &result
}
