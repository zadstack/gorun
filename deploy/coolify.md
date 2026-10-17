# Deploying GoRun on Coolify

GoRun is one container with one persistent volume. Coolify deploys it from
`deploy/docker-compose.yml`.

## 1. Create the resource

1. In Coolify: **New Resource → Docker Compose** and point it at this repository.
2. Set **Base Directory** to `/` and **Docker Compose Location** to
   `deploy/docker-compose.yml`. The compose file builds with the repository root
   as context (`context: ..`), so the Dockerfile can copy `go.mod` and `themes/`.

## 2. Environment variables

| Variable | Required | Notes |
|---|---|---|
| `GORUN_SECRET_KEY` | yes | `openssl rand -base64 32`. Signs sessions and encrypts stored secrets. Never rotate casually. |
| `GORUN_BASE_URL` | yes | Public URL, e.g. `https://example.com` (no trailing slash). |
| `GORUN_SETUP_TOKEN` | for web setup | At least 16 characters (`openssl rand -hex 24`). `/admin/setup` asks for it before creating the first administrator; without it the page only shows instructions. Remove it once the first admin exists. |
| `GORUN_ALLOW_PRIVATE_NET` | no | `1` lets outbound fetches (media from URL, WordPress import, billing servers) reach private networks. Home lab only; leave unset in production. |
| `STRIPE_SECRET_KEY` | for shop | Stripe secret key. |
| `STRIPE_WEBHOOK_SECRET` | for shop | Signing secret of the `/webhooks/stripe` endpoint. |
| `GORUN_S3_ENDPOINT`, `GORUN_S3_BUCKET`, `GORUN_S3_ACCESS_KEY`, `GORUN_S3_SECRET_KEY` | no | New media in an S3-compatible bucket (R2: `https://<account>.r2.cloudflarestorage.com`). Optional `GORUN_S3_REGION` (default `auto`) and `GORUN_S3_PUBLIC_URL`. A misconfigured bucket stops the server at boot. |

`GORUN_ENV=prod`, `GORUN_DATA=/data` and `GORUN_ADDR=:8080` are baked into
the image.

## 3. Persistent storage

The named volume `gorun-data` is mounted at `/data`. It holds the SQLite
database (`gorun.db` + WAL files), uploaded media, theme overrides and
extensions. Never copy the live database file. Take a consistent backup with the
binary itself (the image has no shell and no SQLite CLI):

```sh
docker exec <container> /app/gorun backup
```

It writes `/data/backups/<timestamp>.db` (`VACUUM INTO`) and
`/data/backups/<timestamp>.tar.gz` (media, overrides, extensions, form uploads and any local recovery key/configuration) inside the
volume; copy that directory off the host.

Run a single replica only: SQLite is a single-node database.

## 4. Domain and Traefik

Set the domain on the `gorun` service in Coolify (e.g. `https://example.com`)
and Coolify generates the Traefik labels. Equivalent manual labels:

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.gorun.rule=Host(`example.com`)
  - traefik.http.routers.gorun.entrypoints=https
  - traefik.http.routers.gorun.tls=true
  - traefik.http.routers.gorun.tls.certresolver=letsencrypt
  - traefik.http.services.gorun.loadbalancer.server.port=8080
```

When a domain is set, remove the `ports:` mapping so traffic only enters
through Traefik.

## 5. Health

The container healthcheck runs `/app/gorun healthcheck`, which requests
`GET /healthz` on the local listener. Point Coolify's health check at
`/healthz`, port `8080`.

## 6. First run

Either open `https://<your-domain>/admin/setup` and enter the `GORUN_SETUP_TOKEN`
value with the new administrator's details, or create the administrator from the
host (the image has no shell, so call the binary directly):

```sh
docker exec <container> /app/gorun user create --admin --email you@example.com --password '...'
```

Backups contain secrets. Keep the original external GORUN_SECRET_KEY and other environment credentials separately. For complete file/database consistency stop writes before backup. Restore to an empty data directory while the service is stopped; see [production recovery](../docs/PRODUCTION.md).
