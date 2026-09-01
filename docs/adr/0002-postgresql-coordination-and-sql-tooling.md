# ADR 0002: PostgreSQL coordination and SQL tooling

- Status: Accepted
- Date: 2026-09-01

## Context

The first deployment needs durable application data, scheduling coordination,
and later reliable notification delivery. Its expected single-VPS scale does
not require another stateful service.

## Decision

Use PostgreSQL as the only application datastore. Use PostgreSQL leases for
scheduler coordination and a transactional outbox for future notifications.
Use pgx, sqlc, Goose, and explicit SQL. Do not add an ORM, Redis, Kafka,
RabbitMQ, or another queue without measured requirements.

## Consequences

Transactions can atomically update domain state and future outbox records.
Operational burden stays low. Long-running work must never hold database locks;
lease and query design will require careful integration and concurrency tests.
