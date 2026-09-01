# ADR 0010: Archive monitors after check history exists

- Status: Accepted
- Date: 2026-09-01
- Supersedes: ADR 0007

## Context

ADR 0007 chose hard deletion only while monitors had no historical results.
With durable check history, cascading a normal API delete would destroy useful
operational evidence and make future incident references unsafe.

## Decision

The normal DELETE operation archives a monitor by setting `archived_at`,
disabling it, removing public visibility, and leaving its projection paused.
Archived monitors are excluded from normal lists and future scheduling, cannot
be updated, paused, or resumed, but remain readable by known ID together with
their check history.

`check_results.monitor_id` uses `ON DELETE RESTRICT`. Physical deletion is not
part of the application API and requires an explicit future administrative
retention workflow. Slugs remain globally unique, including archived monitors,
to avoid ambiguous historical identity in v0.1.

## Consequences

Check history survives normal deletion and late in-flight results can still be
stored without reviving a monitor. Storage reclamation and permanent deletion
are intentionally deferred until a retention workflow has explicit safeguards.
