-- name: ListPublicStatus :many
WITH published AS (
    SELECT m.id, m.name, s.state, s.last_checked_at
    FROM monitors AS m
    JOIN monitor_states AS s ON s.monitor_id = m.id
    WHERE m.public = true AND m.archived_at IS NULL
    ORDER BY m.name
    LIMIT 100
)
SELECT p.name, p.state, p.last_checked_at,
       stats.checks_24h, stats.successes_24h
FROM published AS p
CROSS JOIN LATERAL (
    SELECT count(*)::bigint AS checks_24h,
           count(*) FILTER (WHERE r.outcome = 'success')::bigint AS successes_24h
    FROM check_results AS r
    WHERE r.monitor_id = p.id
      AND r.finished_at >= transaction_timestamp() - interval '24 hours'
) AS stats
ORDER BY p.name;
