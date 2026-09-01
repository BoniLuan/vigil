package testutil

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func PostgreSQL(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("VIGIL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VIGIL_TEST_DATABASE_URL is not set; skipping PostgreSQL integration test")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_test") {
		t.Fatal("VIGIL_TEST_DATABASE_URL must target a database whose name ends in _test")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect test PostgreSQL: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping test PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	lock, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire PostgreSQL test lock connection: %v", err)
	}
	if _, err := lock.Exec(context.Background(), "SELECT pg_advisory_lock(86744511)"); err != nil {
		lock.Release()
		t.Fatalf("acquire PostgreSQL integration test lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = lock.Exec(context.Background(), "SELECT pg_advisory_unlock(86744511)")
		lock.Release()
	})
	if _, err := pool.Exec(context.Background(), "TRUNCATE monitors CASCADE"); err != nil {
		t.Fatalf("truncate monitor test data: %v", err)
	}
	return pool
}
