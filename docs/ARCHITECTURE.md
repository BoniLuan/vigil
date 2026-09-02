# Vigil Architecture

Status: accepted baseline
Last updated: 2026-09-01

## 1. Purpose and scope

Vigil is a self-hosted control plane for active service monitoring. It answers
questions such as: Is an application reachable? How quickly is it responding?
When did it fail and recover? Is its TLS certificate approaching expiry? Who
was notified?

It complements, rather than replaces, an infrastructure telemetry stack.
Vigil owns service definitions, scheduled synthetic checks, check history,
incident lifecycle, notifications, and public service status. Prometheus,
Grafana, Node Exporter, and cAdvisor own host/container metrics, dashboards,
and exploratory engineering observability.

The initial operating environment is one personal Linux VPS. The design should
be reliable and production-oriented without paying the operational cost of
microservices, Kubernetes, or distributed infrastructure prematurely.

## 2. Technology recommendation

### Selected backend: Go (accepted)

Go is the strongest fit for this project:

- Goroutines and the standard HTTP stack suit many concurrent, timeout-bound
  network checks without a heavy runtime or complicated concurrency model.
- A small statically linked binary and low baseline memory usage are attractive
  on a VPS and simplify container images, startup, and graceful shutdown.
- Its ecosystem is particularly relevant to infrastructure engineering:
  Prometheus, Docker, Kubernetes, Grafana components, and many exporters are
  written in Go.
- Explicit error handling, contexts, interfaces at real boundaries, and strong
  tooling encourage maintainable worker and API code.
- It adds meaningful career and portfolio value beyond demonstrating another
  CRUD application in a familiar stack.

The trade-off is learning cost: Go has less batteries-included application
structure than FastAPI and less UI ecosystem than TypeScript. SQL, validation,
authentication, and templates require deliberate choices. That is acceptable
here because the backend and operational system are the portfolio focus.

### Alternatives considered

| Option | Strengths | Costs for Vigil | Decision |
|---|---|---|---|
| Go | Excellent concurrency, small footprint, static binary, infrastructure ecosystem, simple deployment | Newer language, some application plumbing is explicit | **Select** |
| TypeScript/Node.js | Strong ecosystem, productive APIs, shared frontend language, good async I/O | Larger runtime/dependency surface, worker CPU/concurrency needs more care, less distinctive for this infrastructure-focused project | Do not select |
| Python/FastAPI | Fast API development, excellent validation/docs, familiar, rich libraries | Higher memory footprint, async/sync boundaries and worker concurrency require discipline, packaging/deployment less compact | Do not select |

### Proposed supporting stack

- Go current stable release, pinned in `go.mod`, CI, and container builds.
- Standard `net/http` (or a deliberately small router such as `chi`) rather
  than a large framework.
- PostgreSQL 16+ as the sole application datastore initially.
- `pgx` for PostgreSQL access and explicit SQL; `sqlc` may generate type-safe
  query code. Avoid an ORM that hides query behavior.
- Versioned SQL migrations using Goose or Atlas. Pick one before scaffolding;
  Goose is the simpler initial recommendation.
- Structured JSON logging using Go's `log/slog`.
- Prometheus Go client for `/metrics`.
- OpenAPI 3.1 as the REST contract; generate documentation, but avoid making
  code generation the architecture.
- HTML templates plus a small amount of progressive enhancement (optionally
  HTMX later). Do not create a separate SPA initially.
- Multi-stage Docker build with a minimal non-root runtime image.

## 3. High-level architecture

```text
Internet
   |
   v
Nginx (TLS, routing, security headers, rate limits)
   |----------------------+-----------------------+
   v                      v                       v
vigil.boniluan.com   status.boniluan.com   grafana.boniluan.com
   |                      |                       |
   +----> Vigil API/UI <--+                       v
              |                              Grafana
              |                                 |
              v                                 v
          PostgreSQL                     Prometheus
              ^                           /      \
              |                          v        v
        Vigil worker                Node Exporter cAdvisor
          |      |
          v      v
   monitored endpoints         notification providers
```

Vigil is a modular monolith compiled into one image. The same binary can expose
subcommands such as `vigil api`, `vigil worker`, and `vigil migrate`. Compose
runs the API and worker as separate processes so they can restart, scale, and
receive least-privilege configuration independently while sharing code and a
database schema.

### Internal modules

Use domain-oriented packages rather than generic controller/service/repository
folders. Suggested boundaries are monitors, checks, incidents, notifications,
status pages, and platform/auth. Domain rules should not depend on HTTP or a
particular database adapter.

## 4. Component responsibilities

| Component | Responsibility |
|---|---|
| Vigil API/UI | REST API, validation, monitor management, status queries, minimal admin UI, public status page, readiness/liveness, metrics |
| Vigil worker | Scheduling, claiming due monitors, bounded concurrent HTTP/TLS checks, persistence, incident transitions, notification outbox processing |
| PostgreSQL | Durable source of truth, coordination leases, check history, incidents, notification outbox/delivery state |
| Prometheus | Scrape and retain numeric infrastructure and Vigil runtime metrics; evaluate infrastructure alert rules if added |
| Grafana | Infrastructure dashboards, PromQL exploration, engineering alerts/visualization |
| Node Exporter | Host CPU, memory, filesystems, load, disk, and network metrics |
| cAdvisor | Docker/container resource and lifecycle metrics |
| Nginx | TLS termination, hostname routing, request limits, security headers, optional temporary access control |

## 5. Proposed repository structure

```text
.
├── cmd/vigil/                 # binary entry point and subcommands
├── internal/
│   ├── monitor/               # monitor domain and use cases
│   ├── check/                 # execution, evaluation, scheduling
│   ├── incident/              # incident state machine
│   ├── notification/          # outbox and provider adapters
│   ├── statuspage/            # public projections
│   ├── platform/
│   │   ├── config/
│   │   ├── database/
│   │   ├── httpserver/
│   │   ├── logging/
│   │   └── telemetry/
│   └── testutil/
├── migrations/                # ordered, immutable SQL migrations
├── web/
│   ├── templates/             # embedded server-rendered templates
│   └── static/                # small embedded CSS/JS assets
├── api/openapi.yaml           # public/admin API contract
├── deploy/
│   ├── compose/               # production Compose and env examples
│   ├── nginx/                 # reference virtual-host configuration
│   ├── prometheus/            # scrape and alert configuration
│   └── grafana/               # provisioned datasources/dashboards
├── docs/                      # architecture, ADRs, operations, security
├── scripts/                   # narrowly scoped developer/ops helpers
├── .github/workflows/         # CI and controlled deployment
├── compose.yaml               # local development stack
├── Dockerfile
├── Makefile
├── .env.example
└── README.md
```

Not all directories should be created before their contents exist.

## 6. Data model

Use UUIDs (prefer UUIDv7 if supported cleanly) for externally visible IDs and
`timestamptz` in UTC. Store durations as integer milliseconds. Keep raw check
history normalized and derive most aggregates initially.

### Initial entities

#### `monitors`

- `id`, `name`, `slug`, `description`
- `kind` (`http` initially)
- `url`, `http_method`, encrypted-or-redacted sensitive headers if later allowed
- `expected_status_min`, `expected_status_max`
- `interval_seconds`, `timeout_ms`
- `failure_threshold`, `recovery_threshold`
- `enabled`, `public`
- `created_at`, `updated_at`, optional `archived_at`

Normal API deletion archives monitors; it disables scheduling and public visibility while preserving configuration and check history.

Validate a safe interval/timeout relationship. The initial worker should support
GET/HEAD only and should not support arbitrary request bodies or secrets until
their security model is designed.

#### `check_results`

- `id`, `monitor_id`, `started_at`, `finished_at`, `duration_ms`
- `outcome` (`success`, `http_failure`, `timeout`, `dns_error`, `tls_error`,
  `connection_error`, `internal_error`)
- `status_code`, `error_code`, bounded/sanitized `error_message`
- `dialed_ip` where available, `tls_expires_at`

Indexes should support `(monitor_id, started_at desc)` and time-based retention.
Do not store response bodies. Monthly partitioning is an evolution step only
after volume warrants it.

#### `monitor_states`

One row per monitor containing the current projection: last result/time/outcome/status/duration, consecutive successes/failures, effective status (`pending`, `up`, `down`, `paused`), and `updated_at`. This makes dashboard
reads cheap while `check_results` remains the audit history.

#### `incidents`

- `id`, `monitor_id`, `status` (`open`, `resolved`)
- `cause`, `summary`
- `started_at` (time of first failure in the triggering sequence)
- `opened_at` (time threshold was crossed), `resolved_at`
- opening and closing check-result IDs
- timestamps

An invariant/partial unique index should allow at most one open incident per
monitor.

#### Notification entities (v0.2)

- `notification_channels`: provider, name, encrypted configuration/secret,
  enabled state.
- `monitor_notification_channels`: many-to-many routing.
- `notification_deliveries`: incident/event, channel, event type, idempotency
  key, attempt count, next attempt, status, timestamps, bounded error.

The delivery table is a transactional outbox: an incident transition and its
notification intent commit atomically.

#### Later entities

Maintenance windows, status-page groups/components, users/sessions, audit log,
and API tokens should be introduced only with their corresponding feature.

### Relationships

```text
monitor 1──1 monitor_state
monitor 1──* check_result
monitor 1──* incident
incident 1──* notification_delivery *──1 notification_channel
monitor  *──* notification_channel
```

## 7. Worker design

The worker should use a database-backed scheduler, not one timer goroutine per
monitor and not an external queue in v0.1.

1. Materialize at most one overdue execution per due, enabled monitor in a
   bounded batch, using `monitors.next_check_at` as the next intended boundary.
2. Advance `next_check_at` directly to the first future interval boundary and
   claim pending or lease-expired execution rows with `FOR UPDATE SKIP LOCKED`.
3. Submit claims to a bounded worker pool. Global concurrency and optional
   per-host concurrency are configuration values.
4. Execute each request with a context deadline, a dedicated HTTP client,
   bounded redirects, safe DNS/IP validation, limited headers/body reads, and
   connection reuse.
5. Classify the result consistently and persist it in a short transaction that
   also updates monitor state and performs any incident transition.
6. Release/expire the lease. A crashed worker leaves a recoverable lease, not a
   permanently stuck monitor.

Scheduling uses the intended schedule rather than `completion + interval`, so
completion latency cannot cause drift. `scheduled_executions` is the durable
work ledger; its unique `(monitor_id, scheduled_at)` identity survives claims,
lease recovery, and completion retries. After downtime Vigil creates at most
one overdue execution per monitor instead of replaying every missed interval.
Database time governs due checks and finite lease expiry.

Completion stores the result, advances the current-state projection when the
execution is newer than `monitor_states.last_applied_scheduled_at`, and marks
the execution completed in one transaction. Older executions that finish late
remain in history but cannot roll counters or current state backward. See ADR
0011 for the full state, pause/archive, and idempotency semantics.

Checks should normally not be retried immediately: a timeout is useful evidence
and the next scheduled check is the retry. Notification delivery, by contrast,
should retry transient failures with exponential backoff, jitter, an attempt
limit, and a durable failed state.

Graceful shutdown stops claiming work, cancels or drains in-flight checks up to
a deadline, flushes telemetry/logs, and releases what can safely be released.

## 8. Incident state machine

Incident detection must be deterministic and transactional per monitor.

- A failed result increments consecutive failures and clears consecutive
  successes.
- When failures reach `failure_threshold` and there is no open incident, open
  one. Its `started_at` is the first failure of that sequence, not merely the
  threshold-crossing time.
- A successful result increments consecutive successes and clears consecutive
  failures.
- When successes reach `recovery_threshold` and an incident is open, resolve it
  using the successful check time.
- Pausing/disabling a monitor stops scheduling but does not fabricate a
  recovery. Its UI status becomes paused and policy for an already open incident
  must be explicit.

Lock the `monitor_states` row while applying a result, enforce one active
incident in the database, and attach a unique notification idempotency key.
This makes duplicate or out-of-order processing harmless. Start with a default
failure threshold of 3 and recovery threshold of 1, both configurable.

## 9. Infrastructure observability integration

Prometheus scrapes:

- Vigil API and worker `/metrics` endpoints over the private Compose network.
- Node Exporter for host metrics. Mount only the host paths it requires,
  read-only, and do not publish it publicly.
- cAdvisor for container metrics. It requires sensitive host/container runtime
  mounts; pin its version, make mounts read-only, and keep it private.

Grafana uses Prometheus as a provisioned datasource. Commit curated dashboards
for VPS health, Docker containers, and Vigil internals. PostgreSQL should not be
used as a Prometheus substitute for infrastructure data. Vigil's user-facing
uptime/incident calculations come from its database, while Prometheus observes
the health and performance of Vigil itself.

Useful Vigil metrics include checks by outcome, check duration histogram,
scheduler lag, due/claimed checks, active checks, open incidents, notification
delivery outcomes, DB pool metrics, HTTP request count/duration, and build info.
Avoid labels containing monitor names, URLs, error messages, incident IDs, or
other unbounded values.

## 10. Product boundary: build versus delegate

Vigil should build:

- Monitor CRUD and configuration validation.
- Synthetic HTTP/TLS checks and durable history.
- Current service state, uptime and latency summaries.
- Incident lifecycle and service-centric notifications.
- Minimal administration UI and a polished public status page.
- Domain-specific REST API and operational endpoints.

Delegate to Prometheus/Grafana/exporters:

- Host and container metric collection/retention.
- PromQL, dashboard construction, ad hoc metric exploration.
- Infrastructure alerting such as disk-full or host-memory pressure.
- General log aggregation and distributed tracing, unless later justified.

Do not embed Grafana panels into Vigil merely to appear integrated. Link to
Grafana for engineering detail and keep credentials/access policies separate.

## 11. Initial API design

Prefix versioned application routes with `/api/v1`. Use JSON, consistent problem
details (`application/problem+json`), strict validation, request IDs, pagination,
and UTC RFC 3339 timestamps.

### Administration API

- `POST /api/v1/monitors`
- `GET /api/v1/monitors`
- `GET /api/v1/monitors/{id}`
- `PATCH /api/v1/monitors/{id}`
- `DELETE /api/v1/monitors/{id}` (prefer disable/soft delete semantics)
- `POST /api/v1/monitors/{id}/pause`
- `POST /api/v1/monitors/{id}/resume`
- `GET /api/v1/monitors/{id}/checks?cursor=&from=&to=`
- `GET /api/v1/monitors/{id}/summary?window=24h`
- `GET /api/v1/incidents?status=&monitor_id=&cursor=`
- `GET /api/v1/incidents/{id}`

Creation returns `201` with `Location`; asynchronous commands can return `202`.
Use cursor pagination for check history. PATCH should distinguish omitted and
zero values. Define stable outcome/status enums in OpenAPI.

### Public API

- `GET /api/v1/public/status`
- `GET /api/v1/public/incidents?cursor=`

Only explicitly public monitors and sanitized incident data appear here. Never
leak internal URLs, error strings, headers, IPs, or private monitor metadata.

### Operational endpoints

- `GET /livez`: process alive; no dependency checks.
- `GET /readyz`: required dependencies available and migrations compatible.
- `GET /metrics`: private Prometheus scrape endpoint.

Operational routes should be excluded from access-log noise where appropriate
and never exposed publicly without network controls.

## 12. Single-VPS deployment

Use Docker Compose with explicit networks:

- `edge`: Nginx to Vigil API and Grafana.
- `app`: Vigil API/worker to PostgreSQL.
- `monitoring`: Prometheus, Grafana, exporters, and Vigil metrics.

Only Nginx publishes ports 80/443. PostgreSQL, Prometheus, exporters, API
metrics, and worker endpoints remain internal. Grafana and the Vigil API are
routed by hostname; the same Vigil API can serve admin and public hosts while
enforcing host-aware route exposure. Alternatively, two API replicas can use
different route modes later if stronger isolation is needed.

Use named volumes for PostgreSQL, Prometheus, and Grafana data; provisioned
Grafana dashboards/config remain in Git. Pin image versions/digests, configure
health checks, restart policies, CPU/memory constraints appropriate to the VPS,
and log rotation. Run application containers as non-root with read-only root
filesystems and temporary writable mounts where practical.

Nginx terminates HTTPS (for example with Certbot-managed certificates), redirects
HTTP, sets security headers, limits request sizes/rates, and forwards trusted
proxy headers. It should not make the design Cloudflare-dependent.

Production migrations run as a one-shot, mutually exclusive deployment job
before the new application starts. Back up PostgreSQL and configuration off the
VPS, and periodically test restoration. Prometheus history may have a shorter
retention and lower backup priority than application/incident data.

## 13. Security considerations

The largest product-specific risk is SSRF because administrators configure URLs
that the server fetches. Before public or multi-user exposure:

- Permit only HTTP/HTTPS and reject credentials in URLs.
- Resolve DNS and block loopback, link-local, private, multicast, metadata, and
  other reserved IP ranges by default, checking every redirect and the actual
  dial target to resist DNS rebinding.
- Provide an explicit, documented allowlist mechanism if private services must
  be monitored; never silently weaken the default.
- Limit redirects, timeout, response bytes, header bytes, decompression, and
  concurrency. Do not persist response bodies or secret headers.

Additional controls:

- Protect the admin host from the first deployment. Initially Nginx Basic Auth
  or a VPN/IP allowlist is acceptable; application sessions with strong password
  hashing, CSRF protection, secure cookies, and rate limiting should arrive by
  v0.3 and before v1.0.
- Treat public endpoints as a separate authorization surface and rate-limit them.
- Store secrets outside Git through environment/secret files with restrictive
  permissions. Encrypt notification credentials at rest with a separate key.
- Use separate least-privilege PostgreSQL roles for migrations and runtime.
- Validate all input, use parameterized queries, escape templates, set CSP and
  other browser security headers, and keep dependencies/images scanned.
- Redact URLs/query strings, credentials, webhook tokens, and sensitive errors
  from logs and API responses.
- Keep Grafana patched, disable anonymous/admin defaults, and use a strong secret.
- Do not expose the Docker socket. cAdvisor's required access is a reviewed
  exception; the Vigil containers need no Docker socket.

## 14. Logging and observability

Emit structured JSON to stdout with timestamp, level, service/process, version,
environment, request ID or execution ID, and bounded error classification.
Development may use human-readable logs. Never use monitor URL/name as a metric
label; logs may include monitor ID but should redact sensitive URL components.

Correlate a scheduled execution from claim through result and incident event.
Instrument both API and worker with RED-style metrics and database pool metrics.
Create Grafana dashboards and alerts for scheduler lag, no recent checks,
elevated worker errors, notification backlog, API errors, disk pressure, and
database/container health. Avoid having Vigil monitor only itself on the same
VPS as the sole availability signal: an external checker is eventually required
to detect total VPS/network failure.

OpenTelemetry tracing and a log aggregation stack are deferred until there is a
real diagnostic need; structured logs plus metrics are sufficient initially.

## 15. Testing strategy

- Unit tests for URL policy, result classification, scheduling calculations,
  uptime/latency aggregation, incident state transitions, and retry policy.
- Table-driven tests and Go's race detector for concurrency-sensitive code.
- Integration tests against real PostgreSQL for migrations, claims/leases,
  constraints, transactions, and concurrent workers. Avoid mocking SQL behavior.
- HTTP checker tests with `httptest` servers covering redirects, timeout, TLS,
  status ranges, response limits, and cancellation.
- API contract/handler tests for validation, authorization, pagination, and
  public-data redaction.
- End-to-end smoke test using Compose: migrate, start API/worker, monitor a
  controlled test endpoint, observe failure/recovery.
- Migration tests from the previous released schema and backup/restore runbook
  exercises before production maturity.
- Load/soak tests later for scheduler lag, DB growth, and bounded concurrency.

Tests should be deterministic: inject clocks and ID generators at domain
boundaries, not through a framework of mocks.

## 16. CI/CD proposal

Pull-request CI in GitHub Actions should run formatting/vetting, static analysis
(`staticcheck` or `golangci-lint` with a pinned version), unit tests, PostgreSQL
integration tests, race tests where affordable, migration verification, OpenAPI
linting, and a container build. Scan dependencies, filesystem/image, and secrets
with pinned actions and least-privilege workflow permissions.

On a version tag, build a multi-stage, reproducible image, attach OCI metadata,
generate an SBOM, scan it, sign it if practical, and push an immutable tag and
digest to GHCR.

Production deployment should initially be an explicit, protected workflow or a
documented VPS pull-and-deploy command—not automatic deployment on every merge.
It should:

1. Back up/verify the database checkpoint.
2. Pull the immutable image.
3. Run the migration job once.
4. Replace API and worker with health-gated startup.
5. Run smoke checks and retain a rollback path compatible with migrations.

Prefer a self-hosted runner only after its security implications are accepted;
SSH with a narrowly scoped deploy account/key is simpler initially. Never place
long-lived broad VPS credentials in untrusted pull-request workflows.

## 17. MVP definition

The MVP is useful when one operator can configure an HTTP monitor, see current
status and recent history, trust periodic checks across restarts, and inspect
the VPS/container stack in Grafana.

Included:

- Go API and worker, PostgreSQL migrations, monitor CRUD.
- Safe HTTP GET checks with interval, expected status range, timeout, bounded
  concurrency, durable history, and state projection.
- Basic uptime percentage and latency summaries for fixed windows.
- Minimal server-rendered admin dashboard.
- Liveness, readiness, structured logs, application Prometheus metrics.
- Prometheus, Grafana, Node Exporter, and cAdvisor with baseline provisioning.
- Local and production Compose examples, tests, CI, and operating docs.

Excluded until the next release: notifications, TLS expiry alerts, advanced
body assertions, maintenance windows, multi-user teams, agents, Kubernetes,
tracing, and a SPA.

## 18. Accepted architectural decisions

The following choices were accepted on 2026-09-01 and are recorded in
[`docs/adr`](adr/README.md):

1. Go modular monolith and one image with separate API/worker commands.
2. PostgreSQL-backed leases and transactional outbox; no Redis initially.
3. Explicit SQL with `pgx` plus `sqlc`, and Goose migrations.
4. Server-rendered templates with progressive enhancement; no separate SPA.
5. SSRF-safe default blocks private IP targets, with an explicit allowlist for
   intentionally monitored internal services.
6. Initial admin protection is Nginx Basic Auth; application authentication is
   deferred beyond v0.1.
7. Retain check results for 90 days, Prometheus metrics for 30 days, and
   incidents indefinitely initially.
8. Vigil and observability use separate Compose projects connected by an
   explicit shared network.
9. v0.1 states and check-ratio uptime semantics follow ADR 0006; there is no
   degraded state.

## 19. Traps to avoid

- Splitting scheduler, checker, incidents, and notifications into microservices.
- Adding Redis/Kafka solely to demonstrate queues when PostgreSQL provides the
  required durability and coordination at this scale.
- Recreating Grafana, PromQL, log aggregation, or infrastructure alerts in Vigil.
- Launching a React SPA before the product workflows justify it.
- A goroutine/ticker per monitor, unbounded concurrency, or holding DB
  transactions open during network calls.
- Treating in-memory state as authoritative or losing schedules on restart.
- Retrying checks so aggressively that a failing target is amplified.
- Ignoring SSRF, redirect/DNS rebinding, secret redaction, and cardinality.
- Calculating every dashboard view by scanning unlimited raw history.
- Partitioning tables, introducing agents, or designing multi-tenancy before
  measured requirements exist.
- Coupling migrations to normal API startup or using irreversible migrations
  without a deployment/rollback plan.
- Claiming true availability monitoring when checker and target share one VPS;
  document this blind spot and later add an external probe.
