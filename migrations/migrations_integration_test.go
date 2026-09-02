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

func TestMigrationsFromBaselineAndEmptyDatabase(t *testing.T) {
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
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_lock(86744511)"); err != nil {
		t.Fatalf("acquire migration test lock: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "SELECT pg_advisory_unlock(86744511)") })
	goose.SetBaseFS(Files)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, db, ".", 0); err != nil {
		t.Fatalf("reset test database: %v", err)
	}
	t.Cleanup(func() { _ = goose.UpContext(context.Background(), db, ".") })

	if err := goose.UpToContext(ctx, db, ".", 2); err != nil {
		t.Fatalf("migrate to monitor baseline: %v", err)
	}
	assertVersion(t, ctx, db, 2)
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("baseline to latest: %v", err)
	}
	assertLatestSchema(t, ctx, db)

	if err := goose.DownToContext(ctx, db, ".", 0); err != nil {
		t.Fatalf("reset before empty migration: %v", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("empty database to latest: %v", err)
	}
	assertLatestSchema(t, ctx, db)
}

func assertLatestSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	assertVersion(t, ctx, db, 4)
	var table, historyIndex, retentionIndex, executions, claimIndex, scheduleIndex sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('check_results'),
            to_regclass('check_results_monitor_started_idx'), to_regclass('check_results_started_idx'),
            to_regclass('scheduled_executions'), to_regclass('scheduled_executions_claimable_idx'),
            to_regclass('monitors_next_check_idx')`).
		Scan(&table, &historyIndex, &retentionIndex, &executions, &claimIndex, &scheduleIndex); err != nil {
		t.Fatal(err)
	}
	if !table.Valid || !historyIndex.Valid || !retentionIndex.Valid || !executions.Valid || !claimIndex.Valid || !scheduleIndex.Valid {
		t.Fatalf("missing schema objects: table=%v history_index=%v retention_index=%v executions=%v", table, historyIndex, retentionIndex, executions)
	}
	var columns int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
        WHERE table_schema = 'public' AND ((table_name = 'monitors' AND column_name = 'archived_at') OR
        (table_name = 'monitors' AND column_name = 'next_check_at') OR
        (table_name = 'monitor_states' AND column_name IN ('last_applied_scheduled_at', 'last_check_result_id', 'last_checked_at',
        'last_outcome', 'last_status_code', 'last_duration_ms', 'consecutive_failures', 'consecutive_successes')))`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 10 {
		t.Fatalf("new schema column count = %d, want 10", columns)
	}
	var constraints int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conname IN
        ('check_results_time_order', 'check_results_error_pair', 'check_results_outcome_error',
        'check_results_http_failure_status', 'check_results_monitor_id_fkey',
        'check_results_error_code', 'check_results_error_description_bytes',
        'scheduled_executions_identity', 'scheduled_executions_state_fields',
        'check_results_execution_id_key', 'check_results_execution_id_fkey')`).Scan(&constraints); err != nil {
		t.Fatal(err)
	}
	if constraints != 11 {
		t.Fatalf("check_results constraint count = %d, want 11", constraints)
	}
}

func assertVersion(t *testing.T, ctx context.Context, db *sql.DB, expected int64) {
	t.Helper()
	version, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if version != expected {
		t.Fatalf("migration version = %d, want %d", version, expected)
	}
}
