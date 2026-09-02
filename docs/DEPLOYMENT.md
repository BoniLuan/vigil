# Single-VPS deployment

Vigil runs as two independent Compose projects behind host-managed Nginx. The application and observability stack can therefore be upgraded separately.

## Topology and prerequisites

Install Docker Engine with Compose v2, Nginx, Certbot, and an `htpasswd` implementation. DNS for `vigil.boniluan.com` and `grafana.boniluan.com` must point at the VPS. `status.boniluan.com` remains reserved.

Host Nginx reaches only loopback-published ports:

```text
vigil.boniluan.com   -> 127.0.0.1:8080 -> vigil-api
grafana.boniluan.com -> 127.0.0.1:3000 -> grafana
```

PostgreSQL exists only on Vigil's internal `app` network. Prometheus, the Vigil operational listeners, Node Exporter, and cAdvisor communicate over the external `vigil-monitoring` network and publish no host ports.

Create the shared network once:

```bash
docker network create vigil-monitoring
```

## Environment and image

```bash
cp deploy/vigil/.env.example deploy/vigil/.env
cp deploy/observability/.env.example deploy/observability/.env
chmod 600 deploy/vigil/.env deploy/observability/.env
```

Generate independent PostgreSQL and Grafana passwords, for example with `openssl rand -base64 32`. Put the PostgreSQL value in `POSTGRES_PASSWORD` and its URL-encoded equivalent in `VIGIL_DATABASE_URL`. Never commit these files.

Set `VIGIL_IMAGE` to an immutable release tag or digest such as `ghcr.io/boniluan/vigil:v0.1.0`. A local production-like build is also supported:

```bash
docker build --build-arg VERSION=v0.1.0 --build-arg COMMIT="$(git rev-parse --short HEAD)" --build-arg BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" -t ghcr.io/boniluan/vigil:v0.1.0 .
```

One non-root image runs `api`, `worker`, `migrate`, and `version`. It contains embedded assets and migrations plus the CA trust store, but no compiler or source tree.

## Database and first deployment

```bash
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml up -d postgres
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml --profile tools run --rm vigil-migrate
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml up -d vigil-api
curl --fail http://127.0.0.1:8080/readyz
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml up -d vigil-worker
```

Migrations never run during API startup. Inspect services with `docker compose ... ps` and structured logs with `docker compose ... logs`.

## Observability

```bash
docker compose --env-file deploy/observability/.env -f deploy/observability/compose.yaml up -d
```

Prometheus retains 30 days in a named volume and scrapes Vigil API/worker, Node Exporter, and cAdvisor every 15 seconds. Grafana provisions its datasource and the VPS, Docker, and Vigil dashboards from Git.

Node Exporter receives read-only views of host `/proc`, `/sys`, and `/`. cAdvisor requires privileged host/runtime visibility and read-only access to Docker runtime data and `/dev/kmsg`; this elevated trust is required for container metrics. Vigil never receives Docker socket access.

## Nginx, HTTPS, and Basic Auth

Obtain certificates through the VPS Certbot workflow, adapt `deploy/nginx/vigil.conf`, then validate and reload:

```bash
sudo nginx -t
sudo systemctl reload nginx
sudo htpasswd -c /etc/nginx/.htpasswd-vigil vigil-admin
sudo chmod 640 /etc/nginx/.htpasswd-vigil
```

Ensure the Nginx worker can read that file. Vigil uses Basic Auth at Nginx. Grafana uses its own authentication; anonymous access and signup are disabled. No reference route proxies metrics, exporters, Prometheus, or the worker listener.

## Upgrade, rollback, and backup

Before an upgrade, take a PostgreSQL backup and record the running image digest. Pull the new immutable image, run migrations, update API, verify readiness, then update worker:

```bash
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml pull
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml --profile tools run --rm vigil-migrate
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml up -d --no-deps vigil-api
curl --fail http://127.0.0.1:8080/readyz
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml up -d --no-deps vigil-worker
```

Rollback by restoring the previous immutable `VIGIL_IMAGE`. Migrations are forward-only: for an incompatible schema rollback, restore the pre-upgrade database backup before starting the old binary. Schedule encrypted off-VPS `pg_dump --format=custom` backups and test restoration. Grafana dashboards and datasource provisioning are reproducible from Git.

## Operations and troubleshooting

```bash
curl --fail http://127.0.0.1:8080/livez
curl --fail http://127.0.0.1:8080/readyz
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml ps
docker compose --env-file deploy/vigil/.env -f deploy/vigil/compose.yaml logs --tail=200 vigil-api vigil-worker postgres
docker compose --env-file deploy/observability/.env -f deploy/observability/compose.yaml ps
docker compose --env-file deploy/observability/.env -f deploy/observability/compose.yaml logs --tail=200 prometheus grafana node-exporter cadvisor
docker stats --no-stream
```

For a down scrape target, verify both projects join `vigil-monitoring`. cAdvisor failures commonly mean `/dev/kmsg`, cgroups, or the Docker data root differs on that host. The API and worker use read-only root filesystems, dropped capabilities, and no-new-privileges. Only the checker needs outbound access; controlled explicit-IP dialing and SSRF policy remain unchanged.
