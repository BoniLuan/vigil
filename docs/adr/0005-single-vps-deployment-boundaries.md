# ADR 0005: Single-VPS deployment boundaries

- Status: Accepted
- Date: 2026-09-01

## Decision

Operate two Docker Compose projects with independent lifecycles:

- Vigil: API, worker, and PostgreSQL.
- Observability: Prometheus, Grafana, Node Exporter, and cAdvisor.

Connect them through an explicitly created shared Docker network. Nginx is the
public TLS reverse proxy. Protect the v0.1 administration host with Nginx Basic
Auth; do not implement application users yet. The status host is independently
public and receives only explicitly sanitized public data.

## Consequences

Application deploys do not restart infrastructure telemetry. Cross-project
network creation and ownership must be documented. Proxy configuration is part
of the v0.1 security boundary, and the application must never assume public and
administrative representations contain the same fields.
