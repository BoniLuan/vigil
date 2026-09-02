-- name: LockDueMonitors :many
SELECT id, next_check_at, interval_seconds, now() AS database_now
FROM monitors
WHERE enabled
  AND archived_at IS NULL
  AND next_check_at IS NOT NULL
  AND next_check_at <= transaction_timestamp()
ORDER BY next_check_at, id
FOR UPDATE SKIP LOCKED
LIMIT sqlc.arg(batch_size);

-- name: InsertScheduledExecution :one
INSERT INTO scheduled_executions (id, monitor_id, scheduled_at)
VALUES ($1, $2, $3)
ON CONFLICT (monitor_id, scheduled_at) DO NOTHING
RETURNING *;

-- name: AdvanceMonitorSchedule :exec
UPDATE monitors
SET next_check_at = next_check_at + (
        (floor(extract(epoch FROM (transaction_timestamp() - next_check_at)) / interval_seconds) + 1)
        * interval_seconds * interval '1 second'
    ),
    updated_at = now()
WHERE id = $1;

-- name: ClaimAvailableExecutions :many
WITH claimable AS (
    SELECT e.id
    FROM scheduled_executions e
    JOIN monitors m ON m.id = e.monitor_id
    WHERE m.enabled
      AND m.archived_at IS NULL
      AND e.scheduled_at <= transaction_timestamp()
      AND (
          e.status = 'pending' OR
          (e.status = 'claimed' AND e.lease_expires_at <= transaction_timestamp())
      )
    ORDER BY e.scheduled_at, e.id
    FOR UPDATE OF e SKIP LOCKED
    LIMIT sqlc.arg(batch_size)
)
UPDATE scheduled_executions e
SET status = 'claimed',
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = transaction_timestamp() + (sqlc.arg(lease_seconds)::double precision * interval '1 second'),
    claim_count = e.claim_count + 1,
    updated_at = now()
FROM claimable
WHERE e.id = claimable.id
RETURNING e.*;

-- name: LockScheduledExecution :one
SELECT * FROM scheduled_executions WHERE id = $1 FOR UPDATE;

-- name: ScheduledExecutionLeaseActive :one
SELECT EXISTS(
    SELECT 1 FROM scheduled_executions
    WHERE id = $1 AND status = 'claimed' AND lease_expires_at > transaction_timestamp()
);

-- name: CompleteScheduledExecution :one
UPDATE scheduled_executions
SET status = 'completed', lease_owner = NULL, lease_expires_at = NULL,
    finished_at = transaction_timestamp(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetScheduledExecution :one
SELECT * FROM scheduled_executions WHERE id = $1;

-- name: SkipPendingExecutions :exec
UPDATE scheduled_executions
SET status = 'skipped', finished_at = transaction_timestamp(), updated_at = now()
WHERE monitor_id = $1 AND status = 'pending';

-- name: CanStartScheduledExecution :one
SELECT EXISTS(
    SELECT 1
    FROM scheduled_executions
    WHERE id = sqlc.arg(execution_id)
      AND status = 'claimed'
      AND lease_owner = sqlc.arg(lease_owner)
      AND lease_expires_at >= transaction_timestamp() + (sqlc.arg(required_seconds)::double precision * interval '1 second')
);
