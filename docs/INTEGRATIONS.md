# Integrations hub: Cloudflare, storage and backup buckets, mail

**Settings → Integrations** (`/admin/settings/integrations`, permission `settings.manage`) makes the
services a site depends on discoverable and configurable in one place, on top of the existing
building blocks: encrypted settings, connection profiles, managed storage destinations, the mailer,
capability evidence and durable background observations. Nothing on the page runs an external
call during a page load or an API `GET`: every status is a saved snapshot with its age, refreshed
by bounded background jobs or by an explicit action.

> **ملخص بالعربية.** صفحة التكاملات تجمع اتصال Cloudflare (رمز API مشفّر لا يُعرض مرة أخرى، اختيار
> الحساب، مصابيح الصلاحيات)، وجرد R2/D1/Workers كملاحظات خلفية مع العمر وحالة «غير مكتمل» الصريحة،
> وربط حاوية موجودة كوجهة وسائط و/أو نسخ احتياطي ببادئتين منفصلتين، وقسم SMTP/Resend مع اختبار بريد
> صريح يفرّق بين قبول المزود ووصول الرسالة. كل شيء محقَّق ببدائل وهمية فقط؛ ما زال الاختبار على حساب
> Cloudflare حقيقي مصرّح به مطلوبًا قبل الاعتماد.

## Cloudflare connection

| Setting | Stored as |
| --- | --- |
| `integrations.cloudflare.token` | AES-GCM ciphertext under the site secret key (AAD `integrations.cloudflare.token`); never returned by any page, API, log or audit entry. Only `token_hint` (last four characters) is shown. |
| `integrations.cloudflare.account_id` / `account_name` | The selected account (32-hex id) and the label from the last accounts read. |

A blank token on save keeps the stored one; **Remove token** clears the token and the account.
Saves are compare-and-swap on the stored token and account (`409 conflict` when another save
won), and invalid input writes nothing. Saving queues the background reads below; it does not
contact Cloudflare inline.

The token is a **management** credential used for read-only listing only. It is not an S3
credential (R2 buckets are bound with their own S3 access keys, created for the bucket in the R2
dashboard) and it is not a GoRun Edge manager key. GoRun never grants or widens permissions and
never creates, modifies or deletes Cloudflare resources.

### Permission checks (scope lamps)

`integrations.cloudflare.scopes` (key `cloudflare`, revision = hash of the token ciphertext and the
account id, refreshed every 12 hours and on **Run permission checks**) runs one bounded read per
scope and records a lamp for each:

| Scope | Read | Minimum token permission |
| --- | --- | --- |
| `token` | `GET /user/tokens/verify` | a valid, active API token |
| `accounts` | `GET /accounts?per_page=1` | Account Settings: Read |
| `r2` | `GET /accounts/{id}/r2/buckets?per_page=1` | Workers R2 Storage: Read |
| `d1` | `GET /accounts/{id}/d1/database?per_page=1` | D1: Read |
| `workers` | `GET /accounts/{id}/workers/scripts?per_page=1` | Workers Scripts: Read |

Lamp status is `passed` (the read succeeded), `incomplete` (HTTP 401/403 or a Cloudflare
authorization error: the token lacks the permission), `could_not_verify` (provider or network
failure, timeout) or `not_checked` (no account selected, or saved for an earlier configuration).
Each lamp carries the check time, sanitized evidence or error (provider messages are truncated and
scrubbed of the token) and a repair step. A verified token is **not** treated as per-service
permission, and GoRun does not claim to detect permissions beyond the ones it checks: the API
cannot report them, so the page recommends a dedicated token limited to one account with only the
read permissions above.

### Account selection

`integrations.cloudflare.accounts` (revision = token ciphertext) lists the accounts the token sees
(paginated, with an explicit `complete` flag). When the token sees several accounts the page offers
a select; an account missing from an incomplete list can be entered by id. Changing the account
changes the revision of every account-bound observation, so earlier results are reported `stale`,
never as the new account's.

## Discovery

`integrations.cloudflare.inventory` keeps one observation per kind (`r2`, `d1`, `workers`) for the
selected account, refreshed hourly (one in flight per kind) and on **Refresh**. Listing follows
page-numbered and cursor-based pagination in full, up to 20 pages of 100, deduplicating by id and
stopping when a page adds nothing new (an endpoint that ignores paging is read once more, not
twenty times); beyond the cap, or when the provider's `total_count` exceeds what was read, the
inventory is saved with `complete: false` and shown as **Inventory incomplete** instead of guessed. Only list metadata is kept: bucket name,
location and class; database id, name, table count and size; Worker name and dates. No D1 rows,
bucket objects or Worker code are read, and nothing is created or modified.

The tables are searchable and filterable (all / bound / candidates / confirmed GoRun Edge). A
resource is **bound** when it is the active destination of a purpose (R2: the endpoint
`https://<account>.r2.cloudflarestorage.com` and bucket match `storage.active.media` or
`storage.active.backup`) or, for Workers, when the identified origin or manager id matches the
Edge settings. Everything else is a candidate offered for explicit binding.

### Confirming a Worker as GoRun Edge

A name never makes a Worker a GoRun Edge instance. **Identify as GoRun Edge** takes the Worker's
public https origin and a GoRun Edge manager key pasted for the check; GoRun calls
`GET <origin>/api/v1/manager` with that key through the same public-host-only client as
Settings → Edge (`edgeWhoami`), and saves the answer (site id, manager id, label) as
`integrations.cloudflare.edge_identity` evidence keyed by the Worker name and fenced on the account
revision. Only a Worker present in the saved inventory can be identified. The key is not stored.

What the contract proves is that **the origin** is a GoRun Edge instance; it cannot prove which
Cloudflare script serves that origin. The evidence therefore carries an `association`:
`workers_dev_host` when the origin is `<worker-name>.<subdomain>.workers.dev`, otherwise
`operator_asserted` (a custom domain the operator tied to this Worker). The kind is manual-only:
`POST /system/observations/integrations.cloudflare.edge_identity/{name}/refresh` answers
`409 manual_only`, so no refresh can overwrite a saved identity with a failed attempt; the evidence
expires after 7 days like other capability evidence. A failed explicit check records a failed
attempt and keeps the earlier identity visible as history. Discovery grants no application access:
the Cloudflare token and the Edge manager key stay separate.

## Storage and backup buckets

The storage section shows, per purpose, the resolved destination and its **source**:
`settings` (a managed destination activated under Settings → Storage or here), `config` (the `[s3]`
section or `GORUN_S3_*` environment for media), `local` (the data directory) or `none`. Endpoint,
region and bucket are shown; credentials never are. Backup schedule values are shown read-only and
edited under System → Backups.

**Bind a bucket** reuses the existing pieces rather than adding settings: it saves (or updates in
place, when the same endpoint/region/bucket already exists) an S3 connection profile
(`connections.profile.*`, keys encrypted) and activates a managed destination
(`storage.destination.*`, `storage.active.<purpose>`) per selected purpose. The provider is R2
(endpoint derived from the selected account) or any public https S3 endpoint; private hosts are
refused by the same guard as connection profiles. The operator chooses **media**, **backups** or
both; both needs an explicit confirmation. Prefixes are fixed by the media and backup code and never
overlap even in one bucket:

| Purpose | Prefix |
| --- | --- |
| media | `sites/<site-id>/uploads/` (generated media under `sites/<site-id>/content/`) |
| backups | `backups/<site-id>/` (encrypted `.gorb` objects) |

The profile and the destinations commit in one transaction; a re-bind of the same location with
the same keys reuses the profile unchanged (name, version and saved check evidence kept), while new
keys rotate the credentials and clear the evidence. Binding queues the **read-only check** (signed `HEAD` on the bucket, kind `connection`). The
**write/read/delete probe** is a separate explicit action behind `system.capability_test` and only
queues the existing operation tests: `operation.storage` for the active media storage,
`operation.s3` on the bound profile for backups. It writes one uniquely named probe object, reads it
back and deletes it. Nothing here runs a backup, reads or deletes real objects, or changes
retention, ACL or lifecycle; backup data stays encrypted and private regardless of bucket ACLs.

## Mail

The mail section shows the resolved SMTP or Resend values from settings (host, port, TLS, from,
from name, reply-to, username, whether a password or API key is stored) and lets the operator edit
them with the same validation and version token as Settings → Mail. This change adds
`mail.reply_to`, a default `Reply-To` header used by the mailer when a message sets none; it is
also on Settings → Mail and in the settings API.

**Send test** (permission `system.capability_test`) sends the standard test template once to the
operator's own address or to a chosen address. The outcome reports **provider acceptance** (the
SMTP server or Resend accepted the message) separately from **inbox delivery**, which is never
verified. Unlike `POST /site/capabilities/services/mail/operation`, which only ever sends to the
caller's own address, this action can address a third party: the admin audit log keeps the full
recipient (pages and API answers show it masked), and each operator may send at most 5 test
messages per 10 minutes (`429 rate_limited`). The send is recorded as `operation.mail` evidence for
the requesting operator (`recipient: requesting_operator | chosen_address`).

Upgrade note: the mail evidence revision now also covers `mail.provider` and `mail.reply_to`, so
mail operation evidence saved before this change is reported `stale` until the next test send.

## API

All operations are under `/api/v1/site/integrations` and documented in the OpenAPI document with
their scopes and error codes (see the generated tables in [API.md](API.md#site)):

| Operation | Scope | Notes |
| --- | --- | --- |
| `GET /site/integrations` | `settings.manage` | The whole snapshot; `Cache-Control: no-store`; no external calls. |
| `PUT /site/integrations/cloudflare` | `settings.manage` | `{token?, account_id?, clear?}`; `422 validation_failed`, `409 conflict`. Returns the connection view, never the token. |
| `POST /site/integrations/cloudflare/check` | `settings.manage` | `202` with the task references; `409 cloudflare_not_configured`. |
| `GET /site/integrations/cloudflare/inventory/{kind}` | `settings.manage` | `404` for an unknown kind. |
| `POST /site/integrations/cloudflare/inventory/{kind}/refresh` | `settings.manage` | `202`; concurrent requests share one job (`deduplicated`); `409 cloudflare_not_configured` / `account_required`. |
| `POST /site/integrations/cloudflare/workers/{name}/identify` | `settings.manage` | `{origin, manager_key}`; synchronous, bounded; `422 edge_identity_failed`. |
| `POST /site/integrations/storage/bind` | `settings.manage` | `{source, endpoint?, region?, bucket, name?, access_key, secret_key, purposes[], confirm_both?, connection_id?}`. |
| `POST /site/integrations/mail/test` | `system.capability_test` | `{to?}`; synchronous; `409 capability_disabled` until mail is configured. |

Observation kinds added: `integrations.cloudflare.accounts`, `integrations.cloudflare.scopes`,
`integrations.cloudflare.inventory` (keys `r2`, `d1`, `workers`) and
`integrations.cloudflare.edge_identity` (key = Worker name, on request only). They are readable
through the generic observations API as well.

## What was verified, and what was not

Verified with fakes only (no real Cloudflare account, token, deployment, DNS change or mail):

- An `httptest` stand-in for the Cloudflare v4 API covering token verification, account listing,
  cursor-based R2 paging, page-numbered D1 paging, Workers listing, injected 401/403 and 5xx
  failures, and the page cap (`incomplete` instead of a guess).
- Token storage: encrypted, never echoed in pages, API responses, observation rows or audit
  metadata; invalid input and concurrent saves leave the previous configuration intact; blank token
  keeps the stored one; clear removes both token and account.
- Revision fencing: changing the account marks inventory and identity evidence `stale`.
- Deduplication: a second check or refresh while one is queued returns the same job.
- Worker identity through a fake manager origin: a non-GoRun origin is never promoted, private
  origins and malformed keys are refused, the key is not stored, and the identified Worker is shown
  bound once the Edge settings point at it.
- Storage binding: refusal without confirmation for both purposes, missing S3 keys (a Cloudflare
  token is not accepted), private endpoints; successful binding reuses connection profiles and
  destinations, shows resolved values and prefixes, and only queues checks and probes.
- Mail: a fake plaintext SMTP server (AUTH PLAIN, `tls: none` on loopback) for provider acceptance
  and rejection, `Reply-To` emission, masked audit, evidence for the operator, and the
  `system.capability_test` requirement for the test route.
- Permissions on every page route and API operation, OpenAPI coverage, AR/EN rendering.

Still required before relying on it: a live, authorised test against a real Cloudflare account
with a dedicated read-only token (lamp semantics against real error codes, real R2/D1/Workers
pagination sizes, an R2 bucket bound with real S3 keys and probed), a real GoRun Edge Worker
identity check, and a real SMTP or Resend provider. Known limits: per-purpose prefixes are fixed
(not configurable), the inventory cap is 20 pages per kind, Cloudflare API errors are reported by
status and code with a truncated message, and the D1 and Workers listings offer no binding
(D1 databases and Workers are inventory only; a Worker's only binding is the Edge origin).
