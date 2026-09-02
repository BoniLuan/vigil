# ADR 0011: Durable scheduling, leases, and ordered completion

- Status: Accepted
- Date: 2026-09-01

## Context

In-memory timers cannot preserve intended executions across restarts, coordinate
multiple worker processes, or make ambiguous completion retries idempotent.
Completion order may also differ from schedule order and corrupt threshold
counters if older results overwrite newer state.

## Decision

`monitors.next_check_at` is the next intended UTC schedule boundary.
`scheduled_executions` is the durable work and identity source of truth, unique
on `(monitor_id, scheduled_at)`. A due claim transaction materializes at most
one overdue execution per monitor and advances `next_check_at` to the first
future interval boundary. Missed intervals are not replayed.

Executions move `pending -> claimed -> completed`. Pausing or archiving changes
unstarted `pending` executions to terminal `skipped`; an already claimed
execution may still complete historically. Claims use bounded batches,
`FOR UPDATE SKIP LOCKED`, a worker-instance UUID, database time, and a finite
lease. An expired claim may be reclaimed with the same execution ID.

`check_results.execution_id` is nullable for manually applied and existing
results, and unique when present. Completion locks the execution and monitor,
inserts the result, updates projection when eligible, and completes execution
in one transaction. Retrying an already committed completion returns its one
existing result without incrementing counters again.

`monitor_states.last_applied_scheduled_at` orders projection changes. A
completion newer than that value may advance state and counters. An older or
equal completion is retained as history but cannot change the projection.
This is deterministic and prevents rollback, though counters intentionally omit
late results that arrive after a newer schedule has already been applied.

## Consequences

PostgreSQL is authoritative for schedule, work identity, leases, and recovery.
There is no catch-up storm after downtime and no process-local deduplication.
The worker must use completion with its execution and worker IDs; unscheduled
`ApplyResult` remains compatible for manual use but stops advancing projection
once scheduled projection history exists.
