package run

import (
	"context"
	"log/slog"

	"github.com/BoniLuan/vigil/internal/platform/config"
	"github.com/BoniLuan/vigil/internal/platform/database"
)

func Worker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	logger.Info("worker ready")
	<-ctx.Done()
	logger.Info("worker shutdown complete")
	return nil
}
