# Deploying GoRun on Coolify

GoRun is one container with one persistent volume. Coolify deploys it from
`deploy/docker-compose.yml`.

## 1. Create the resource

1. In Coolify: **New Resource → Docker Compose** and point it at this repository.
2. Set **Base Directory** to `/` and **Docker Compose Location** to
   `deploy/docker-compose.yml`. The compose file builds with the repository root
   as context (`context: ..`), so the Dockerfile can copy `go.mod` and `themes/`.
3. Turn on **Include Source Commit in Build** so the image reports the commit
   (`SOURCE_COMMIT`) instead of `dev`, or set a `VERSION` build variable yourself.

To run a released image instead of building, use **New Resource → Docker Image** with
`ghcr.io/zadstack/gorun:0.1` (or a full version such as `0.1.0`; `latest` follows
every release), port `8080`, and the same environment variables and `/data` volume
as below. Images are multi-arch (linux/amd64, linux/arm64).

## 2. Environment variables

| Variable | Required | Notes |
|---|---|---|
| `GORUN_SECRET_KEY` | yes | `openssl rand -base64 32`. Signs sessions and encrypts stored secrets. Never rotate casually. |
| `GORUN_BASE_URL` | yes | Public URL, e.g. `https://example.com` (no trailing slash). |
| `GORUN_SETUP_TOKEN` | for web setup | At least 16 characters (`openssl rand -hex 24`). `/admin/setup` asks for it before creating the first administrator; without it the page only shows instructions. Remove it once the first admin exists. |
| `GORUN_TRUSTED_PROXIES` | behind Traefik: yes | Comma-separated IPs/CIDRs of the reverse proxy, e.g. Coolify's `coolify` network `10.0.1.0/24` (verify, see [step 4](#trust-traefiks-forwarded-headers)). Empty trusts no forwarding headers, so every visitor shares Traefik's IP for login limits and audit. |
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

Run a single replica only: SQLite is a single-node database. GoRun enforces this
with an exclusive OS lock on `/data/gorun.lock`: a second container on the same
volume exits with "data directory is in use by another GoRun process" and names
the holder. Deploy with stop-then-start (no rolling/zero-downtime overlap) so the
old container releases the lock before the new one boots; a crashed container's
lock is released by the OS automatically.

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

### Trust Traefik's forwarded headers

GoRun trusts **no** forwarding headers by default. Behind Traefik, the TCP peer
of every request is the Traefik container, so until you list it every visitor
shares Traefik's address: the login limits (5 failures per IP + email, 30
attempts per IP per 15 minutes) then apply to the whole site at once, and the
audit log records Traefik's IP. GoRun logs a warning (at most once an hour)
when it receives `X-Forwarded-For`, `Forwarded` or `X-Forwarded-Proto` from a
peer it does not trust.

Set `GORUN_TRUSTED_PROXIES` to the address or network Traefik reaches GoRun on:

```sh
# Find the network GoRun and Traefik share (Coolify's default is "coolify").
docker network inspect coolify --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}'
docker inspect coolify-proxy --format '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}'
```

```
GORUN_TRUSTED_PROXIES=10.0.1.0/24
```

Use the narrowest value that matches: the proxy's own IP when it is stable,
otherwise the shared Docker network. Never `0.0.0.0/0` or `::/0` (rejected at
startup), and do not list networks where untrusted containers can connect to
GoRun directly. With Cloudflare in front of Traefik, see
[login security](../docs/LOGIN-SECURITY.md) before adding Cloudflare's ranges.
Only `X-Forwarded-For` (walked right to left through trusted hops) and
`X-Forwarded-Proto` are read; `Forwarded`, `X-Real-IP` and `CF-Connecting-IP`
are ignored.

Check it after deploying: sign in, open **Agents → Audit** and confirm the
recorded IP is your own public address, not a `10.x`/`172.x` proxy address,
and that the startup log has no "untrusted peer" warning.

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
