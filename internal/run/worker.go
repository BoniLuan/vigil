package run

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/platform/config"
	"github.com/BoniLuan/vigil/internal/platform/database"
	"github.com/BoniLuan/vigil/internal/scheduler"
	"github.com/BoniLuan/vigil/internal/worker"
)

func Worker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	workerID, err := scheduler.NewWorkerID()
	if err != nil {
		return fmt.Errorf("generate worker identity: %w", err)
	}
	schedulerService := scheduler.NewService(pool)
	monitorService := monitor.NewService(monitor.NewStore(pool))
	runner, err := worker.New(worker.Config{
		Concurrency: cfg.WorkerConcurrency, PollInterval: cfg.WorkerPollInterval,
		LeaseDuration: cfg.WorkerLeaseDuration, ShutdownGrace: cfg.ShutdownTimeout,
	}, workerID, schedulerService, monitorService, check.NewExecutor(net.DefaultResolver),
		checkresult.NewService(pool), logger)
	if err != nil {
		return fmt.Errorf("configure worker: %w", err)
	}
	return runner.Run(ctx)
}
