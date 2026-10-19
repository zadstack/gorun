# Deploying GoRun on Coolify

GoRun is one container with one persistent volume. Coolify deploys it from
`deploy/docker-compose.yml`, or you can run a released GHCR image. For general
production rules (HSTS, backup cutover, upgrades) see
[docs/PRODUCTION.md](../docs/PRODUCTION.md). Install overview:
[docs/GORUN-INSTALLATION.md](../docs/GORUN-INSTALLATION.md). Docs index:
[docs/README.md](../docs/README.md).

## 1. Create the resource

1. In Coolify: **New Resource → Docker Compose** and point it at this repository.
2. Set **Base Directory** to `/` and **Docker Compose Location** to
   `deploy/docker-compose.yml`. The compose file builds with the repository root
   as context (`context: ..`), so the Dockerfile can copy `go.mod` and `themes/`.
3. Turn on **Include Source Commit in Build** so the image reports the commit
   (`SOURCE_COMMIT`) instead of `dev`, or set a `VERSION` build variable yourself.

To run a released image instead of building, use **New Resource → Docker Image** with
`ghcr.io/zadstack/gorun:0.4` (or a full version such as `0.4.0`; `latest` follows
every release), port `8080`, and the same environment variables and `/data` volume
as below. Images are multi-arch (linux/amd64, linux/arm64).

## 2. Environment variables

| Variable | Required | Notes |
|---|---|---|
| `GORUN_SECRET_KEY` | yes | `openssl rand -base64 32`. Signs sessions and encrypts stored secrets. Never rotate casually. |
| `GORUN_BASE_URL` | yes | Public URL, e.g. `https://example.com` (no trailing slash). |
| `GORUN_SETUP_TOKEN` | for web setup | At least 16 characters (`openssl rand -hex 24`). `/admin/setup` asks for it before creating the first administrator; without it the page only shows instructions. Remove it once the first admin exists. |
| `GORUN_TRUSTED_PROXIES` | behind Traefik: yes | Comma-separated IPs/CIDRs of the reverse proxy, e.g. Coolify's `coolify` network `10.0.1.0/24` (verify, see [step 4](#trust-traefiks-forwarded-headers)). Empty trusts no forwarding headers, so every visitor shares Traefik's IP for login limits and audit. |
| `GORUN_HSTS_INCLUDE_SUBDOMAINS` | no | `true` only when **every** subdomain of the host already serves HTTPS. Default HSTS is already `max-age=31536000` on HTTPS responses. |
| `GORUN_HSTS_PRELOAD` | no | `true` only with the subdomain flag, and only if you intend to submit the host to the browser preload list (hard to undo). |
| `GORUN_ALLOW_PRIVATE_NET` | no | `1` lets outbound fetches (media from URL, WordPress import, billing servers) reach private networks. Home lab only; leave unset in production. |
| `STRIPE_SECRET_KEY` | for shop | Stripe secret key. |
| `STRIPE_WEBHOOK_SECRET` | for shop | Signing secret of the `/webhooks/stripe` endpoint. |
| `GORUN_S3_ENDPOINT`, `GORUN_S3_BUCKET`, `GORUN_S3_ACCESS_KEY`, `GORUN_S3_SECRET_KEY` | no | New media in an S3-compatible bucket (R2: `https://<account>.r2.cloudflarestorage.com`). Optional `GORUN_S3_REGION` (default `auto`) and `GORUN_S3_PUBLIC_URL`. A misconfigured bucket stops the server at boot. |

`GORUN_ENV=prod`, `GORUN_DATA=/data` and `GORUN_ADDR=:8080` are baked into
the image. Pin the image tag (`0.4.0` or `0.4`) in production rather than
relying on `latest` alone.

## 3. Persistent storage

The named volume `gorun-data` is mounted at `/data`. It holds the SQLite
database (`gorun.db` + WAL files), uploaded media, theme overrides and
extensions. Never copy the live database file. Take a consistent backup with the
binary itself (the image has no shell and no SQLite CLI):

```sh
docker exec <container> /app/gorun backup
# keep newest N complete local pairs (host cron / Coolify scheduled task):
docker exec <container> /app/gorun backup --retain 7
```

It writes `/data/backups/<timestamp>.db` (`VACUUM INTO`) and
`/data/backups/<timestamp>.tar.gz` (media, overrides, extensions, form uploads)
inside the volume; copy that directory off the host. **Default backups omit
`secret.key` and `gorun.toml`** (they hold the site key and other plaintext
secrets). Keep `GORUN_SECRET_KEY` and other env credentials in Coolify's secret
store separately. Only `gorun backup --include-secrets` adds those two files;
the admin UI never does.

**In-app schedule:** while the container is running, **System → Backups** can set
hours + local retain-N (secrets still excluded; no automatic S3 upload). Prefer
a Coolify scheduled task or host cron with `docker exec … backup --retain N` if
you want backups independent of admin settings or after the process stops.
Encrypted remote upload remains a manual admin action (64 MiB/file limit).

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

### Drop Traefik `launch.css` file mounts

GoRun ships the launch overlay stylesheet in the binary at
[`/static/launch.css`](../web/launch/launch.css). After upgrading to a release
that includes this asset, **remove** any Coolify/Traefik workaround that used to
inject or file-mount that path:

| Remove | Why |
| --- | --- |
| Traefik `file` / static router (or Coolify storage mount) that maps a host file to `/static/launch.css` or `/static/launch-polish.js` | The app serves both URLs from embedded assets |
| Extra `volumes:` bind of those files into the GoRun or proxy container solely for those routes | No longer required for launch overlays |
| Middleware that injects `<link>` / `<script>` for those paths **only** because the files lived outside the image | Core `/start` pages already link them; drop only the external file source |

Verify after redeploy (stop-then-start):

```sh
curl -sI "https://<your-domain>/static/launch.css" | head
curl -sI "https://<your-domain>/static/launch-polish.js" | head
curl -sI "https://<your-domain>/start" | head
# Expect: HTTP 200 and GoRun response headers (CSP / x-request-id),
# not a bare proxy file hit. /start is the customer journey; /admin/login stays staff.
```

Do **not** remove TLS, domain, or trusted-proxy Traefik labels. This note is only
about launch overlay mounts. Do not change Resend (or other mail) secrets when
dropping these mounts.

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
`GET /healthz` on the **local** listener inside the container. That is a
liveness probe only: it does **not** prove TLS, Traefik routing, or public
reachability.

Point Coolify's health check at `/healthz`, port `8080`. After the domain is
live, also probe **from outside** the container (see [§9](#9-https-acceptance-checklist)).

## 6. First run

Either open `https://<your-domain>/admin/setup` and enter the `GORUN_SETUP_TOKEN`
value with the new administrator's details, or create the administrator from the
host (the image has no shell, so call the binary directly):

```sh
docker exec <container> /app/gorun user create --admin --email you@example.com --password '...'
```

Prefer `--password-stdin` over putting the password on the command line when your
shell allows it. After setup, remove `GORUN_SETUP_TOKEN` from the Coolify env.

Switch the admin UI language with the top-bar locale control (Arabic ⇄ English).
Build the first public site under **Appearance → Build your site**, or activate a
theme under **Appearance → Themes** (activation alone does not create starter
pages). See [SITE_BUILDER.md](../docs/SITE_BUILDER.md) and
[ADMIN_WORKSPACE.md](../docs/ADMIN_WORKSPACE.md).

## 7. Backup restore (post restore UX)

Restores **never merge** into the live `/data` directory.

**While the site runs (admin):** **Operations → Backups → Inspect / restore**
(password confirmation required). The copy lands under
`/data/restores/<timestamp>/`. Move that directory **out** of the live volume
before serving it (for example copy to a new volume or host path), stop the
current container, then start GoRun with `GORUN_DATA` pointing at the moved
copy and the **original** `GORUN_SECRET_KEY`. An interrupted restore leaves
`/data/restores/.<timestamp>.partial`; delete it before retrying.

**While stopped (CLI):**

```sh
docker run --rm -v gorun-data:/data -v /var/lib/gorun-restored:/restore \
  --entrypoint /app/gorun ghcr.io/zadstack/gorun:0.4.0 \
  backup-restore --input /data/backups/<timestamp>.manifest.json --dest /restore
```

Then point a new deployment at the restored directory (or replace the volume
contents carefully offline) with the original secrets. Full cutover:
[PRODUCTION.md](../docs/PRODUCTION.md). For a standing staging site (second
resource/volume) and promote-to-live:
[STAGING.md](../docs/STAGING.md) (`gorun staging seed|promote`).

## 8. Upgrade from a release tag

Full operator runbook (binary · GHCR · Coolify · smoke · rollback):
[docs/UPGRADE.md](../docs/UPGRADE.md).

1. Backup (`docker exec … /app/gorun backup`) and confirm Coolify still holds
   `GORUN_SECRET_KEY` / `GORUN_BASE_URL` / `GORUN_TRUSTED_PROXIES`.
2. Change the image to `ghcr.io/zadstack/gorun:X.Y.Z` (or rebuild from a newer
   commit with **Include Source Commit in Build**).
3. Redeploy with **stop then start** so the old container releases `/data/gorun.lock`.
4. Check `/healthz`, admin login, and a public page. Migrations run on boot.
   Confirm **Operations → System health** shows the expected GoRun version.

Rollback: previous image tag + restored pre-upgrade backup pair + same secret
key (not image-only). See [UPGRADE.md](../docs/UPGRADE.md#6-rollback),
[installation upgrades](../docs/GORUN-INSTALLATION.md#upgrades-from-release-tags--الترقية-من-وسوم-الإصدار),
and [TROUBLESHOOTING.md](../docs/TROUBLESHOOTING.md).

Keep the original external `GORUN_SECRET_KEY` and other environment credentials
separately from backup archives. For complete file/database consistency stop
writes before backup when you need one coherent snapshot.

## 9. HTTPS acceptance checklist

One recipe for Coolify (Traefik) and generic Docker / `ghcr.io/zadstack/gorun`.
Run it after the domain is attached and `GORUN_BASE_URL` is the public
`https://…` URL (no trailing slash). Replace `https://cms.example.com` and
`<container>` with your values.

### A. Trusted proxies CIDR

```sh
# Coolify default network name is "coolify" — confirm on your host:
docker network inspect coolify --format '{{range .IPAM.Config}}{{.Subnet}}{{end}}'
docker inspect coolify-proxy --format '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}'
```

Set the narrowest match in Coolify env (or compose / `docker run -e`):

```
GORUN_TRUSTED_PROXIES=10.0.1.0/24
```

Prefer the proxy container IP when it is stable; otherwise the shared Docker
network CIDR. Never `0.0.0.0/0` / `::/0` (rejected at startup). Redeploy
stop-then-start after changing this value.

**Pass:** container logs have **no** recurring
`forwarding headers from an untrusted peer` warning after you load the site
over HTTPS. Sign in once, open **AI Agents → Audit**, and confirm the recorded
IP is your **public** address (not Traefik's `10.x` / `172.x`).

### B. Cookie `Secure`

With `GORUN_BASE_URL=https://…`, session/CSRF cookies get the `Secure` flag
even if the proxy is not yet trusted. HSTS and real client IPs still need
§A.

```sh
# Through the public HTTPS hostname (or Traefik → container with X-Forwarded-Proto):
curl -sSI https://cms.example.com/admin/login | tr -d '\r' | grep -i '^set-cookie:'
```

**Pass:** any `Set-Cookie` line includes `Secure` (and usually `HttpOnly`,
`SameSite=Lax`). Example: `gp_csrf=…; Path=/; HttpOnly; Secure; SameSite=Lax`.

### C. HSTS options

Default on every **request that GoRun treats as HTTPS** (TLS on the socket, or
`X-Forwarded-Proto: https` from a peer in `GORUN_TRUSTED_PROXIES`):

```
Strict-Transport-Security: max-age=31536000
```

```sh
curl -sSI https://cms.example.com/healthz | tr -d '\r' | grep -i '^strict-transport-security:'
```

**Pass:** header is present with `max-age=31536000`.

Optional (leave unset until you mean them):

| Variable | When |
| --- | --- |
| `GORUN_HSTS_INCLUDE_SUBDOMAINS=true` | **Every** subdomain of this host already serves HTTPS |
| `GORUN_HSTS_PRELOAD=true` | Only with the subdomain flag, and only if you will submit the host to the browser preload list (hard to undo) |

If §A is wrong, this header is **missing** even when browsers use HTTPS at the
edge — that is the usual Coolify misconfig (empty `GORUN_TRUSTED_PROXIES`).

### D. Healthcheck from outside the container

Inside (always localhost; matches the image `HEALTHCHECK` / compose):

```sh
docker exec <container> /app/gorun healthcheck
# equivalent: GET http://127.0.0.1:8080/healthz from inside the container
```

Outside (TLS + Traefik / published port — this is the acceptance gate):

```sh
curl -fsS -o /dev/null -w '%{http_code}\n' https://cms.example.com/healthz
# expect: 200
curl -fsS -o /dev/null -w '%{http_code}\n' https://cms.example.com/readyz
# expect: 200 when the DB is up
```

**Pass:** both return `200` from a host that is **not** the container network
namespace (your laptop, another VPS, or Coolify's external check URL). Green
Docker health alone is not enough.

### E. Quick matrix

| Check | Command / UI | Expect |
| --- | --- | --- |
| Trusted CIDR set | Coolify env / compose | Proxy IP or shared Docker network only |
| No untrusted-peer spam | Container logs after HTTPS hit | Warning absent (or only once while misconfigured) |
| Client IP | **AI Agents → Audit** after login | Your public IP |
| Cookie Secure | `curl -sSI https://…/admin/login` | `Set-Cookie: … Secure` |
| HSTS default | `curl -sSI https://…/healthz` | `Strict-Transport-Security: max-age=31536000` |
| Outside health | `curl -fsS https://…/healthz` | HTTP 200 |
| Outside ready | `curl -fsS https://…/readyz` | HTTP 200 |

Same env vars apply to plain `docker run` / compose with nginx, Caddy, or
Traefik in front of `ghcr.io/zadstack/gorun:0.1` — only the CIDR discovery
commands change (inspect the network your reverse proxy shares with GoRun).
Troubleshooting: [TROUBLESHOOTING.md](../docs/TROUBLESHOOTING.md#https-proxy-hsts).
Production overview: [PRODUCTION.md](../docs/PRODUCTION.md).

## Optional update button in GoRun admin

After manually deploying a version with **System → System updates**, the site
owner can request subsequent deployments from that page. This integration is for
Coolify deployments; it does not run a shell or modify the application's binary.

Configure these **server-side environment secrets** in the GoRun resource:

| Variable | Value |
| --- | --- |
| `GORUN_UPDATE_COOLIFY_URL` | Your trusted Coolify HTTPS origin, e.g. `https://coolify.example.com` (no path, query or credentials). |
| `GORUN_UPDATE_COOLIFY_TOKEN` | A Coolify API token permitted to deploy the intended resource. Use the narrowest available permissions. Never place it in a browser URL or client-side configuration. |
| `GORUN_UPDATE_COOLIFY_RESOURCE` | The single Coolify resource UUID belonging to this site. |

In Coolify, first verify the repository, branch or image tag configured for that
resource. The button deploys **that configured source**; it does not select a
GitHub commit, promise that `main` is tested, or automatically change a pinned
image tag. A resource pinned to an old release will remain on that release until
you change its tag in Coolify. Keep the existing `/data` volume and secret key,
and retain the stop-then-start strategy described above.

The button requires the `system.update` permission, a recent password sign-in,
CSRF validation and explicit confirmation. It creates and verifies a local
backup before sending an authenticated `POST /api/v1/deploy` for the configured
resource. Local backups are not a substitute for an off-host backup and a
separate copy of the site secrets. Requests are spaced ten minutes apart,
including uncertain failures, to reduce accidental duplicate deployments.

An accepted request means **queued**, not deployed. Check progress in Coolify,
then open GoRun System Health to verify the running version. A timeout can occur
after Coolify accepts a request; check there before retrying. There is no
automatic rollback, deployment polling or recovery from incompatible database
migrations in this first integration. The admin page never exposes the API token,
the deployment URL or upstream response bodies, and redirects are not followed.
Without all three environment variables, the page explains setup and keeps its
button disabled. The integration may reach an operator-selected private Coolify
host over verified HTTPS without enabling private URL fetching elsewhere.

Protocol reference: [Coolify Deploy API](https://coolify.io/docs/api/endpoints/deployments/deploy-by-tag-or-uuid).
