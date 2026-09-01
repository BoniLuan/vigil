-- name: CreateMonitor :one
INSERT INTO monitors (
    id, name, slug, description, kind, url, http_method,
    expected_status_min, expected_status_max, interval_seconds, timeout_ms,
    failure_threshold, recovery_threshold, enabled, public
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14, $15
)
RETURNING *;

-- name: CreateMonitorState :one
INSERT INTO monitor_states (monitor_id, state)
VALUES ($1, $2)
RETURNING *;

-- name: GetMonitor :one
SELECT
    m.id, m.name, m.slug, m.description, m.kind, m.url, m.http_method,
    m.expected_status_min, m.expected_status_max, m.interval_seconds,
    m.timeout_ms, m.failure_threshold, m.recovery_threshold, m.enabled,
    m.public, m.version, m.created_at, m.updated_at,
    s.state, s.updated_at AS state_updated_at
FROM monitors m
JOIN monitor_states s ON s.monitor_id = m.id
WHERE m.id = $1;

-- name: ListMonitors :many
SELECT
    m.id, m.name, m.slug, m.description, m.kind, m.url, m.http_method,
    m.expected_status_min, m.expected_status_max, m.interval_seconds,
    m.timeout_ms, m.failure_threshold, m.recovery_threshold, m.enabled,
    m.public, m.version, m.created_at, m.updated_at,
    s.state, s.updated_at AS state_updated_at
FROM monitors m
JOIN monitor_states s ON s.monitor_id = m.id
ORDER BY m.created_at DESC, m.id DESC
LIMIT $1 OFFSET $2;

-- name: UpdateMonitor :one
UPDATE monitors SET
    name = $2,
    slug = $3,
    description = $4,
    url = $5,
    http_method = $6,
    expected_status_min = $7,
    expected_status_max = $8,
    interval_seconds = $9,
    timeout_ms = $10,
    failure_threshold = $11,
    recovery_threshold = $12,
    public = $13,
    updated_at = now(),
    version = version + 1
WHERE id = $1 AND version = $14
RETURNING *;

-- name: SetMonitorEnabled :one
UPDATE monitors SET
    enabled = $2,
    updated_at = now(),
    version = version + 1
WHERE id = $1
RETURNING *;

-- name: SetMonitorState :one
UPDATE monitor_states SET
    state = $2,
    updated_at = now()
WHERE monitor_id = $1
RETURNING *;

-- name: LockMonitor :one
SELECT id FROM monitors WHERE id = $1 FOR UPDATE;

-- name: DeleteMonitor :one
DELETE FROM monitors WHERE id = $1 RETURNING id;
