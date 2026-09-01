package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("VIGIL_DATABASE_URL", "postgres://vigil:secret@db:5432/vigil")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DatabaseMaxConns != 10 || cfg.DatabaseMinConns != 1 {
		t.Fatalf("unexpected pool defaults: %+v", cfg)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("VIGIL_DATABASE_URL", "http://not-postgres.example")
	t.Setenv("VIGIL_DATABASE_MAX_CONNS", "0")
	t.Setenv("VIGIL_SHUTDOWN_TIMEOUT", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	for _, want := range []string{"VIGIL_DATABASE_URL", "VIGIL_DATABASE_MAX_CONNS", "VIGIL_SHUTDOWN_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error %q does not contain %q", err, want)
		}
	}
}

func TestLoadRejectsMinGreaterThanMax(t *testing.T) {
	t.Setenv("VIGIL_DATABASE_URL", "postgres://vigil:secret@db:5432/vigil")
	t.Setenv("VIGIL_DATABASE_MAX_CONNS", "2")
	t.Setenv("VIGIL_DATABASE_MIN_CONNS", "3")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "VIGIL_DATABASE_MIN_CONNS") {
		t.Fatalf("Load() error = %v", err)
	}
}
