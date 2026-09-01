# ADR 0007: Hard-delete monitors before historical data exists

Superseded by [ADR 0010](0010-archive-monitors-with-history.md).

- Status: Superseded
- Date: 2026-09-01

## Context

The monitor-model milestone has configuration and a one-to-one current-state
projection but no check results, incidents, or other historical records. Soft
deletion would require filtering every query and defining restoration semantics
without preserving meaningful history yet.

## Decision

`DELETE /api/v1/monitors/{id}` permanently deletes the monitor. Its
`monitor_states` row is removed by `ON DELETE CASCADE`. The API returns `204 No
Content`; deleting a missing monitor returns `404 Not Found`.

Revisit deletion before introducing check history. At that point, preserving
historical integrity may justify archive semantics or retaining a monitor
snapshot independently of active configuration.

## Consequences

The current model remains simple and database constraints guarantee cleanup.
Deletion is irreversible and the administration surface must present it as
such. Future historical migrations must explicitly supersede this ADR.
