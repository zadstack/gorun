# Staging and promote-to-live

GoRun is **one process per data directory**. There is no shared SQLite, no
in-app environment switcher, and no live merge of staging into production.
Staging and promote-to-live are a **productized operator path** on top of
backup + confined restore.

Related: [PRODUCTION.md](PRODUCTION.md) (backup/restore cutover),
[UPGRADE.md](UPGRADE.md) (dress rehearsal), [STORAGE.md](STORAGE.md),
[TROUBLESHOOTING.md](TROUBLESHOOTING.md), [deploy/coolify.md](../deploy/coolify.md).

## Model

| Role | What it is |
| --- | --- |
| **Live** | `gorun serve --data /var/lib/gorun` (holds `<data>/gorun.lock`) |
| **Staging** | A **second** process + **second** data dir + different `--addr` / `GORUN_BASE_URL` |
| **Promote** | Backup staging → restore into a **new** live destination → stop live → point `--data` / volume at the new dir |

`GORUN_ENV` remains `dev` or `prod` only. Staging is a second site layout, not a
third enum value.

```text
live data ──backup──► pair ──restore──► staging data ──serve staging──► edit
                                              │
                                         backup
                                              │
                                              ▼
                                    restore → new live dir
                                              │
                         stop live → serve --data new live dir
```

## Seed a staging site

From a live (or any) data directory:

```sh
gorun staging seed --from /var/lib/gorun --dest /var/lib/gorun-staging
```

Or from an existing backup pair:

```sh
gorun backup --data /var/lib/gorun
gorun staging seed --input /var/lib/gorun/backups/<stamp>.manifest.json \
  --dest /var/lib/gorun-staging
```

Then:

1. Copy `secret.key` and `gorun.toml` into the restored directory, or set the
   original `GORUN_SECRET_KEY` (default backups omit secrets; use
   `--include-secrets` on `--from` only when you will keep that archive offline).
2. Serve staging alone, e.g.:

```sh
GORUN_BASE_URL=https://staging.example.com \
  gorun serve --data /var/lib/gorun-staging --addr 127.0.0.1:8081 --env prod
```

3. Keep production on its original `--data`. Two processes must not share one
   data directory — the second fails with `data directory is in use`.

Admin path: **Operations → Backups → Inspect / restore** writes under
`<data>/restores/<stamp>/` while live keeps running. Move that folder **out** of
the live tree before serving it as staging (same rule as production restore).

## Promote staging to live

After edits on staging look right:

```sh
gorun staging promote --from /var/lib/gorun-staging --dest /var/lib/gorun-new
```

Or promote a specific pair:

```sh
gorun staging promote --input /var/lib/gorun-staging/backups/<stamp>.manifest.json \
  --dest /var/lib/gorun-new
```

Then:

1. Copy secrets into `/var/lib/gorun-new` (unless the backup included them).
2. Re-point external credentials (S3, Stripe, mail). Object storage and env
   secrets are **not** in the archive.
3. **Stop** every process on the current live data directory (releases `gorun.lock`).
4. Point the live service at the new directory (`--data /var/lib/gorun-new`) or
   swap the Coolify/Docker volume mount to that path.
5. Smoke-test `/healthz`, `/readyz`, admin login, and a published page.
6. Keep the previous live directory until smoke checks pass (rollback = stop +
   serve the old `--data` with the same secret).

`gorun staging promote` never overwrites the current live path: `--dest` must
not already exist. That is intentional for single-process SQLite.

Equivalent low-level commands remain available:

```sh
gorun backup --data /var/lib/gorun-staging
gorun backup-inspect --input …/backups/<stamp>.manifest.json
gorun backup-restore --input …/backups/<stamp>.manifest.json --dest /var/lib/gorun-new
```

## Coolify / Docker

- Prefer a **second resource** (or second compose service) with its own volume
  for staging, different public URL, and the same image tag you plan to promote.
- For cutover, restore into a new host path or volume, stop the live container,
  then remount `/data` at the restored tree with the **original**
  `GORUN_SECRET_KEY`. See [coolify.md §7](../deploy/coolify.md#7-backup-restore-post-restore-ux).
- Do not run overlapping replicas on one volume.

## Honest limits

- No content-only / selective promote in this release.
- No preview-URL product surface (use a separate staging host or private port).
- No multisite / shared database.
- Theme override “publish” and content publish are **not** environment promote —
  those stay draft → live **within** one data dir.
- Restore “staging” directories (`.partial`) are atomic-write scratch, not a staging env.

## Naming

| Say | Mean |
| --- | --- |
| Staging site / staging data dir | Second GoRun process + data dir |
| Promote-to-live / cutover | Restore to new live dir → stop → point `--data` |
| Avoid bare “Promote” in admin | Collides with theme/content publish language |
