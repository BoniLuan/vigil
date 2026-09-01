package migrations

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMigrationsAreCurrent(t *testing.T) {
	databaseURL := os.Getenv("VIGIL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VIGIL_TEST_DATABASE_URL is not set; skipping migration integration test")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_test") {
		t.Fatal("VIGIL_TEST_DATABASE_URL must target a database whose name ends in _test")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(context.Background(), db, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	version, err := goose.GetDBVersionContext(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("migration version = %d, want 2", version)
	}
}
