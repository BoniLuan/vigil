# Vigil application metrics

The API and worker processes expose Prometheus text metrics alongside `/livez`
and PostgreSQL-backed `/readyz`. These operational endpoints are unauthenticated
inside the application and must remain on the private `vigil-monitoring` Docker
network; they must not be published directly to the internet.

The API exports:

- `vigil_build_info{version,commit,role}`
- `vigil_http_requests_total{method,route,status_class}`
- `vigil_http_request_duration_seconds{method,route}`
- `vigil_db_pool_connections{state}`
- `vigil_scheduler_lag_seconds`

Scheduler lag is the database-time age of the oldest enabled monitor whose
`next_check_at` is due, or zero when none are due. Its scrape query uses the
partial `monitors_next_check_idx`; failures return `NaN` rather than blocking a
scrape indefinitely.

Routes use `http.ServeMux` patterns such as `GET /api/v1/monitors/{id}`. Monitor
IDs, names, URLs, hosts, execution IDs, IPs, and error text are never metric
labels. Build labels are bounded per deployed process.

The worker listens on `VIGIL_WORKER_HTTP_ADDR` (default `:9090`) and exports:

- `vigil_scheduler_claim_attempts_total{result}`
- `vigil_scheduler_claimed_executions_total`
- `vigil_checks_completed_total{outcome}`
- `vigil_check_duration_seconds{outcome}`
- `vigil_worker_active_checks` and `vigil_worker_capacity`
- completion-failure, panic-recovery, and lease-start-rejection counters
- build, database-pool, and scheduler-lag metrics

A check is counted when the checker returns, even if durable completion later fails. Labels are bounded enums or build metadata; monitor and execution identifiers, URLs, hosts, IPs, and error descriptions are never labels.
