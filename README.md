# Vigil

Vigil is a planned self-hosted monitoring and observability platform for a
single Linux VPS. It will actively check websites, APIs, and health endpoints,
track their availability and latency, manage incidents, and publish a focused
service-status experience. A standard Prometheus and Grafana stack will provide
host and container observability alongside it.

The architecture baseline is approved and the **v0.1 foundation milestone** is
in progress. The repository currently contains process/configuration/logging,
PostgreSQL connectivity, and migration foundations only; monitoring features
have not started.

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

## Foundation commands

The project targets Go 1.27 and uses PostgreSQL. Copy `.env.example` to `.env`
for local values, then export that file through your preferred environment
loader. Vigil deliberately does not parse `.env` files itself.

```bash
make db-up
make migrate
go run ./cmd/vigil api
```

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

Vigil now implements HTTP monitor configuration, the security-hardened HTTP checker, durable check history, threshold-based current-state projection, and administration endpoints under `/api/v1/monitors`. Durable scheduler claims and leases are implemented as callable primitives; checker composition, the worker pool, incidents, and notifications remain intentionally unimplemented. See [`api/openapi.yaml`](api/openapi.yaml) for the
current API contract.

PostgreSQL integration tests require an isolated database whose name ends in
`_test`:

```bash
make test-db-create
make test-integration
```
