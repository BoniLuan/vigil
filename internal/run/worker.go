package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/BoniLuan/vigil/internal/appmetrics"
	"github.com/BoniLuan/vigil/internal/check"
	"github.com/BoniLuan/vigil/internal/checkresult"
	"github.com/BoniLuan/vigil/internal/monitor"
	"github.com/BoniLuan/vigil/internal/platform/config"
	"github.com/BoniLuan/vigil/internal/platform/database"
	"github.com/BoniLuan/vigil/internal/scheduler"
	"github.com/BoniLuan/vigil/internal/worker"
)

func Worker(ctx context.Context, cfg config.Config, build BuildInfo, logger *slog.Logger) error {
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
	metrics := appmetrics.NewWorker(pool, appmetrics.BuildInfo{Version: build.Version, Commit: build.Commit, Role: "worker"}, cfg.WorkerConcurrency)
	runner, err := worker.New(worker.Config{
		Concurrency: cfg.WorkerConcurrency, PollInterval: cfg.WorkerPollInterval,
		LeaseDuration: cfg.WorkerLeaseDuration, ShutdownGrace: cfg.ShutdownTimeout,
	}, workerID, schedulerService, monitorService, check.NewExecutorWithUserAgent(net.DefaultResolver, checkerUserAgent(build.Version)),
		checkresult.NewService(pool), logger, worker.WithObserver(metrics))
	if err != nil {
		return fmt.Errorf("configure worker: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), cfg.DatabaseTimeout)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	server := &http.Server{Addr: cfg.WorkerHTTPAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	runtimeCtx, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Run(runtimeCtx) }()
	serverDone := make(chan error, 1)
	go func() {
		logger.Info("worker operations listening", "address", cfg.WorkerHTTPAddr)
		serverDone <- server.ListenAndServe()
	}()

	shutdownServer := func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown worker operations server: %w", err)
		}
		return nil
	}
	select {
	case runErr := <-runnerDone:
		cancelRuntime()
		if err := shutdownServer(); err != nil {
			return err
		}
		return runErr
	case serveErr := <-serverDone:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return <-runnerDone
		}
		cancelRuntime()
		<-runnerDone
		return fmt.Errorf("serve worker operations: %w", serveErr)
	case <-ctx.Done():
		cancelRuntime()
		serverErr := shutdownServer()
		runErr := <-runnerDone
		if serverErr != nil {
			return serverErr
		}
		return runErr
	}
}
