package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	generated "github.com/BoniLuan/vigil/internal/platform/database/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	batchSize   = int32(500)
	maxBatches  = 20
	runInterval = time.Hour
)

type Counts struct {
	Results    int
	Executions int
}

type Service struct {
	queries *generated.Queries
	days    int32
}

func New(pool *pgxpool.Pool, days int32) (*Service, error) {
	if days < 1 || days > 3650 {
		return nil, fmt.Errorf("retention days must be between 1 and 3650")
	}
	return &Service{queries: generated.New(pool), days: days}, nil
}

// RunOnce uses short, bounded DELETE statements. SKIP LOCKED permits another
// process to perform maintenance without blocking check completion. A pass is
// capped; any backlog continues on the next hourly run.
func (s *Service) RunOnce(ctx context.Context) (Counts, error) {
	var counts Counts
	for range maxBatches {
		ids, err := s.queries.DeleteExpiredCheckResults(ctx, generated.DeleteExpiredCheckResultsParams{
			RetentionDays: s.days, BatchSize: batchSize,
		})
		if err != nil {
			return counts, fmt.Errorf("delete expired check results: %w", err)
		}
		counts.Results += len(ids)
		if len(ids) < int(batchSize) {
			break
		}
	}
	for range maxBatches {
		ids, err := s.queries.DeleteExpiredCompletedExecutions(ctx, generated.DeleteExpiredCompletedExecutionsParams{
			RetentionDays: s.days, BatchSize: batchSize,
		})
		if err != nil {
			return counts, fmt.Errorf("delete expired completed executions: %w", err)
		}
		counts.Executions += len(ids)
		if len(ids) < int(batchSize) {
			break
		}
	}
	return counts, nil
}

// Run starts with one maintenance pass and repeats hourly until shutdown.
// Monitoring continues if a maintenance pass fails; the next pass retries.
func (s *Service) Run(ctx context.Context, logger *slog.Logger) {
	for {
		counts, err := s.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			logger.Error("retention maintenance failed", "error", err)
		} else if err == nil && (counts.Results > 0 || counts.Executions > 0) {
			logger.Info("retention maintenance completed", "results_deleted", counts.Results, "executions_deleted", counts.Executions)
		}
		timer := time.NewTimer(runInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
