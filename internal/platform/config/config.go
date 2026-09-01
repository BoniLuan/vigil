package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment      string
	LogLevel         string
	LogFormat        string
	HTTPAddr         string
	ShutdownTimeout  time.Duration
	DatabaseURL      string
	DatabaseMaxConns int32
	DatabaseMinConns int32
	DatabaseTimeout  time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Environment:      env("VIGIL_ENV", "development"),
		LogLevel:         env("VIGIL_LOG_LEVEL", "info"),
		LogFormat:        env("VIGIL_LOG_FORMAT", "json"),
		HTTPAddr:         env("VIGIL_HTTP_ADDR", ":8080"),
		DatabaseURL:      strings.TrimSpace(os.Getenv("VIGIL_DATABASE_URL")),
		DatabaseMaxConns: 10,
		DatabaseMinConns: 1,
	}

	var errs []error
	cfg.ShutdownTimeout, errs = duration("VIGIL_SHUTDOWN_TIMEOUT", 10*time.Second, errs)
	cfg.DatabaseTimeout, errs = duration("VIGIL_DATABASE_CONNECT_TIMEOUT", 5*time.Second, errs)
	cfg.DatabaseMaxConns, errs = integer("VIGIL_DATABASE_MAX_CONNS", cfg.DatabaseMaxConns, errs)
	cfg.DatabaseMinConns, errs = integer("VIGIL_DATABASE_MIN_CONNS", cfg.DatabaseMinConns, errs)

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("VIGIL_DATABASE_URL is required"))
	} else if parsed, err := url.Parse(cfg.DatabaseURL); err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		errs = append(errs, errors.New("VIGIL_DATABASE_URL must be a valid postgres URL"))
	}
	if cfg.HTTPAddr == "" {
		errs = append(errs, errors.New("VIGIL_HTTP_ADDR must not be empty"))
	}
	if cfg.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("VIGIL_SHUTDOWN_TIMEOUT must be positive"))
	}
	if cfg.DatabaseTimeout <= 0 {
		errs = append(errs, errors.New("VIGIL_DATABASE_CONNECT_TIMEOUT must be positive"))
	}
	if cfg.DatabaseMaxConns < 1 {
		errs = append(errs, errors.New("VIGIL_DATABASE_MAX_CONNS must be at least 1"))
	}
	if cfg.DatabaseMinConns < 0 || cfg.DatabaseMinConns > cfg.DatabaseMaxConns {
		errs = append(errs, errors.New("VIGIL_DATABASE_MIN_CONNS must be between 0 and VIGIL_DATABASE_MAX_CONNS"))
	}

	return cfg, errors.Join(errs...)
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration, errs []error) (time.Duration, []error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, errs
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, append(errs, fmt.Errorf("%s: %w", key, err))
	}
	return value, errs
}

func integer(key string, fallback int32, errs []error) (int32, []error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, errs
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, append(errs, fmt.Errorf("%s: %w", key, err))
	}
	return int32(value), errs
}
