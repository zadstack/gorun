# GoRun: upgrade and rollback runbook

Operator checklist for moving a live site from one **release tag** to another.
There is **no** in-app core updater: you swap the binary or container image, keep
the same data directory and secret key, let migrations run, smoke-test, and if
needed roll back with the **previous artifact + a pre-upgrade backup pair + the
original key**.

Short summaries also live in [GORUN-INSTALLATION.md](GORUN-INSTALLATION.md),
[PRODUCTION.md](PRODUCTION.md), and [deploy/coolify.md](../deploy/coolify.md).
Problems: [TROUBLESHOOTING.md](TROUBLESHOOTING.md). Schema rules:
[ARCHITECTURE.md](ARCHITECTURE.md#migrations-must-be-additive).

---

## 0. Rules that do not bend

| Rule | Why |
| --- | --- |
| **One process per data dir** | Exclusive lock on `<data>/gorun.lock`. Stop then start; never overlap two containers on one volume. |
| **Migrations are forward-only** | New `serve` / `gorun migrate` applies additive SQL. Rolling back the binary alone does **not** undo schema. |
| **Rollback = prior artifact + restored data + original key** | Keep the previous archive/image tag, a backup pair from **before** the upgrade, and `GORUN_SECRET_KEY` / `secret.key` outside the default archive. |
| **Pin version tags in production** | Use `ghcr.io/zadstack/gorun:X.Y.Z` (or `X.Y`), not `latest` alone. |
| **Secrets stay outside default backups** | `gorun backup` omits `secret.key` / `gorun.toml` unless `--include-secrets`. Coolify env is the secret store for Docker. |

---

## 1. Before you touch production

1. Read the target section in [CHANGELOG.md](../CHANGELOG.md) and the
   [GitHub release](https://github.com/zadstack/gorun/releases) notes for
   `vX.Y.Z`.
2. Record what you run today:
   - Binary: `./gorun version` (or `docker exec <container> /app/gorun version`).
   - Admin: **Operations → System health** shows the same build string under
     **GoRun version** (and `/api/v1/system/health` → `version`).
   - Compare to the Releases page (or `gh release view`) — this check is
     read-only; there is no auto-updater.
3. Take a **local backup pair** and copy it off the host:

```sh
# Binary / systemd host
gorun backup --data /var/lib/gorun

# Docker / Coolify (distroless image — no shell)
docker exec <container> /app/gorun backup
# then copy /data/backups/<stamp>.{db,tar.gz,manifest.json} off the volume
```

4. Confirm you still hold, separately from the archive:
   - `GORUN_SECRET_KEY` (or `secret.key`)
   - `gorun.toml` if you use one
   - Coolify / compose env (`GORUN_BASE_URL`, `GORUN_TRUSTED_PROXIES`, Stripe, S3, …)
5. Prefer a **dress rehearsal** on a restored copy (see §5) before the live cutover.
6. Keep the **previous** release archive or image tag reachable until smoke passes.

---

## 2. Upgrade — release binary

```sh
# Example: 0.1.0 → 0.1.1 (replace versions and arch)
PREV=0.1.0
NEXT=0.1.1
ARCH=linux-amd64   # or linux-arm64, darwin-amd64, darwin-arm64

# 1) Backup (already done in §1) — note the stamp
ls /var/lib/gorun/backups/

# 2) Stop the service so gorun.lock is released
sudo systemctl stop gorun   # or your process manager

# 3) Fetch + verify the new archive
curl -fLO "https://github.com/zadstack/gorun/releases/download/v${NEXT}/gorun-${NEXT}-${ARCH}.tar.gz"
curl -fLO "https://github.com/zadstack/gorun/releases/download/v${NEXT}/SHA256SUMS-${NEXT}.txt"
sha256sum --ignore-missing -c "SHA256SUMS-${NEXT}.txt"
tar xzf "gorun-${NEXT}-${ARCH}.tar.gz"
# Keep the old extract dir (or the previous tarball) for rollback.

# 4) Start from the new extract dir (themes/ must resolve)
cd "gorun-${NEXT}-${ARCH}"
GORUN_ENV=prod \
GORUN_DATA=/var/lib/gorun \
GORUN_BASE_URL=https://cms.example.com \
GORUN_SECRET_KEY='…same as before…' \
./gorun serve
# Migrations apply on serve before HTTP opens.
# Optional explicit migrate (same env; --data alone does not load a service env file):
#   ./gorun migrate --data /var/lib/gorun
```

Then run the [smoke list](#4-smoke-after-upgrade).

---

## 3. Upgrade — Docker / GHCR and Coolify

### A. Plain Docker / Compose

```sh
# 1) Backup
docker exec <container> /app/gorun backup

# 2) Stop the old container (releases /data/gorun.lock)
docker stop <container>

# 3) Pull the pinned tag and start with the SAME volume and env
docker pull ghcr.io/zadstack/gorun:X.Y.Z
docker run -d --name gorun -p 8080:8080 -v gorun-data:/data \
  -e GORUN_BASE_URL=https://cms.example.com \
  -e GORUN_SECRET_KEY='…unchanged…' \
  -e GORUN_TRUSTED_PROXIES='…unchanged…' \
  ghcr.io/zadstack/gorun:X.Y.Z
```

Compose: bump the `image:` tag (or `VERSION` build arg), then
`docker compose … up -d` with a stop-then-start policy — do not run two
replicas on `gorun-data`.

### B. Coolify

1. Backup: `docker exec <container> /app/gorun backup`; confirm Coolify still
   holds `GORUN_SECRET_KEY`, `GORUN_BASE_URL`, `GORUN_TRUSTED_PROXIES`.
2. Set the image to `ghcr.io/zadstack/gorun:X.Y.Z` (Docker Image resource), or
   rebuild from a newer commit with **Include Source Commit in Build**.
3. Redeploy **stop then start** so the old container releases `/data/gorun.lock`.
4. Migrations run on boot. Continue with [§4](#4-smoke-after-upgrade).

Coolify overview and HTTPS acceptance:
[deploy/coolify.md](../deploy/coolify.md) (§8 upgrade summary, §9 HTTPS).

---

## 4. Smoke after upgrade

Run from a host that is **not** only the container network namespace when you
are behind Traefik/Coolify (same idea as the HTTPS acceptance checklist).

| Check | Command / UI | Expect |
| --- | --- | --- |
| Version | `gorun version` / `docker exec … /app/gorun version` | Target `X.Y.Z` (not stale `dev` unless you built from source without `VERSION`) |
| Liveness | `curl -fsS https://cms.example.com/healthz` | HTTP 200 |
| Readiness | `curl -fsS https://cms.example.com/readyz` | HTTP 200 |
| Metrics (if `GORUN_METRICS`) | `curl -fsS http://127.0.0.1:8080/metrics` (Bearer if token set) | HTTP 200, body has `gorun_jobs` — see [OBSERVABILITY.md](OBSERVABILITY.md) |
| In-container | `docker exec <c> /app/gorun healthcheck` | OK (necessary but not sufficient alone) |
| Admin | Sign in | Session works; cookies `Secure` if `BASE_URL` is `https://…` |
| System health | **Operations → System health** | GoRun version matches; schema check not unexpected `fail` |
| Schema ahead | `gorun info --data …` → `unknown_migrations` | Empty on the **new** binary after a successful upgrade |
| Content / media | Open a public page + one media URL | Renders; media loads |
| Optional | Contact form / Stripe test / AI probe | Only if those features are in use — configured ≠ delivered |

If any hard check fails, go to [§6 Rollback](#6-rollback).

---

## 5. Dress rehearsal (recommended)

Upgrade a **copy**, not the live volume:

```sh
# Inspect + restore offline to a new empty directory
gorun backup-inspect --input /var/lib/gorun/backups/<stamp>.manifest.json
gorun backup-restore --input /var/lib/gorun/backups/<stamp>.manifest.json \
  --dest /var/lib/gorun-upgrade-test
# Copy operator-held secret.key / gorun.toml (or set GORUN_SECRET_KEY)
# Run the NEW binary/image with --data /var/lib/gorun-upgrade-test
# Smoke on a private port / hostname, then discard the test dir
```

Admin path while live: **Operations → Backups → Inspect / restore** writes
`<data>/restores/<stamp>/` — move that directory **out** of the live data dir
before `serve --data` on it. Details: [PRODUCTION.md](PRODUCTION.md).

---

## 6. Rollback

Use this when the new tag fails smoke, will not boot, or you must undo quickly.
Migrations already applied stay in any DB the new binary touched — that is why
you restore a **pre-upgrade** pair (or the untouched pre-upgrade data dir copy).

### A. Binary host

```sh
sudo systemctl stop gorun

# Option 1 — you kept the old data dir untouched and only swapped the binary:
#   start the PREVIOUS extract with the same GORUN_DATA and secret key.

# Option 2 — the new binary already migrated the live DB (typical):
gorun backup-restore --input /var/lib/gorun/backups/<pre-upgrade-stamp>.manifest.json \
  --dest /var/lib/gorun-rolled-back
# copy secret.key / gorun.toml (or set GORUN_SECRET_KEY)
# point the service at /var/lib/gorun-rolled-back
# run the PREVIOUS release binary from its extract dir
sudo systemctl start gorun
```

### B. Docker / GHCR / Coolify

1. Stop the new container.
2. Restore the pre-upgrade pair into a **new** directory or replacement volume
   (never merge into a half-migrated live dir). Example:

```sh
docker run --rm -v gorun-data:/data -v /var/lib/gorun-rolled-back:/restore \
  --entrypoint /app/gorun ghcr.io/zadstack/gorun:<PREV> \
  backup-restore --input /data/backups/<pre-upgrade-stamp>.manifest.json --dest /restore
```

3. Start `ghcr.io/zadstack/gorun:<PREV>` (or Coolify image pin) with the
   restored data and the **same** `GORUN_SECRET_KEY`.
4. Re-run the smoke list.

A temporary WARN `database has migrations unknown to this binary` /
System Health schema “ahead” can appear if an old binary briefly opens a DB
the new one already migrated — expected only when migrations are additive and
you intend that state. Prefer a restored pre-upgrade pair for a clean rollback.
Security-sensitive migrations that older binaries ignore are called out in
release notes (see [LOGIN-SECURITY.md](LOGIN-SECURITY.md) for an example).

---

## 7. Path cheat sheet

| Path | Artifact | Data | Lock | Docs |
| --- | --- | --- | --- | --- |
| Release binary | `gorun-X.Y.Z-<os>-<arch>.tar.gz` + `SHA256SUMS` | `GORUN_DATA` host path | stop service | §2 |
| GHCR / Docker | `ghcr.io/zadstack/gorun:X.Y.Z` | named volume `/data` | `docker stop` then run | §3.A |
| Coolify | image pin or rebuild | `gorun-data` → `/data` | stop-then-start redeploy | §3.B · [coolify.md](../deploy/coolify.md) |
| Compose | `deploy/docker-compose.yml` | `gorun-data` | no overlapping replicas | [compose](../deploy/docker-compose.yml) |

Related: [STORAGE.md](STORAGE.md) (backup schedule / retain-N),
[PRODUCTION.md](PRODUCTION.md) (HTTPS, restore cutover),
[TROUBLESHOOTING.md](TROUBLESHOOTING.md#upgrade-from-a-release-tag).

---

## العربية — ملخص سريع

لا يوجد محدّث تلقائي للنواة. الترقية = نسخة احتياطية ← إيقاف العملية (قفل
`gorun.lock`) ← ثنائي أو صورة الوسم الجديد على **نفس** مجلد البيانات والمفتاح ←
migrations عند `serve` ← فحص الصحة ودخول الأدمن. الرجوع = ثنائي/صورة سابقة +
مجلد مُستعاد من زوج النسخة قبل الترقية + المفتاح الأصلي — وليس إرجاع الملف
التنفيذي وحده. التفاصيل أعلاه؛ Coolify في
[deploy/coolify.md](../deploy/coolify.md).
