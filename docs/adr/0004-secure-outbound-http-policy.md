# ADR 0004: Secure outbound HTTP policy

- Status: Accepted
- Date: 2026-09-01

## Context

Fetching administrator-configured URLs creates an SSRF boundary. URL validation
alone does not protect redirects, alternative IP encodings, DNS rebinding, or
connections to infrastructure metadata services.

## Decision

HTTP monitors reject private, loopback, link-local, multicast, metadata,
reserved, and otherwise internal destinations by default. Enforcement must
cover initial resolution, every redirect, and the IP actually dialed. Only HTTP
and HTTPS are allowed and resource limits apply.

Design a policy boundary that can later accept an explicit trusted-target
allowlist. v0.1 does not need to expose that allowlist.

## Consequences

Vigil is safe by default for internet-facing administration. Monitoring private
Compose-network services is intentionally unavailable until an explicit,
auditable exception mechanism exists.
