-- name: DeleteExpiredCheckResults :many
WITH expired AS (
    SELECT id FROM check_results
    WHERE started_at < transaction_timestamp() - make_interval(days => sqlc.arg(retention_days)::integer)
    ORDER BY started_at, id
    LIMIT sqlc.arg(batch_size)::integer
    FOR UPDATE SKIP LOCKED
)
DELETE FROM check_results AS results
USING expired
WHERE results.id = expired.id
RETURNING results.id;

-- name: DeleteExpiredCompletedExecutions :many
WITH expired AS (
    SELECT executions.id FROM scheduled_executions AS executions
    WHERE executions.scheduled_at < transaction_timestamp() - make_interval(days => sqlc.arg(retention_days)::integer)
      AND executions.status IN ('completed', 'skipped')
      AND NOT EXISTS (SELECT 1 FROM check_results AS results WHERE results.execution_id = executions.id)
    ORDER BY executions.scheduled_at, executions.id
    LIMIT sqlc.arg(batch_size)::integer
    FOR UPDATE OF executions SKIP LOCKED
)
DELETE FROM scheduled_executions AS executions
USING expired
WHERE executions.id = expired.id
RETURNING executions.id;
