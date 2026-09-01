# Vigil Roadmap

Status: accepted baseline
Last updated: 2026-09-01

This roadmap uses small, demonstrable releases. Each release should leave the
repository deployable, documented, and testable rather than accumulating an
unintegrated feature backlog.

## v0.1 — Reliable monitoring foundation

Goal: monitor HTTP endpoints durably and observe both Vigil and its host.

- Approve architecture decisions and add ADRs.
- Scaffold the Go module, configuration, logging, graceful shutdown, and build
  metadata with no speculative abstractions.
- Add local Compose PostgreSQL, migrations, runtime/migration roles, and DB
  integration-test support.
- Implement monitor CRUD, validation, OpenAPI contract, and minimal
  server-rendered monitor list/detail/forms.
- Implement database-backed scheduling leases, bounded worker pool, safe HTTP
  checker, check history, and current-state projection.
- Add uptime/latency summaries for documented fixed windows.
- Add `/livez`, `/readyz`, `/metrics`, request/execution correlation, and core
  operational metrics.
- Add Prometheus, Grafana, Node Exporter, and cAdvisor with provisioned baseline
  dashboards; keep exporters private.
- Add unit/integration/race/smoke tests and GitHub Actions CI.
- Document development, production Compose/Nginx deployment, backups,
  restoration, upgrades, and troubleshooting.

Exit criteria: after process or VPS restart, due checks resume without duplicate
state transitions; an operator can create a monitor and inspect current/recent
status; Grafana shows host, container, and Vigil health; CI is green.

## v0.2 — Incidents and notifications

Goal: turn check results into actionable, reliable service events.

- Implement the transactional incident state machine with configurable failure
  and recovery thresholds.
- Add incident history and duration to API/admin UI.
- Add transactional notification outbox and Discord webhook delivery with
  idempotency, retry/backoff, redaction, and delivery visibility.
- Add TLS certificate expiry collection and warning policy.
- Publish a clean, read-only status page on `status.boniluan.com` with sanitized
  current state and recent incidents.
- Add alerting/runbooks for stalled scheduler and notification backlog.

Exit criteria: a controlled failure opens exactly one incident and sends one
logical Discord alert; stable recovery resolves it and sends one recovery alert,
including across worker crashes/retries.

## v0.3 — Secure administration and operations

Goal: make routine internet-facing operation safe and maintainable.

- Add single-operator application authentication, secure sessions, CSRF,
  password reset/recovery runbook, and audit events for configuration changes.
- Add notification-channel management and secret encryption/rotation.
- Add maintenance windows and explicit paused-incident behavior.
- Implement check-result retention/batched cleanup and measure whether table
  partitioning is warranted.
- Harden Compose containers, proxy policies, backup verification, dependency
  updates, image SBOM/signing, and protected deployment workflow.
- Improve accessibility and responsive behavior of the server-rendered UI.

Exit criteria: the admin surface no longer relies on temporary proxy auth,
secrets can be rotated, maintenance does not create false incidents, and backup
restore plus deployment rollback have been exercised.

## v0.4 — Monitoring depth

Goal: support realistic API checks without becoming a general workflow engine.

- Optional expected-body substring/JSON assertion with strict size limits.
- Configurable safe headers and secret references; preserve SSRF protections.
- Manual test execution that cannot mutate incident state unless explicitly
  designed to do so.
- Status-page components/groups and clearer uptime/latency history.
- Generic webhook notifications; add email or Telegram based on actual need.
- Evaluate infrastructure Prometheus alert rules and Alertmanager separately
  from Vigil's service-incident notifications.

Exit criteria: common health/API semantics are expressible, safely testable,
and documented without arbitrary scripting.

## v1.0 — Production baseline

Goal: declare stable contracts and an operable single-VPS product.

- Stabilize v1 API, schema migration policy, configuration contract, supported
  upgrade path, and retention semantics.
- Meet defined reliability tests for scheduler recovery, idempotent incidents,
  notification delivery, graceful shutdown, and database restore.
- Complete threat model, security checklist, operator runbooks, architecture
  diagram, demo/screenshots, contribution guide, and release process.
- Add an optional external probe/dead-man signal or document integration with an
  independent uptime service so total VPS failure is detectable.
- Benchmark a stated monitor count/check interval on representative VPS
  resources and publish limits rather than claiming unlimited scale.

Agents, multi-region execution, multi-user organizations, Kubernetes, and a
separate SPA are explicitly post-v1 possibilities and require demonstrated
demand plus new architectural decisions.

## Recommended implementation order

1. Approve the decisions in `docs/ARCHITECTURE.md` and write ADRs.
2. Establish Go quality gates, configuration, process lifecycle, and local DB.
3. Design and migrate the smallest monitor/check/state schema.
4. Build monitor domain rules and REST endpoints with integration tests.
5. Build the safe HTTP checker against controlled test servers.
6. Add scheduler claims/leases and bounded concurrency; test competing workers
   and crash recovery.
7. Persist check/state atomically and expose recent history/summaries.
8. Add the minimal server-rendered administration UI.
9. Instrument Vigil, then provision Prometheus/exporters/Grafana dashboards.
10. Package and harden containers, production Compose, Nginx examples, backups,
    CI, smoke tests, and deployment documentation.
11. Release v0.1 and operate it before implementing v0.2 incidents/notifications.

## Commit-friendly sequence for v0.1

Prefer reviewable vertical commits such as:

1. `docs: record accepted architecture decisions`
2. `build: scaffold Go service and quality checks`
3. `feat: add PostgreSQL migrations and monitor model`
4. `feat: expose validated monitor API`
5. `feat: execute and classify safe HTTP checks`
6. `feat: schedule durable checks with worker leases`
7. `feat: persist check state and expose summaries`
8. `feat: add minimal monitor dashboard`
9. `ops: add Vigil metrics and observability stack`
10. `ops: add production deployment and runbooks`

Avoid mixing broad formatting, generated artifacts, schema changes, and runtime
behavior in one commit when they can be reviewed independently.
