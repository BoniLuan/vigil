# Vigil pages and data flow

Vigil has three different views:

| Address | Audience | What it shows |
| --- | --- | --- |
| `vigil.boniluan.com/` | Public | Project overview; its illustrated dashboard is **not live data**. |
| `vigil.boniluan.com/monitors` | Operator (Nginx Basic Auth) | Live monitor list, status counts, Details/Edit actions, and monitor creation. |
| `status.boniluan.com/` | Public | Only monitors explicitly marked **Public**: name, current state, last check, and 24-hour uptime. |
| `grafana.boniluan.com/` | Operator (Grafana login) | VPS, Docker, and Vigil process metrics from Prometheus. |

On the private monitor list, select **Details** for 24h/7d/30d/90d uptime, latency, recent checks, TLS expiry, and pause/resume/archive controls. Select **Edit** to change URL, method, interval, expected status, thresholds, name, or public visibility. The list and detail page refresh every 30 seconds. Editing and actions stay behind Basic Auth.

The public status page does **not** show URLs, IP addresses, error details, monitor IDs, or private monitors. It has no incident timeline yet. A public monitor's `pending` or `paused` state is shown honestly rather than reported as healthy. Its 24-hour uptime is successful completed checks divided by all completed checks in the window; no checks means N/A.

## What happens to a check

1. PostgreSQL records when a monitor is due. The worker claims a scheduled execution with a lease.
2. The checker resolves the hostname, rejects unsafe destinations, and dials only a validated IP. It follows at most five separately validated redirects.
3. A single transaction saves the result and updates the monitor's current-state projection. Thresholds control `up`/`down` transitions; target failures still count as completed executions.
4. The admin and public pages read the projection; Grafana reads Prometheus metrics. Neither page performs checks itself.
5. The worker deletes check results older than 90 days by default in bounded hourly batches. It retains monitors and current state. Prometheus keeps 30 days of metrics.

For passwords and deployment commands, see [DEPLOYMENT.md](DEPLOYMENT.md). Do not put those credentials in the repository or share them in screenshots.
