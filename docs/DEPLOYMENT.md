# Single-VPS deployment

Vigil runs as two independent Compose projects behind the existing Docker-based Nginx edge proxy. The application and observability stack can therefore be upgraded separately.

## Topology and prerequisites

Install Docker Engine with Compose v2, Nginx, Certbot, and an `htpasswd` implementation. DNS for `vigil.boniluan.com` and `grafana.boniluan.com` must point at the VPS. `status.boniluan.com` serves a sanitized public status page. Its certificate must include that hostname.

The existing Docker edge proxy reaches stable service aliases on the external `web-proxy` network. Loopback bindings remain available for host-only diagnostics:

```text
vigil.boniluan.com   -> edge proxy -> vigil-api:8080
grafana.boniluan.com -> edge proxy -> grafana:3000
status.boniluan.com  -> edge proxy -> public status route only
```

PostgreSQL exists only on Vigil's internal `app` network. Prometheus, the Vigil operational listeners, Node Exporter, and cAdvisor communicate over the external `vigil-monitoring` network and publish no host ports. Only API and Grafana also join the existing external `web-proxy` network.

Create the shared network once:

```bash
docker network create vigil-monitoring
```

## Environment and image

```bash
mkdir -p /home/luan/.config/vigil
cp deploy/vigil/.env.example /home/luan/.config/vigil/vigil.env
cp deploy/observability/.env.example /home/luan/.config/vigil/observability.env
chmod 600 /home/luan/.config/vigil/*.env
```

Generate independent PostgreSQL and Grafana passwords, for example with `openssl rand -base64 32`. Put the PostgreSQL value in `POSTGRES_PASSWORD` and its URL-encoded equivalent in `VIGIL_DATABASE_URL`. Never commit these files.

Set `VIGIL_IMAGE` to an immutable release tag or digest such as `ghcr.io/boniluan/vigil:v0.1.0`. A local production-like build is also supported:

```bash
docker build --build-arg VERSION=v0.1.0 --build-arg COMMIT="$(git rev-parse --short HEAD)" --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" -t ghcr.io/boniluan/vigil:v0.1.0 .
```

For the initial VPS rollout, tag the local image with the short Git commit (for example `vigil:dda9d32`) and set `VIGIL_IMAGE` to that exact tag in `/home/luan/.config/vigil/vigil.env`.

One non-root image runs `api`, `worker`, `migrate`, and `version`. It contains embedded assets and migrations plus the CA trust store, but no compiler or source tree.

## Database and first deployment

```bash
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml up -d postgres
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml --profile tools run --rm vigil-migrate
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml up -d vigil-api
curl --fail http://127.0.0.1:8080/readyz
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml up -d vigil-worker
```

Migrations never run during API startup. Inspect services with `docker compose ... ps` and structured logs with `docker compose ... logs`.

## Observability

```bash
docker compose --env-file /home/luan/.config/vigil/observability.env -f deploy/observability/compose.yaml up -d
```

Prometheus retains 30 days in a named volume and scrapes Vigil API/worker, Node Exporter, and cAdvisor every 15 seconds. Grafana provisions its datasource and the VPS, Docker, and Vigil dashboards from Git.

Node Exporter receives read-only views of host `/proc`, `/sys`, and `/`. cAdvisor requires privileged host/runtime visibility and read-only access to Docker runtime data and `/dev/kmsg`; this elevated trust is required for container metrics. Vigil never receives Docker socket access.

## Nginx, HTTPS, and Basic Auth

`deploy/nginx/edge.override.yaml` mounts Vigil's routing into the shared edge project. Keep the shared BoniLuan edge configuration in sync so future home-site rebuilds retain the Grafana and status virtual hosts. It mounts `deploy/nginx/vigil.conf` read-only at `/etc/nginx/conf.d/vigil.conf` and `/home/luan/.config/vigil/htpasswd` at `/etc/nginx/.htpasswd-vigil`. Obtain or expand the shared `boniluan.com` certificate through its Certbot container, then validate and reload the edge proxy:
The certificate must include `boniluan.com`, `www.boniluan.com`, `finpulse.boniluan.com`, `lume.boniluan.com`, `sitio.boniluan.com`, `vigil.boniluan.com`, `grafana.boniluan.com`, and `status.boniluan.com`; the existing Certbot renewal container then renews that lineage automatically.

```bash
docker exec boniluan-certbot certbot certonly --webroot --webroot-path /var/www/certbot --cert-name boniluan.com --expand --non-interactive --agree-tos -d boniluan.com -d www.boniluan.com -d finpulse.boniluan.com -d lume.boniluan.com -d sitio.boniluan.com -d vigil.boniluan.com -d grafana.boniluan.com -d status.boniluan.com
```

```bash
ADMIN_PASSWORD="$(openssl rand -hex 24)"
printf "%s\n" "$ADMIN_PASSWORD" > /home/luan/.config/vigil/admin.password
chmod 600 /home/luan/.config/vigil/admin.password
docker run --rm httpd:2.4-alpine htpasswd -Bbn vigil-admin "$ADMIN_PASSWORD" > /home/luan/.config/vigil/htpasswd
unset ADMIN_PASSWORD
docker run --rm -v /home/luan/.config/vigil:/data alpine:3.22 chown 0:101 /data/htpasswd
chmod 640 /home/luan/.config/vigil/htpasswd
docker compose -f /home/luan/projects/boniluan/compose.yaml -f /home/luan/projects/vigil/deploy/nginx/edge.override.yaml up -d --build web
docker exec boniluan-home nginx -t
docker exec boniluan-home nginx -s reload
```

Ensure the Nginx worker can read that file. Vigil’s `/` portfolio landing page and `/assets/` are public; `/monitors`, `/api/v1`, and every other Vigil route inherit Basic Auth from the protected catch-all location. The landing-page dashboard is illustrative and does not query or expose production monitor data. Grafana uses its own authentication; anonymous access and signup are disabled. No reference route proxies metrics, exporters, Prometheus, or the worker listener.

## Upgrade, rollback, and backup

Before an upgrade, take a PostgreSQL backup and record the running image digest. Pull the new immutable image, run migrations, update API, verify readiness, then update worker:

```bash
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml pull
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml --profile tools run --rm vigil-migrate
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml up -d --no-deps vigil-api
curl --fail http://127.0.0.1:8080/readyz
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml up -d --no-deps vigil-worker
```

Rollback by restoring the previous immutable `VIGIL_IMAGE`. Migrations are forward-only: for an incompatible schema rollback, restore the pre-upgrade database backup before starting the old binary. Grafana dashboards and datasource provisioning are reproducible from Git.

Create and validate a restrictive custom-format backup without modifying production:

```bash
mkdir -p /home/luan/.config/vigil/backups
umask 077
docker exec vigil-postgres-1 pg_dump --format=custom --no-owner --no-acl -U vigil -d vigil > /home/luan/.config/vigil/backups/vigil-$(date -u +%Y%m%dT%H%MZ).dump
docker exec -i vigil-postgres-1 pg_restore --list < /path/to/the/new.dump
```

Copy backups encrypted off the VPS. A backup is not verified by archive listing alone: periodically restore it into a disposable PostgreSQL container, compare the migration version and table counts to the backup-time snapshot, then stop the container. Never restore a test archive over production. The temporary restore container can use `--network none`, ephemeral data, and a read-only backup mount. Counts against live production may increase while the worker continues checking endpoints.

## Retention

Prometheus keeps 30 days of metrics. The worker deletes `check_results` older than `VIGIL_CHECK_RESULT_RETENTION_DAYS` (default 90) in bounded batches at startup and hourly. Completed or skipped execution rows without results are removed after the same period. Pending/claimed work, monitor configuration, and current state remain. Cleanup failures are logged and retried. Check the backlog without changing data:

```bash
docker exec vigil-postgres-1 psql -U vigil -d vigil -Atc "SELECT count(*) FROM check_results WHERE started_at < now() - interval '90 days';"
```

Back up PostgreSQL before reducing retention. Large backlogs may require several hourly passes. No table partitioning is used in v0.1.

## Operations and troubleshooting

```bash
curl --fail http://127.0.0.1:8080/livez
curl --fail http://127.0.0.1:8080/readyz
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml ps
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml logs --tail=200 vigil-api vigil-worker postgres
docker compose --env-file /home/luan/.config/vigil/observability.env -f deploy/observability/compose.yaml ps
docker compose --env-file /home/luan/.config/vigil/observability.env -f deploy/observability/compose.yaml logs --tail=200 prometheus grafana node-exporter cadvisor
docker stats --no-stream

# Stop without deleting named data volumes:
docker compose --env-file /home/luan/.config/vigil/vigil.env -f deploy/vigil/compose.yaml stop
docker compose --env-file /home/luan/.config/vigil/observability.env -f deploy/observability/compose.yaml stop
```

The public project page is `https://vigil.boniluan.com/`. The private service-monitoring dashboard is `https://vigil.boniluan.com/monitors`; browser Basic Auth username is `vigil-admin`, and its password is stored at `/home/luan/.config/vigil/admin.password` (mode 0600). The related Nginx password hash is `/home/luan/.config/vigil/htpasswd`; it is not the plaintext password.

Infrastructure and application metrics dashboards are at `https://grafana.boniluan.com/`. Grafana has a separate login: username is configured as `GRAFANA_ADMIN_USER` (currently `admin`) and the password is stored at `/home/luan/.config/vigil/grafana.password` (mode 0600). Its environment file is `/home/luan/.config/vigil/observability.env`. Do not paste these credentials into tickets, chats, or the repository. The public project page, public status page, and Grafana login are different from the private Vigil monitor administration view. See [USER_GUIDE.md](USER_GUIDE.md) for what each page shows.

For a down scrape target, verify both projects join `vigil-monitoring`. cAdvisor failures commonly mean `/dev/kmsg`, cgroups, or the Docker data root differs on that host. The API and worker use read-only root filesystems, dropped capabilities, and no-new-privileges. Only the checker needs outbound access; controlled explicit-IP dialing and SSRF policy remain unchanged.
