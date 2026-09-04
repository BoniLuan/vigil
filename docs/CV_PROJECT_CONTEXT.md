# Vigil — CV and Interview Context

Use this as factual context when writing a CV, LinkedIn project entry, portfolio
case study, or interview notes. Do not present roadmap items as implemented.

## Project

- **Name:** Vigil
- **Repository:** <https://github.com/BoniLuan/vigil>
- **Live project page:** <https://vigil.boniluan.com>
- **Type:** Open-source, self-hosted monitoring and observability platform
- **Status:** v0.1 deployed and operated on a personal Linux VPS
- **Developer:** Luan Alves Bonifacio
- **License:** MIT

Vigil performs scheduled HTTP/HTTPS health checks and presents current state,
latency, historical results, and rolling uptime through a REST API and a small
server-rendered administration UI. A separate Prometheus/Grafana stack observes
Vigil, the VPS, and its Docker containers.

## Stack and purpose

| Technology | Purpose |
|---|---|
| Go 1.27 | Backend, concurrent checker, API, worker, UI, and operational endpoints |
| PostgreSQL 17 | Domain data, durable scheduling, leases, idempotency, and state projection |
| pgx + sqlc | Explicit SQL with generated type-safe Go access |
| Goose | Immutable embedded database migrations |
| `net/http` + `html/template` | REST API and embedded server-rendered UI without an SPA toolchain |
| Docker + Docker Compose | Hardened application packaging and reproducible service topology |
| Nginx + Certbot | HTTPS edge routing and Basic Auth for administration |
| Prometheus + Grafana | Application/infrastructure metrics and provisioned dashboards |
| Node Exporter + cAdvisor | VPS and Docker-container telemetry |
| OpenAPI | Implemented REST API contract |

## Architecture

Vigil is a modular monolith: one Go codebase and image with separate commands:

```text
vigil api       API, UI, health, and metrics
vigil worker    scheduler and bounded check execution
vigil migrate   explicit database migrations
vigil version   build metadata
```

PostgreSQL is the source of truth. No Redis, queue broker, microservices, or
Kubernetes are required for the single-VPS architecture.

```text
Operator -> Nginx -> API/UI -> PostgreSQL
                              ^
Worker -> durable claim -> safe HTTP checker -> atomic completion

Prometheus -> API + worker + Node Exporter + cAdvisor -> Grafana
```

## Key engineering work

### Secure HTTP checker

- Allows only globally routable public destinations by default.
- Rejects loopback, private, unique-local, link-local, metadata, multicast,
  unspecified, reserved, and non-routable IP ranges.
- Resolves once, validates every DNS candidate, then dials only an approved
  explicit IP, preventing default-client re-resolution and DNS rebinding.
- Revalidates every redirect hop, limits redirects to five, and blocks HTTPS to
  HTTP downgrade.
- Preserves the logical hostname for HTTP Host, TLS SNI, and certificate
  verification while dialing the approved IP.
- Ignores environment proxy variables so a proxy cannot bypass destination
  control.
- Uses one global timeout across DNS, dialing, TLS, redirects, response headers,
  and a bounded 32 KiB body drain.
- Produces stable outcomes and bounded sanitized diagnostics without leaking
  credentials, query strings, proxy values, or stack traces.

### Persistence, scheduler, and worker

- Persists historical results and updates the current monitor-state projection
  atomically in PostgreSQL.
- Applies consecutive failure/recovery thresholds for `pending`, `up`, `down`,
  and `paused` states.
- Uses durable `(monitor_id, scheduled_at)` execution identity, PostgreSQL
  `FOR UPDATE SKIP LOCKED`, finite leases, and process-lifetime worker identity.
- Makes completion idempotent through a unique execution/result relationship;
  retries cannot duplicate results or state-counter changes.
- Prevents schedule drift by storing intended schedule boundaries and performs
  at most one overdue check after downtime.
- Retains out-of-order history while preventing an older result from rolling
  the current projection backward.
- Runs a bounded pool of five checks, claims only available capacity, isolates
  individual job panics, and drains active work during graceful shutdown.

### Product and operations

- Validated monitor CRUD, GET/HEAD methods, pause/resume/archive, current state,
  bounded history, and 24h/7d/30d/90d uptime and average-latency summaries.
- Provides a public product/portfolio page while monitor administration, API
  data, and operational metrics remain protected.
- Exposes bounded-cardinality Prometheus metrics for API traffic, database pool,
  scheduler lag, claims, check outcomes/duration, worker capacity, completion
  failures, and panic recovery.
- Provisions Grafana dashboards for Vigil internals, VPS resources, and Docker
  containers from repository-owned configuration.
- Runs non-root/read-only application containers on separated private networks;
  PostgreSQL, Prometheus, exporters, worker operations, and `/metrics` are not
  publicly exposed.
- Documents deployments, upgrades, rollback, backups, troubleshooting,
  architecture, and durable decisions through a runbook and ADRs.

## Production evidence

The first VPS deployment verified real 60-second public monitors, threshold
failure/recovery behavior, graceful worker restart, schedule recovery, and
private observability targets. One verification snapshot contained 37 scheduled
executions, 37 unique identities, and 37 results, each claimed once. Prometheus
reported all four targets up, all three Grafana dashboards loaded production
data, and a real custom-format PostgreSQL backup was produced. The warmed stack
used roughly 696 MiB at that point; this is an operational snapshot, not a load
or capacity benchmark.

## Testing and quality

- Table-driven domain, validation, checker, projection, and worker unit tests.
- Deterministic offline DNS, TCP, TLS, redirect, timeout, proxy, and SSRF tests.
- Real isolated PostgreSQL integration tests for migrations, constraints,
  transactions, API behavior, leases, concurrent claims, ordering, idempotency,
  pause/archive races, and end-to-end worker flow.
- Go race detector, `go vet`, formatting, sqlc drift checks, production builds,
  Docker/Compose, Prometheus, OpenAPI, Nginx, and Gitleaks validation.

## CV-ready bullets

- Built and deployed Vigil, a self-hosted monitoring platform in Go and
  PostgreSQL that performs durable HTTP/HTTPS checks and exposes current health,
  latency, rolling uptime, and historical results.
- Engineered a PostgreSQL-backed scheduler with durable execution identities,
  `FOR UPDATE SKIP LOCKED`, expiring leases, bounded concurrency, idempotent
  completion, and ordered state projection for crash-safe execution.
- Hardened outbound checks against SSRF and DNS rebinding by validating every
  DNS candidate and redirect hop, dialing only approved explicit IPs, and
  preserving the hostname for Host, SNI, and certificate verification.
- Containerized and operated the system on a Linux VPS with non-root/read-only
  services, private Docker networks, Nginx/HTTPS, Prometheus, Grafana, Node
  Exporter, cAdvisor, provisioned dashboards, and tested backup procedures.
- Built offline security regression tests and real PostgreSQL integration,
  concurrency, race, migration, API, and worker test suites.

## Scope not implemented in v0.1

Do not claim incidents, Discord/email/Telegram notifications, a live public
status page, application user/team authentication, maintenance windows, custom
headers or body assertions, multi-VPS agents, Kubernetes, distributed tracing,
or log aggregation. These remain possible later milestones.

## Instructions for a CV-writing assistant

Emphasize backend architecture, reliable execution, database concurrency,
security, DevOps, observability, and genuine production operation. Do not invent
user counts, traffic, SLA, revenue, team size, or scale. Treat production counts
and memory figures as point-in-time validation evidence rather than benchmarks.
Tailor the number and depth of bullets to the target vacancy and available space.
