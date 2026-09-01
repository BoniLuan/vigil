package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/BoniLuan/vigil/internal/monitor"
	monitorapi "github.com/BoniLuan/vigil/internal/monitor/httpapi"
	"github.com/BoniLuan/vigil/internal/platform/config"
	"github.com/BoniLuan/vigil/internal/platform/database"
)

func API(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	var ready atomic.Bool
	ready.Store(true)
	mux := http.NewServeMux()
	monitorService := monitor.NewService(monitor.NewStore(pool))
	monitorapi.New(monitorService, logger).Register(mux)
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		pingCtx, cancel := context.WithTimeout(r.Context(), cfg.DatabaseTimeout)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("api listening", "address", cfg.HTTPAddr)
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
		ready.Store(false)
		logger.Info("api shutdown started")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		logger.Info("api shutdown complete")
		return nil
	}
}
