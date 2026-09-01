# ADR 0003: Server-rendered interface

- Status: Accepted
- Date: 2026-09-01

## Decision

Use Go HTML templates for the initial administration UI. Small progressive
enhancements, including HTMX, may be evaluated when a concrete interaction
needs them. Do not create a React, Vue, or other SPA project in v0.1.

The REST API remains a documented product boundary rather than an accidental
template implementation detail.

## Consequences

Vigil ships one artifact and avoids a second build/dependency ecosystem. Rich
client-side interactions may require revisiting this choice later.
