# Services

A site connects six services: `mail`, `storage`, `chat`, `analytics`, `backups` and `metricool`. Each one has the same shape on every surface (`gorun service`, the MCP `services` toolset, the REST API under `/api/v1/site/services` and `gorun info`): a list of fields, at most one secret, and a status.

A status never contains a secret, only `secret_set` (true or false). Every change is audited as `service.update` with the field names only and `via` (`cli`, `mcp`, `api` or `admin`).

## Status

Every status has the same JSON keys:

| Key | Meaning |
|---|---|
| `name` | the service |
| `provider` | the provider in use (`smtp`, `r2`, `gorun`, …) |
| `state` | `off`, `configured` or `incomplete` |
| `missing` | what keeps it `incomplete` (field or secret names) |
| `fields` | the stored values, without the secret |
| `secret_set` | whether a secret is stored |
| `source` | where the setting comes from: `settings`, `config` (environment) or `local` |
| `last_test` | the last test result (`ok`, `at`, `message`, and `step` when it failed), or `null` |

`last_test` is the last `test` that ran, from any surface. A service with nothing to test (`no test`) records none, and a `set` or `off` of the service clears it: a test of the old values says nothing of the new ones.

## The services

### mail

Settings → Mail, validated like the admin form. Each provider keeps its own secret: switching from `smtp` to `resend` without a new key is `incomplete` (`missing: ["api_key"]`); the SMTP password is never reused.

| Field | Type | Notes |
|---|---|---|
| `provider` | enum | `smtp` or `resend` |
| `host`, `port`, `tls`, `username` | string, int, enum, string | SMTP only; `tls` is `starttls`, `tls` or `none` |
| `from` | string | required for Resend |
| `from_name`, `reply_to` | string | |

Secret: `password` (SMTP) or `api_key` (Resend). The SMTP password is needed only when a `username` is set: a relay with a `host` and no login is `configured` without one, and a remote set of it answers no `needs_secret`. Test: `gorun service test mail --to ADDR` sends the admin's test message.

### storage

The media and backup buckets, over Integrations → Storage (Connections profiles and managed destinations). While no destination is bound, `GORUN_S3_*` is the media source `config`; a bound media destination wins over it, and the status then says `overridden_config: true`.

| Field | Type | Notes |
|---|---|---|
| `provider` | enum | `r2` or `s3` |
| `account` | string | R2: the Cloudflare account ID |
| `endpoint` | string | S3: the endpoint URL |
| `region`, `bucket` | string | `bucket` is required |
| `purpose` | enum | `media`, `backups` or `both` (`backup` is taken as `backups`) |

Secret: `keys`, one line `ACCESS_KEY:SECRET_KEY`. `public_url` is refused: managed buckets serve media through the site's `/media/`.

Status fields add:

| Field | Type | Meaning |
|---|---|---|
| `media_source` | string | where new media goes: `settings` (a bound destination), `config` (`GORUN_S3_*`) or `local` |
| `backups_source` | string | `settings` when a backup destination is bound, else `none` |
| `destinations` | object | present while a destination is bound: one key per bound purpose, `media` and/or `backups`, each the destination's id as a **string** |
| `overridden_config` | bool | present (`true`) when a bound media destination wins over `GORUN_S3_*`; `config_endpoint` and `config_bucket` then name the bucket it replaces |
| `backups_endpoint`, `backups_bucket` | string | present when the backup bucket is not the media one |

A bucket bound for both purposes:

```json
"fields": {
  "provider": "r2",
  "account": "0123456789abcdef0123456789abcdef",
  "endpoint": "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com",
  "region": "auto",
  "bucket": "site-media",
  "purpose": "both",
  "media_source": "settings",
  "backups_source": "settings",
  "destinations": { "media": "a75216835e502bde187185e5e9f69e40", "backups": "49a0f0c967390552b7e9b6b89ddb88ee" }
}
```

Each purpose has its own destination record, so the two ids differ even when they name one bucket. A purpose that is not bound has no key (`{"media": "…"}` alone for a media-only bucket), and `destinations` itself is absent while nothing is bound.

The keys are those of the service (`backups`, plural) on every surface. The admin's storage form posts `purposes=media,backup` and the settings are `storage.active.media` and `storage.active.backup`; the id in `destinations.backups` is the one in `storage.active.backup`, and the one the backups service takes as its `destination`. `gorun service set storage purpose=backup` is the same as `purpose=backups`.

Test: a real round trip on the bucket of each bound purpose (media first, then backups; `GORUN_S3_*` for media while no media destination is bound). It checks the bucket (a signed `HEAD`), puts a small object under the reserved prefix `0000/00/gorun-service-test-`, reads it back and deletes it. The message names each purpose's outcome (`media: ok; backups: cannot write (HTTP 403)`) and `step` the first that failed:

| `step` | Meaning |
|---|---|
| `auth` | the bucket check was refused (HTTP 401 or 403): wrong or revoked keys |
| `bucket_missing` | the bucket check answered 404 |
| `unreachable` | the endpoint did not answer, or answered something else |
| `write`, `read`, `delete` | that step of the round trip failed (`read` also when the object read back differs) |
| `keys` | the stored keys cannot be opened with this site's key |

With nothing bound, the test cannot run and answers `services.error.not_configured`.

### chat

The Chatwoot widget on public pages.

| Field | Type | Notes |
|---|---|---|
| `provider` | enum | `chatwoot` |
| `base_url` | string | `https://` origin only, no path or query |
| `website_token` | string | not a secret: it is public in the page |

No secret.

### analytics

GoRun's own page analytics.

| Field | Type | Notes |
|---|---|---|
| `enabled` | bool | |
| `exclude` | string | comma-separated path prefixes, each starting with `/` |

No secret. `off` sets `enabled` to false.

### backups

The local backup schedule, and optionally an upload to the active backup destination.

| Field | Type | Notes |
|---|---|---|
| `enabled` | bool | `false` stops the schedule |
| `every_hours` | int | 1–168 |
| `retain` | int | 0–100 (0 keeps every pair) |
| `destination` | string | `local`, or the id of the active backup destination |

No secret. Status fields add `last_success`. No test (`no test`); `run` does a real backup instead.

#### Run a backup now

`gorun service run backups` (MCP `run_service`, `POST /api/v1/site/services/backups/run`, permission `system.backup`) takes a verified backup now, as a scheduled run does but whether or not one is due: it writes the pair under `<data>/backups/`, records `last_success`, uploads the pair to `destination` when it is not `local` (encrypted, read back and checked, recorded as the remote last backup), and keeps the newest `retain` pairs. The schedule's own clock does not move. It answers:

```json
{
  "ok": true,
  "at": "2026-11-28T09:00:00Z",
  "message": "backup 20261128T090000Z saved locally and uploaded to <destination id>",
  "stamp": "20261128T090000Z",
  "bytes": 1843200,
  "duration_ms": 2140,
  "destination": "<destination id>",
  "remote": "success",
  "objects": ["backups/<site>/2026/11/28/20261128T090000Z.db.gorb", "backups/<site>/2026/11/28/20261128T090000Z.tar.gz.gorb"],
  "pruned": 0
}
```

`remote` is the upload's status (`success`, `failed`, `partial`, `paused` when `destination` is no longer the active backup destination, or `uploaded_unrecorded`) and is absent for `local`. A failed upload is `ok: false` with the local pair kept, and the CLI exits non-zero; `warning` names a stage that did not stop the backup (`analytics`, `remote`, `prune`). While another backup, a restore or an update holds the data directory, the run is refused (REST 409 `operation_in_progress`). Audited as `service.run` with `via`, beside the backup's own `system.backup` records.

### metricool

Settings → Metricool: the drafts integration and its web tracker, over the `metricool.config` setting the admin form writes.

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | the Metricool user id, 1–10 digits |
| `blog_id` | string | the brand id (`blogId`), 1–10 digits |
| `tracker_hash` | string | optional: the hash of Metricool's web tracker, 8–64 letters and digits; not a secret, it is in every page |

Secret: `token`, the Metricool API token, stored encrypted with the site key; only from `--secret-stdin` or the one-time link. Status fields add `drafts_ready`: the token passed its test on the current settings, so the admin's draft composer can send. Changing `user_id`, `blog_id` or the token clears the test, as the admin form does; changing only `tracker_hash` keeps it. Every change gives the setting a new version, so an admin form opened before it is refused instead of overwriting it.

```sh
printf '%s\n' "$METRICOOL_TOKEN" | gorun service set metricool user_id=12345 blog_id=67890 --secret-stdin
gorun service set metricool blog_id=67891     # fix the brand; the token stays
gorun service test metricool
```

Test: the admin's read-only brand test: the token opens the user's brand list and `blog_id` is one of its brands, not deleted. `step` is `credential` (Metricool refused the token, or it cannot be decrypted) or `brand`. The result is stored in `metricool.config`, where the admin shows it too; `last_test` reads it from there.

#### Web tracker

With `tracker_hash` set, public pages the theme renders carry Metricool's tracker before `</body>`: a nonced inline script that loads `https://tracker.metricool.com/resources/be.js` and calls `beTracker.t({hash: …})`. Those responses, and only those, add `https://tracker.metricool.com` to `script-src` and `connect-src`; images already allow `https:`. Admin, sign-in, API and analytics paths never get it, and theme templates stay untouched. The tracker is printed by the chat module, beside the chat widget: turning the chat addon off removes both. `tracker_hash` is also a field of Settings → Metricool in the admin.

## From the shell

```sh
gorun service list                  # {"services":[…]}
gorun service get mail              # the status of one service
gorun service set chat provider=chatwoot base_url=https://chat.example.com website_token=abc123
gorun service test mail --to me@example.com
gorun service test storage          # put, read back and delete on each bound bucket
gorun service run backups           # a backup now, uploaded to the configured destination
gorun service off chat
```

Every subcommand takes `--data dir`. It boots only the settings store, with no job workers, so it works while `gorun serve` is stopped.

`set` changes only the fields given; the others and the stored secret stay as they were. The secret comes only from the first line of stdin with `--secret-stdin`, never from an argument; an empty line is refused:

```sh
printf '%s\n' "$SECRET" | gorun service set mail provider=smtp host=smtp.mx.cloudflare.net port=465 tls=tls \
  username=api_token from=noreply@example.com --secret-stdin

printf '%s\n' "$ACCESS_KEY:$SECRET_KEY" | gorun service set storage provider=r2 account=$CF_ACCOUNT \
  bucket=site-media purpose=media --secret-stdin
```

`set` prints `service <name>: <state>` (with the missing items when `incomplete`). A wrong value exits non-zero with `service: invalid <field names>`, and nothing is saved. A secret given as `key=value` (`password=…`, `api_key=…`, `keys=…`) is refused the same way. `test` and `run` print the result as JSON and exit non-zero when it did not pass. Only `backups` has a `run`.

`gorun mail set --provider … [--secret-stdin | --clear-secret]` stays as an alias of `gorun service set mail` ([docs/MAIL-OPERATIONS.md](MAIL-OPERATIONS.md#configure-mail-from-the-shell)).

## From an agent or a script

The MCP toolset `services` and the REST operations call the same registry as the shell, with the same validation and the same statuses. Neither ever takes a secret: a key that names one (`password`, `api_key`, `keys`, `token`) is refused as `validation_failed` with `services.error.secret_value`, any other key that is not one of the service's fields above (`secret_key`, `resend_api_key`, …) with `services.error.unknown_field`, and nothing is saved. On MCP both are refused before the call is parked, so no such value is stored with an approval or shown in its preview.

| MCP tool | REST | Scope | Notes |
|---|---|---|---|
| `list_services` | `GET /api/v1/site/services` | `settings.manage` | every status |
| `get_service` | `GET /api/v1/site/services/{name}` | `settings.manage` | one status |
| `set_service` | `PATCH /api/v1/site/services/{name}` | `settings.manage` | `{"values":{…}}` (MCP adds `"name"`); only the fields given change. On MCP it needs human approval (sensitive) |
| — | `DELETE /api/v1/site/services/{name}` | `settings.manage` | `off`; answers the new status |
| `test_service` | `POST /api/v1/site/services/{name}/test` | `system.capability_test` | mail sends one test message to the caller's own address; recorded as `last_test` |
| `run_service` | `POST /api/v1/site/services/{name}/run` | `system.backup` | `backups` only: [a backup now](#run-a-backup-now) |

Values may be strings, numbers or booleans (`"port": 465` is `port=465`). A set answers the new status, and when the service is `incomplete` because its secret is missing, `needs_secret`:

```json
{
  "status": {"name": "mail", "state": "incomplete", "missing": ["password"], "secret_set": false, "…": "…"},
  "needs_secret": {
    "link": "https://example.com/admin/services/mail/secret?t=…",
    "expires_at": "2026-11-26T12:15:00Z",
    "cli": "printf '%s\\n' \"$SECRET\" | gorun service set mail --secret-stdin"
  }
}
```

The link is the [one-time secret page](MCP.md#one-time-secret-link): a person signed in with `settings.manage` opens it within 15 minutes and types the secret once. It is made for the calling user (audited as `link_actor` when used), is bound to the provider it was made for (after a provider change it is gone and saves nothing) and is absolute when the site's base URL is known.

When the values can only be saved together with the secret (storage on a site with no bucket bound yet, or on a new endpoint), nothing is saved by the set: the values are checked as they would be with a secret, held with the link (never the secret, and only the token's hash), and applied in the same transaction as the secret when the link is used. The answer then has `"pending": true` and a `cli` line that carries the values:

```json
{
  "status": {"name": "storage", "state": "off", "…": "…"},
  "needs_secret": {
    "link": "https://example.com/admin/services/storage/secret?t=…",
    "expires_at": "2026-11-26T12:15:00Z",
    "cli": "printf '%s\\n' \"$SECRET\" | gorun service set storage 'account=…' 'bucket=site-media' 'provider=r2' 'purpose=media' --secret-stdin",
    "pending": true
  }
}
```

An approved MCP call stores its result (read back with `check_approval`) with the link redacted (`?t=…redacted`) and a `note`: only the live answer of a call that runs at once carries the token. Call `set_service` again for a fresh link, or run the CLI line on the site. Changes are audited as `service.update` with `via: mcp` or `via: api`. An unknown service name is `not_found` (HTTP 404).

## In `gorun info`

`gorun info` adds `services`, the same list as `gorun service list`. Its `mail` block stays as it was.
