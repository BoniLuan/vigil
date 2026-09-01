# ADR 0009: Revalidate every HTTP redirect destination

- Status: Accepted
- Date: 2026-09-01

## Context

Following redirects with Go's default HTTP client can resolve and dial a new
hostname outside Vigil's destination-validation boundary. Redirects can
therefore bypass an otherwise correct SSRF policy. A redirect from HTTPS to
HTTP also silently removes transport confidentiality.

## Decision

Vigil follows redirects with an explicit executor loop, with at most five
redirect hops after the initial request. Every hop resolves its logical
hostname independently, rejects the complete DNS result if any address is
prohibited, and dials only the approved explicit addresses. The logical
hostname remains the HTTP Host and TLS ServerName.

HTTPS-to-HTTP redirects are rejected. HTTP-to-HTTP, HTTP-to-HTTPS, and
HTTPS-to-HTTPS redirects are allowed. GET remains GET and HEAD remains HEAD for
301, 302, 303, 307, and 308 because Vigil sends no request body. Each hop builds
fresh minimal headers rather than copying headers from the previous request.
All hops share the original check deadline, and every response body is bounded
and closed before continuing.

## Consequences

Redirects preserve Vigil's SSRF, proxy, Host, and TLS hostname guarantees.
Checks cannot follow downgrade redirects, more than five redirects, or redirect
targets containing credentials or unsupported schemes. This deliberately
differs from browser behavior and may mark some otherwise reachable endpoints
as failed.
