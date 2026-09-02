package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/BoniLuan/vigil/internal/platform/config"
	"github.com/BoniLuan/vigil/internal/platform/logging"
	"github.com/BoniLuan/vigil/internal/run"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(realMain(os.Args))
}

func realMain(args []string) int {
	if len(args) < 2 {
		printUsage()
		return 2
	}

	command := args[1]
	if command == "version" {
		fmt.Printf("vigil %s (commit=%s, built=%s)\n", version, commit, date)
		return 0
	}
	if command != "api" && command != "worker" && command != "migrate" {
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		printUsage()
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 1
	}

	logger, err := logging.New(cfg.LogLevel, cfg.LogFormat, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logging configuration error: %v\n", err)
		return 1
	}
	logger = logger.With("service", "vigil", "process", command, "version", version)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var runErr error
	switch command {
	case "api":
		runErr = run.API(ctx, cfg, run.BuildInfo{Version: version, Commit: commit}, logger)
	case "worker":
		runErr = run.Worker(ctx, cfg, run.BuildInfo{Version: version, Commit: commit}, logger)
	case "migrate":
		runErr = run.Migrations(ctx, cfg, logger, args[2:])
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		logger.Error("process stopped with error", "error", runErr)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: vigil <api|worker|migrate|version> [arguments]")
}
