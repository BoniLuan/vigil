# Vigil

Vigil is a planned self-hosted monitoring and observability platform for a
single Linux VPS. It will actively check websites, APIs, and health endpoints,
track their availability and latency, manage incidents, and publish a focused
service-status experience. A standard Prometheus and Grafana stack will provide
host and container observability alongside it.

Vigil now has an end-to-end v0.1 monitoring path: durable PostgreSQL scheduling,
a bounded worker runtime, the security-hardened HTTP checker, atomic history and
current-state projection, and monitor administration APIs.

## Planned shape

- Go modular monolith, exposed as separate API and worker processes
- PostgreSQL for configuration, check history, incidents, and delivery state
- Server-rendered administration and public status pages backed by a REST API
- Prometheus, Grafana, Node Exporter, and cAdvisor for engineering observability
- Docker Compose deployment behind an existing Nginx HTTPS reverse proxy

## Documentation

- [Architecture and technical decisions](docs/ARCHITECTURE.md)
- [Release roadmap and implementation order](docs/ROADMAP.md)
- [Architecture decision records](docs/adr/README.md)
- [Worker runtime and lifecycle](docs/WORKER.md)
- [Application metrics](docs/OBSERVABILITY.md)
- [Single-VPS deployment runbook](docs/DEPLOYMENT.md)

## Foundation commands

The project targets Go 1.27 and uses PostgreSQL. Copy `.env.example` to `.env`
for local values, then export that file through your preferred environment
loader. Vigil deliberately does not parse `.env` files itself.

```bash
make db-up
make migrate
go run ./cmd/vigil api
# In a separate process:
go run ./cmd/vigil worker
```

With the API running:

```text
Admin UI:  http://localhost:8080/monitors
Metrics:   http://localhost:8080/metrics
Liveness:  http://localhost:8080/livez
Readiness: http://localhost:8080/readyz
```

The worker exposes private operational endpoints on port `9090` by default. Production Compose does not publish that port to the host.

Available process commands are:

```text
vigil api
vigil worker
vigil migrate [up|status|version]
vigil version
```

Quality checks:

```bash
make fmt
make test
make test-race
make lint
make build
```

Vigil implements a minimal server-rendered administration dashboard, fixed-window uptime summaries, Prometheus-compatible API and worker metrics, HTTP monitor configuration, secure HTTP execution, durable scheduling and leases, bounded worker concurrency, check history, threshold-based current-state projection, and administration endpoints under `/api/v1/monitors`. The production deployment includes a separately managed Prometheus, Grafana, Node Exporter, and cAdvisor stack. Incidents and notifications remain intentionally unimplemented. See [`api/openapi.yaml`](api/openapi.yaml) for the
current API contract.

PostgreSQL integration tests require an isolated database whose name ends in
`_test`:

```bash
make test-db-create
make test-integration
```
