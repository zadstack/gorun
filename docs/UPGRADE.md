# GoRun: upgrade and rollback runbook

Operator checklist for moving a live site from one **release tag** to another.
Operators replace the binary or container image, keep the same data directory
and secret key, let migrations run, and smoke-test. The optional in-app staged
update ([§7](#7-staged-updates-in-app)) records the same steps as independent
phases — compatibility, release verification, backup, activation, health and
links, acceptance — and never reports a queued deployment as a completed update.

If activation fails, prefer a **compatible previous artifact on the current
data**. Restoring a pre-upgrade backup is a separate disaster-recovery decision
that can lose newer writes. Never automatically replace current data with an old
snapshot. Preserve the current database, media, settings, themes and secret key
before any recovery cutover; see [§6](#6-rollback).

Short summaries also live in [GORUN-INSTALLATION.md](GORUN-INSTALLATION.md),
[PRODUCTION.md](PRODUCTION.md), and [deploy/coolify.md](../deploy/coolify.md).
A dress rehearsal on a restored copy is the same confined-restore path as
staging; for an ongoing staging site + promote-to-live cutover see
[STAGING.md](STAGING.md). Problems: [TROUBLESHOOTING.md](TROUBLESHOOTING.md).
Schema rules: [ARCHITECTURE.md](ARCHITECTURE.md#migrations-must-be-additive).

---

## 0. Rules that do not bend

| Rule | Why |
| --- | --- |
| **One process per data dir** | Exclusive lock on `<data>/gorun.lock`. Stop then start; never overlap two containers on one volume. |
| **Migrations are forward-only** | New `serve` / `gorun migrate` applies additive SQL. Rolling back the binary alone does **not** undo schema. |
| **Runtime rollback preserves current data** | Use the prior artifact only after checking schema, feature and security compatibility. Keep the original key and current database, media, settings and themes. |
| **An old snapshot is not automatic rollback** | A pre-upgrade backup omits subsequent writes. Restore only into a separate directory; cutover requires an explicit recovery decision and reconciliation of newer data. |
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
7. Review downgrade compatibility, including security behavior and theme support.
   A migration being additive is necessary, but does not prove that every prior
   binary is safe with the current data and enabled features.

---

## 2. Upgrade — release binary

```sh
# Example: 0.2.0 → 0.3.0 (replace versions and arch)
PREV=0.2.0
NEXT=0.3.0
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

`gorun version` identifies the artifact executed by that command. Confirm the
serving process through authenticated `/api/v1/system/health` → `version` (or the
System health page), especially after replacing a binary on disk. A current
runtime version, successful migrations and acceptance of optional features are
separate observations.
The additive `/api/v1/site/info` endpoint also reports the running build with
its source; `/api/v1/site/runtime` provides protected process details. See
[API.md](API.md#public-site-information-and-protected-runtime). Discovery requires
no configuration changes or data migration and preserves existing theme services.

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

Use this when the new tag fails smoke, will not boot, or must be withdrawn.
First stop or quiesce the failing process and preserve its current data, including
writes made since the pre-upgrade backup. Keep that current copy and the
pre-upgrade backup separately. Do not delete or overwrite either during diagnosis.

### A. Runtime rollback — preserve current data

Migrations already applied stay in the database. The repository's additive
migration policy supports running a compatible earlier binary against this
current database; it does not guarantee compatibility with every old release.
Check the release notes, schema inspection, feature changes, security behavior
and installed themes. Rehearse with the previous artifact against a **copy of
current data**, on a private endpoint with integrations and background side
effects disabled, before allowing it to serve live traffic.

If compatible, stop the new process and start the previous binary/image with the
**same current data directory and original secret key**, then run the smoke list.
Use stop-then-start for Docker/Coolify as well. Do not run down migrations or
restore an old database as part of this operation. If compatibility cannot be
established, keep the site in maintenance and repair forward or use the explicit
recovery procedure below.

An old binary may report `database has migrations unknown to this binary` /
System Health schema “ahead”. Inspect the unknown migrations; do not suppress
the warning as proof that the downgrade is safe. Security-sensitive migrations
that older binaries ignore are called out in release notes; see
[LOGIN-SECURITY.md](LOGIN-SECURITY.md) for an example.

### B. Disaster recovery — restore into a separate directory

Use a pre-upgrade snapshot only after explicitly deciding how to reconcile any
newer content, media, users, orders and settings, or accepting their loss. Verify
that current data has been preserved before switching traffic. A backup alone
does not capture writes made after its timestamp. These commands prepare a
separate recovery tree; they do not authorize automatic cutover.

Binary host:

```sh
sudo systemctl stop gorun

# Preserve the CURRENT data tree first; never restore into /var/lib/gorun.
gorun backup-restore --input /var/lib/gorun/backups/<pre-upgrade-stamp>.manifest.json \
  --dest /var/lib/gorun-rolled-back
# copy secret.key / gorun.toml (or set GORUN_SECRET_KEY)
# Inspect, rehearse, reconcile newer writes, and approve recovery cutover.
# Only then point the service at /var/lib/gorun-rolled-back and start
# the PREVIOUS release binary from its extract dir.
```

Docker / GHCR / Coolify:

1. Stop the new container and preserve the current volume.
2. Restore the pre-upgrade pair into a **new** directory or replacement volume
   (never merge into a half-migrated live dir). Example:

```sh
docker run --rm -v gorun-data:/data -v /var/lib/gorun-rolled-back:/restore \
  --entrypoint /app/gorun ghcr.io/zadstack/gorun:<PREV> \
  backup-restore --input /data/backups/<pre-upgrade-stamp>.manifest.json --dest /restore
```

3. Inspect and rehearse the restored tree, reconcile newer writes, and approve
   recovery cutover before starting `ghcr.io/zadstack/gorun:<PREV>` (or Coolify
   image pin) with the restored data and the **same** `GORUN_SECRET_KEY`.
4. Re-run the smoke list.

---

## 7. Staged updates (in-app)

**System → System updates** runs a persisted, phase-by-phase lifecycle instead
of a single deploy request. It is optional: without a trusted release source the
page only shows the running version, and the manual procedures above still apply.

### Configuration (server side only)

| Setting | Env / `gorun.toml` | Meaning |
| --- | --- | --- |
| Release source | `GORUN_UPDATE_SOURCE_URL` / `[update] source_url` | HTTPS JSON index (plain HTTP only on the loopback address, e.g. a local supervisor). Required for requests. |
| Source token | `GORUN_UPDATE_SOURCE_TOKEN` / `source_token` | Optional bearer sent to the index. Never shown in pages or APIs. |
| Artifact selector | `GORUN_UPDATE_ARTIFACT` / `artifact` | Which artifact of a release to activate (exact id or substring, e.g. `linux-amd64`). A release with one artifact needs none. |
| Health links | `GORUN_UPDATE_HEALTH_LINKS` / `health_links` | Absolute public or media URLs that must answer 2xx on the new process before acceptance. |
| Downgrade | `GORUN_UPDATE_ALLOW_DOWNGRADE` / `allow_downgrade` | Lets compatibility pass for a lower target after you reviewed schema, feature and security constraints. Recorded on the operation. |
| Missing digest | `GORUN_UPDATE_ALLOW_MISSING_DIGEST` / `allow_missing_digest` | Accepts releases whose index publishes no `sha256:` digest. Off by default. |
| Activation timeout | `GORUN_UPDATE_ACTIVATION_TIMEOUT` / `activation_timeout` | How long activation may wait for the provider or operator (default `15m`). |
| Coolify adapter | `GORUN_UPDATE_COOLIFY_URL/TOKEN/RESOURCE` | Existing operator configuration. The resource must be a **Docker image** application: verification reads it (`GET /api/v1/applications/{uuid}`), activation pins `docker_registry_image_name/tag` to the verified artifact (`PATCH`), confirms the pin, then posts `/api/v1/deploy` and polls `/api/v1/deployments/{id}`. A resource that builds from a repository cannot be pinned and the update is **refused at verification**, before any backup. Otherwise the **manual** adapter is used. |

Release index format (publish it from CI or a local supervisor):

```json
{"latest":"0.15.0",
 "releases":[{"version":"0.15.0",
              "artifacts":[{"id":"ghcr.io/zadstack/gorun:0.15.0","digest":"sha256:…"},
                           {"id":"gorun-0.15.0-linux-amd64.tar.gz","digest":"sha256:…"}],
              "migrations":["content/0001_content.sql","…"],
              "themes":["default","business","portfolio","magazine","docs"]}]}
```

`migrations` (every migration the release knows) and `themes` (slugs it ships or supports)
are optional compatibility data. When present, the compatibility phase refuses a target that does
not know a migration already applied to this data, or that does not support the active theme.
When absent the phase records `release_data: unknown` instead of guessing.

“Latest” is a background observation of this index (System Health → Background
observations, `GET /api/v1/system/update` → `latest`) with its observed time and
age. It is reported `unknown` when no source is configured, the running build is
not a release tag (`dev`, `git describe` output), or the observation is missing.

### Phases and evidence

| Phase | Passes when | Fails safely when |
| --- | --- | --- |
| Compatibility | Running build is a release tag; target is newer (or downgrade allowed); schema is not ahead of the binary; modules and active theme recorded. | Development build, same version, unreviewed downgrade, schema ahead. Nothing was touched. |
| Release verification | Trusted source resolves an immutable artifact id and `sha256:` digest; optional `expected_digest` matches. | Unknown version, missing digest, digest mismatch. No backup, no deployment. |
| Backup | Existing backup machinery writes and inspects a manifest (stamp recorded). | Disk or verification failure blocks activation. |
| Activation | Adapter accepted the request (`deployment_ref` recorded once, before polling) **and** the serving process reports the target identity. | Provider reports failure, timeout after acceptance, or “active” without the identity changing. |
| Health and links | New process reports the expected identity, `readyz`, no failing protected check, every configured link 2xx; migration state recorded. | Any failure → rollback decision. |
| Final acceptance | All of the above; `accepted_version` is written and the lease released. | — |

A queued or accepted deployment is **never** success. The old process stops
when the provider restarts it; on boot the new process reconciles: an activation
whose identity now serves passes on that evidence and the lifecycle continues
(health → acceptance) in the new process. Any other phase interrupted by a
restart is marked `interrupted` with a remediation; a new request is accepted
afterwards and first checks whether the provider still reports the earlier
deployment in progress (`409`).

**Manual adapter.** When no Coolify configuration exists, the activation phase
records `manual:<version>` and waits (up to the activation timeout) for you to
stop the process and start the verified artifact on the same data directory.
The new process confirms activation on boot. Follow §2/§3 for the swap itself.

### Conflicts and ownership

One durable lease per data directory covers updates, rollbacks, scheduled and
manual backups and restores. A conflicting action is refused with the owning
operation (`409 operation_in_progress`, or the “busy” notice on the Backups
page). The runner renews the lease on a heartbeat; a runner that loses it stops
at once. The `gorun.lock` process lock is unchanged.

### Rollback semantics

If health fails after activation and the source can resolve the prior release,
the lifecycle re-activates that **prior artifact on the current data** — only
when activation applied no migration (`migration_state: unchanged`). If
migrations were applied, compatibility is uncertain: the operation ends
`rollback_failed`, the current data is kept, and you repair forward or recover
into a separate directory (§6 B). A database or media snapshot is never restored
automatically, and the backup taken in the Backup phase is retained for your
explicit recovery decision.

### Limits in this release

- The Coolify adapter only updates Docker image resources (pinned tag). Git-built
  resources are refused rather than deployed as a "version". Rollback re-pins the
  prior image resolved from the index.
- Compatibility uses the release index's `migrations`/`themes` when published;
  without them it can only refuse a database already ahead of the running binary
  and records `release_data: unknown`.
- No in-app downgrade of security-sensitive migrations; see §6 A.

## 8. Path cheat sheet

| Path | Artifact | Data | Lock | Docs |
| --- | --- | --- | --- | --- |
| Release binary | `gorun-X.Y.Z-<os>-<arch>.tar.gz` + `SHA256SUMS` | `GORUN_DATA` host path | stop service | §2 |
| GHCR / Docker | `ghcr.io/zadstack/gorun:X.Y.Z` | named volume `/data` | `docker stop` then run | §3.A |
| Coolify | image pin or rebuild | `gorun-data` → `/data` | stop-then-start redeploy | §3.B · [coolify.md](../deploy/coolify.md) |
| Compose | `deploy/docker-compose.yml` | `gorun-data` | no overlapping replicas | [compose](../deploy/docker-compose.yml) |

Related: [STORAGE.md](STORAGE.md) (backup schedule / retain-N),
[PRODUCTION.md](PRODUCTION.md) (HTTPS, restore cutover),
[STAGING.md](STAGING.md) (staging site + promote-to-live),
[TROUBLESHOOTING.md](TROUBLESHOOTING.md#upgrade-from-a-release-tag).

---

## العربية — ملخص سريع

لا توجد دورة تحديث تلقائية مكتملة للنواة. إجراء النشر الاختياري في الأدمن ينشئ
نسخة احتياطية موثّقة ويرسل طلب نشر إلى Coolify؛ قبول الطلب لا يعني نجاح التحديث.
الترقية = نسخة احتياطية ← إيقاف العملية (قفل `gorun.lock`) ← ثنائي أو صورة الوسم
الجديد على **نفس** مجلد البيانات والمفتاح ← migrations عند `serve` ← فحص الصحة
ودخول الأدمن. التراجع عن التشغيل يستخدم إصدارًا سابقًا متوافقًا مع **البيانات
الحالية** بعد فحص التوافق. استعادة نسخة قديمة إجراء تعافٍ منفصل قد يفقد البيانات
الأحدث؛ احتفظ بالبيانات الحالية، واستعد إلى مجلد منفصل، ولا تنفّذ التحويل قبل
تسوية الكتابات الجديدة أو قبول فقدانها صراحةً. التفاصيل أعلاه؛ Coolify في
[deploy/coolify.md](../deploy/coolify.md).
