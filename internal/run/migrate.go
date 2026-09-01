package run

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/BoniLuan/vigil/internal/platform/config"
	"github.com/BoniLuan/vigil/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func Migrations(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	command := "up"
	if len(args) > 0 {
		command = args[0]
	}
	if command != "up" && command != "status" && command != "version" {
		return fmt.Errorf("unsupported migration command %q: expected up, status, or version", command)
	}

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping migration database: %w", err)
	}

	goose.SetBaseFS(migrations.Files)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	logger.Info("running database migration command", "command", command)
	if err := goose.RunContext(ctx, command, db, "."); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
