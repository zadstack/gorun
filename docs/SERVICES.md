# Services

A site connects five services: `mail`, `storage`, `chat`, `analytics` and `backups`. Each one has the same shape on every surface (`gorun service`, and in 0.28 the MCP `services` toolset and the REST API): a list of fields, at most one secret, and a status.

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
| `last_test` | the last test result, or `null` |

## The services

### mail

Settings → Mail, validated like the admin form. Each provider keeps its own secret: switching from `smtp` to `resend` without a new key is `incomplete` (`missing: ["api_key"]`); the SMTP password is never reused.

| Field | Type | Notes |
|---|---|---|
| `provider` | enum | `smtp` or `resend` |
| `host`, `port`, `tls`, `username` | string, int, enum, string | SMTP only; `tls` is `starttls`, `tls` or `none` |
| `from` | string | required for Resend |
| `from_name`, `reply_to` | string | |

Secret: `password` (SMTP) or `api_key` (Resend). Test: `gorun service test mail --to ADDR` sends the admin's test message.

### storage

The media and backup buckets, over Integrations → Storage (Connections profiles and managed destinations). While no destination is bound, `GORUN_S3_*` is the media source `config`; a bound media destination wins over it, and the status then says `overridden_config: true`.

| Field | Type | Notes |
|---|---|---|
| `provider` | enum | `r2` or `s3` |
| `account` | string | R2: the Cloudflare account ID |
| `endpoint` | string | S3: the endpoint URL |
| `region`, `bucket` | string | `bucket` is required |
| `purpose` | enum | `media`, `backups` or `both` |

Secret: `keys`, one line `ACCESS_KEY:SECRET_KEY`. Status fields add `media_source`, `backups_source`, `destinations` and, when it applies, `overridden_config`. `public_url` is refused: managed buckets serve media through the site's `/media/`.

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

No secret. Status fields add `last_success`.

## From the shell

```sh
gorun service list                  # {"services":[…]}
gorun service get mail              # the status of one service
gorun service set chat provider=chatwoot base_url=https://chat.example.com website_token=abc123
gorun service test mail --to me@example.com
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

`set` prints `service <name>: <state>` (with the missing items when `incomplete`). A wrong value exits non-zero with `service: invalid <field names>`, and nothing is saved. A secret given as `key=value` (`password=…`, `api_key=…`, `keys=…`) is refused the same way. `test` prints the result as JSON and exits non-zero when it did not pass (storage has no synchronous test and says `no test`).

`gorun mail set --provider … [--secret-stdin | --clear-secret]` stays as an alias of `gorun service set mail` ([docs/MAIL-OPERATIONS.md](MAIL-OPERATIONS.md#configure-mail-from-the-shell)).
