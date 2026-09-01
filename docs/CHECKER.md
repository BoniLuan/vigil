# HTTP checker contract

The checker boundary is `monitor.Monitor -> check.Executor -> check.Result`.
It performs no persistence or monitor-state changes.

## Outcomes and errors

The stable outcomes are `success`, `http_failure`, `timeout`, `dns_error`,
`tls_error`, `connection_error`, and `internal_error`. Error codes provide the
more precise reason:

- DNS: `dns_lookup_failed`, `dns_no_addresses`, `destination_prohibited`.
- Timeout: `request_timeout` for the monitor's configured timeout,
  `deadline_exceeded` for a caller deadline, and `cancelled` for explicit caller
  cancellation. All remain the domain outcome `timeout`.
- TLS: `tls_certificate`, `tls_hostname`, and `tls_handshake_failed`.
- Connections: `connection_refused`, `connection_reset`,
  `connection_closed`, `network_unreachable`, or `connection_failed`.
- HTTP: `unexpected_status`.
- Redirect policy: `redirect_limit`, `redirect_loop`, `redirect_downgrade`, and
  `redirect_invalid`.

Typed Go errors drive classification. Certificate trust, validity, and hostname
errors are distinguished when Go exposes their typed forms. Other failures that
occur after TCP connection during TLS negotiation conservatively use
`tls_handshake_failed`; the checker does not parse platform-dependent error
text. Common typed socket errors receive specific codes, with
`connection_failed` as the portable fallback.

Result descriptions are fixed, sanitized, valid UTF-8, and limited to 512
bytes. They never include raw network errors, complete URLs, query strings,
credentials, proxy settings, or stack traces.

## Security and resource boundaries

One deadline covers DNS, all candidate IP attempts, TLS negotiation, every
redirect hop, response headers, and bounded response draining. GET response
bodies are discarded through a 32 KiB limiting reader and every body is closed;
HEAD bodies are closed without reading. Response content is never retained.

Each network hop receives a fresh transport and controlled dialer scoped to
that hop's independently resolved and approved IP set. Closing idle connections
after the hop intentionally forgoes cross-hop and cross-check connection reuse:
reusing a connection could detach a later check from its current DNS and SSRF
validation. This is an accepted v0.1 efficiency trade-off.

TLS verification is enabled with TLS 1.2 as the minimum. The logical hostname
is retained for Host, SNI, and certificate validation while TCP connects only
to an approved explicit IP. Environment proxies, automatic redirects, and
automatic compression are disabled. Redirect targets repeat URL, DNS, and IP
validation and HTTPS-to-HTTP downgrades are rejected.

The User-Agent remains `Vigil/0.1`. Build version metadata currently lives in
the CLI package; wiring it into the checker before the worker constructs an
executor would add coupling without operational value. A future constructor
option can inject `Vigil/<version>` when checker composition is implemented.

## Durable result application

`internal/checkresult.Service.ApplyResult` is the boundary from the pure checker
to PostgreSQL. Network execution finishes before a short transaction locks the
monitor and its projection, inserts one `check_results` row, updates
`monitor_states`, and commits. The scheduler milestone will add a durable
execution identity; this milestone deliberately does not invent one before
claim semantics exist.

Projection transitions are deterministic:

| Current | Check | Before threshold | At threshold |
|---|---|---|---|
| `pending` | success | — | `up` immediately |
| `pending` | failure | remain `pending` | `down` at `failure_threshold` |
| `up` | success | remain `up` | remain `up` |
| `up` | failure | remain `up` | `down` at `failure_threshold` |
| `down` | failure | remain `down` | remain `down` |
| `down` | success | remain `down` | `up` at `recovery_threshold` |
| `paused` or archived | either | state and counters unchanged | state and counters unchanged |

Success clears consecutive failures; failure clears consecutive successes. A
late result for a paused or archived monitor is retained and becomes its latest
historical result, but cannot revive it or advance threshold counters.

History reads use bounded limit/offset pagination in v0.1. Cursor pagination can
replace it when result volume demonstrates a need. `check_results` has no
speculative execution key: the scheduler/lease migration must introduce and
uniquely constrain the execution identity together with its retry semantics.
