-- name: LockMonitorProjection :one
SELECT
    m.id, m.failure_threshold, m.recovery_threshold, m.archived_at,
    s.state, s.consecutive_failures, s.consecutive_successes
FROM monitors m
JOIN monitor_states s ON s.monitor_id = m.id
WHERE m.id = $1
FOR UPDATE OF m, s;

-- name: InsertCheckResult :one
INSERT INTO check_results (
    id, monitor_id, started_at, finished_at, duration_ms, outcome,
    status_code, error_code, error_description, dialed_ip, tls_expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11
)
RETURNING *;

-- name: UpdateMonitorProjection :exec
UPDATE monitor_states SET
    state = $2,
    last_check_result_id = $3,
    last_checked_at = $4,
    last_outcome = $5,
    last_status_code = $6,
    last_duration_ms = $7,
    consecutive_failures = $8,
    consecutive_successes = $9,
    updated_at = now()
WHERE monitor_id = $1;

-- name: ListCheckResults :many
SELECT *
FROM check_results
WHERE monitor_id = $1
ORDER BY started_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: MonitorExists :one
SELECT EXISTS(SELECT 1 FROM monitors WHERE id = $1);
