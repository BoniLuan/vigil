# ADR 0008: Ignore environment proxies for synthetic checks

- Status: Accepted
- Date: 2026-09-01

## Context

Go's default HTTP transport can honor `HTTP_PROXY`, `HTTPS_PROXY`, and
`NO_PROXY`. Routing a synthetic check through an environment-selected proxy
would prevent Vigil from controlling and verifying the actual destination path.
It could bypass destination-IP SSRF enforcement and make latency or
availability measurements describe the proxy rather than the target.

## Decision

Vigil's synthetic HTTP transport will ignore environment-provided proxy
variables by default. Part 2 will construct an explicit transport with no proxy
function and will dial only an IP approved by the destination policy.

Proxy support is out of scope. Any future proxy feature must be explicit,
validated, observable, and introduced through a superseding security review.

## Consequences

Synthetic checks have a predictable direct network path and SSRF enforcement
remains attached to the actual dial target. Environments that require outbound
proxies cannot use them implicitly.
