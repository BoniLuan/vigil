# ADR 0001: Go modular monolith

- Status: Accepted
- Date: 2026-09-01

## Context

Vigil needs an HTTP API, scheduled concurrent checks, and operationally simple
deployment on one VPS. Independent microservices would add failure modes and
release coordination without a current scaling requirement.

## Decision

Use Go and one modular codebase. Build one `vigil` application image exposing
separate `api`, `worker`, and `migrate` commands. Run API and worker as separate
processes while preserving domain-oriented internal boundaries.

## Consequences

The application has a small runtime footprint and direct concurrency model.
Processes can restart or scale independently, but deploy from one artifact.
Module boundaries must be maintained in code rather than through network calls.
