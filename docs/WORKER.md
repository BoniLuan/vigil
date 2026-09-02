# Worker runtime

The `vigil worker` process continuously composes Vigil's durable scheduling and
HTTP-checking boundaries:

```text
API process
    |
PostgreSQL
    |
worker process -> due claims -> bounded execution slots -> safe HTTP checker
                                                    |
                                  atomic CompleteExecution
                                                    |
                                  check_results + monitor_states
```

PostgreSQL remains the schedule and lease authority. One process-lifetime UUID
owns claims. The worker claims no more executions than its currently free
slots, so leased work is not held in a local queue. The default pool has five
slots and polls every second. Claim errors back off for 1, 2, then 5 seconds and
reset after a successful poll.

Before starting an HTTP request, the worker reloads the monitor's current
configuration and asks PostgreSQL whether its lease has at least the monitor
timeout plus a five-second completion margin remaining. Insufficient leases are
left claimed and recover through normal expiry; no synthetic health result is
created. A scheduled execution therefore uses configuration current at
execution time rather than a configuration snapshot. Paused or archived
monitors are not started, while completion of an already-running check remains
historical without reviving the monitor.

Target failures are ordinary checker results and complete their execution.
Database, orchestration, process, or recovered-panic failures do not fabricate a
target result; the lease expires for safe reclaim. A completion ambiguity may
cause the target to be checked again, but execution identity and the unique
result relationship prevent duplicate persistence.

On SIGINT or SIGTERM the process stops polling and claiming immediately. Active
checks receive the configured shutdown grace period (35 seconds by default).
Only when that grace expires are their contexts cancelled. The worker also runs a private operational listener on `VIGIL_WORKER_HTTP_ADDR`
(default `:9090`) with `/livez`, PostgreSQL-backed `/readyz`, and `/metrics`.
Production Compose exposes it only to the shared monitoring network.
