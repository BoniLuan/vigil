package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	generated "github.com/BoniLuan/vigil/internal/platform/database/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool    *pgxpool.Pool
	queries *generated.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, queries: generated.New(pool)}
}

func (s *Store) Create(ctx context.Context, value Monitor) (Monitor, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Monitor{}, fmt.Errorf("begin create monitor transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)
	_, err = q.CreateMonitor(ctx, generated.CreateMonitorParams{
		ID: value.ID, Name: value.Name, Slug: value.Slug,
		Description: text(value.Description), Kind: value.Kind, Url: value.URL,
		HttpMethod: value.HTTPMethod, ExpectedStatusMin: int16(value.ExpectedStatusMin),
		ExpectedStatusMax: int16(value.ExpectedStatusMax), IntervalSeconds: int32(value.Interval.Seconds()),
		TimeoutMs: int32(value.Timeout.Milliseconds()), FailureThreshold: int16(value.FailureThreshold),
		RecoveryThreshold: int16(value.RecoveryThreshold), Enabled: value.Enabled, Public: value.Public,
	})
	if err != nil {
		return Monitor{}, translateWriteError(err)
	}
	if _, err := q.CreateMonitorState(ctx, generated.CreateMonitorStateParams{MonitorID: value.ID, State: value.State}); err != nil {
		return Monitor{}, fmt.Errorf("create monitor state: %w", err)
	}
	result, err := q.GetMonitor(ctx, value.ID)
	if err != nil {
		return Monitor{}, fmt.Errorf("read created monitor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Monitor{}, fmt.Errorf("commit create monitor: %w", err)
	}
	return monitorFromGet(result), nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (Monitor, error) {
	row, err := s.queries.GetMonitor(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, fmt.Errorf("get monitor: %w", err)
	}
	return monitorFromGet(row), nil
}

func (s *Store) List(ctx context.Context, options ListOptions) ([]Monitor, error) {
	rows, err := s.queries.ListMonitors(ctx, generated.ListMonitorsParams{Limit: int32(options.Limit), Offset: int32(options.Offset)})
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	result := make([]Monitor, 0, len(rows))
	for _, row := range rows {
		result = append(result, monitorFromList(row))
	}
	return result, nil
}

func (s *Store) Update(ctx context.Context, value Monitor) (Monitor, error) {
	_, err := s.queries.UpdateMonitor(ctx, generated.UpdateMonitorParams{
		ID: value.ID, Name: value.Name, Slug: value.Slug, Description: text(value.Description),
		Url: value.URL, HttpMethod: value.HTTPMethod,
		ExpectedStatusMin: int16(value.ExpectedStatusMin), ExpectedStatusMax: int16(value.ExpectedStatusMax),
		IntervalSeconds: int32(value.Interval.Seconds()), TimeoutMs: int32(value.Timeout.Milliseconds()),
		FailureThreshold: int16(value.FailureThreshold), RecoveryThreshold: int16(value.RecoveryThreshold),
		Public: value.Public, Version: value.Version,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrWriteConflict
	}
	if err != nil {
		return Monitor{}, translateWriteError(err)
	}
	return s.Get(ctx, value.ID)
}

func (s *Store) setOperationalState(ctx context.Context, id uuid.UUID, enabled bool, state string) (Monitor, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Monitor{}, fmt.Errorf("begin monitor state transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)
	if _, err := q.LockMonitor(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	} else if err != nil {
		return Monitor{}, fmt.Errorf("lock monitor: %w", err)
	}
	current, err := q.GetMonitor(ctx, id)
	if err != nil {
		return Monitor{}, fmt.Errorf("get locked monitor: %w", err)
	}
	if current.Enabled != enabled {
		if _, err := q.SetMonitorEnabled(ctx, generated.SetMonitorEnabledParams{ID: id, Enabled: enabled}); err != nil {
			return Monitor{}, fmt.Errorf("set monitor enabled: %w", err)
		}
	}
	if current.State != state {
		if _, err := q.SetMonitorState(ctx, generated.SetMonitorStateParams{MonitorID: id, State: state}); err != nil {
			return Monitor{}, fmt.Errorf("set monitor state: %w", err)
		}
	}
	row, err := q.GetMonitor(ctx, id)
	if err != nil {
		return Monitor{}, fmt.Errorf("read changed monitor state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Monitor{}, fmt.Errorf("commit monitor state: %w", err)
	}
	return monitorFromGet(row), nil
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.queries.DeleteMonitor(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	return nil
}

func translateWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "monitors_slug_key" {
		return ErrSlugConflict
	}
	return fmt.Errorf("write monitor: %w", err)
}

func text(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func stringPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func timestamp(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func monitorFromGet(row generated.GetMonitorRow) Monitor {
	return Monitor{
		ID: row.ID, Name: row.Name, Slug: row.Slug, Description: stringPointer(row.Description),
		Kind: row.Kind, URL: row.Url, HTTPMethod: row.HttpMethod,
		ExpectedStatusMin: int(row.ExpectedStatusMin), ExpectedStatusMax: int(row.ExpectedStatusMax),
		Interval:         time.Duration(row.IntervalSeconds) * time.Second,
		Timeout:          time.Duration(row.TimeoutMs) * time.Millisecond,
		FailureThreshold: int(row.FailureThreshold), RecoveryThreshold: int(row.RecoveryThreshold),
		Enabled: row.Enabled, Public: row.Public, Version: row.Version, State: row.State,
		CreatedAt: timestamp(row.CreatedAt), UpdatedAt: timestamp(row.UpdatedAt),
		StateUpdatedAt: timestamp(row.StateUpdatedAt),
	}
}

func monitorFromList(row generated.ListMonitorsRow) Monitor {
	return Monitor{
		ID: row.ID, Name: row.Name, Slug: row.Slug, Description: stringPointer(row.Description),
		Kind: row.Kind, URL: row.Url, HTTPMethod: row.HttpMethod,
		ExpectedStatusMin: int(row.ExpectedStatusMin), ExpectedStatusMax: int(row.ExpectedStatusMax),
		Interval:         time.Duration(row.IntervalSeconds) * time.Second,
		Timeout:          time.Duration(row.TimeoutMs) * time.Millisecond,
		FailureThreshold: int(row.FailureThreshold), RecoveryThreshold: int(row.RecoveryThreshold),
		Enabled: row.Enabled, Public: row.Public, Version: row.Version, State: row.State,
		CreatedAt: timestamp(row.CreatedAt), UpdatedAt: timestamp(row.UpdatedAt),
		StateUpdatedAt: timestamp(row.StateUpdatedAt),
	}
}
