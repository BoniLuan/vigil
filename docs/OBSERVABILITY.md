# Vigil application metrics

The API process exposes Prometheus text metrics at `GET /metrics` alongside
`/livez` and PostgreSQL-backed `/readyz`. The endpoint is unauthenticated inside
the application and must remain on a private network or behind trusted reverse
proxy controls; it must not be published directly to the internet.

Part A exports:

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

API and worker are independent processes, so the API cannot correctly export
worker-local active-check, checker-outcome, panic, or completion counters. Those
collectors and a private worker metrics listener are deferred to Part B, when
Compose networking can make the endpoint scrapeable without publishing it.
