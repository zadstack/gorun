# GoRun REST API

This is the default GoRun registry. A build that adds its own modules (see [ARCHITECTURE.md](ARCHITECTURE.md#building-on-the-core--البناء-فوق-النواة)) documents their operations separately.

> **ملخص بالعربية.** واجهة REST في GoRun على `/api/v1`، وكل الردود بالغلاف نفسه
> `{"ok":true,"data":…}` أو `{"ok":false,"error":{…}}`. المصادقة بمفتاح API من نوع `api`
> (`Authorization: Bearer gp_…`) أو بجلسة لوحة التحكم مع الترويسة `X-CSRF-Token` للطلبات المعدِّلة.
> مفاتيح الوكلاء (`agent`) لا تعمل هنا وتُرفض بالرمز `403 agent_key_not_allowed`، فهي لـ MCP فقط.
> لكل عملية نطاقات صلاحية (`x-scopes`) يجب أن يملكها المفتاح ودور صاحبه معًا. وثيقة OpenAPI 3.1 على
> `/api/v1/openapi.json` والمرجع التفاعلي على `/api/docs`، وكلاهما عام. جدول العمليات أدناه مولَّد من الوثيقة
> الحية، والقسم الأخير يشرح لمطوري الوحدات كيف يسجلون عملياتهم.

Base URL: `<site>/api/v1`. The OpenAPI 3.1 document is generated at runtime from the operations that
modules register, and served at `GET /api/v1/openapi.json`. The interactive reference (Scalar) is at
`GET /api/docs`. These and `GET /api/v1/site/info` are public; other paths need a key or a session.

## Authentication

- **API key.** `Authorization: Bearer gp_…`, created in the admin under **AI Agents → API keys**
  (`/admin/agents/keys`, permission `api_keys.manage`). A key acts as its owner: it never exceeds the owner's
  role, and only what its scopes list (`*` = all of the owner's permissions). Keys have a per-key rate limit
  (`rate_per_min`, default 60); over it the API answers `429 rate_limited` with `Retry-After`.
- **Key kinds.** Only keys of kind `api` (**REST API** in the admin) work on `/api`. Keys of kind `agent`
  (**AI agent**, the admin default) work only on `/mcp` and get `403 agent_key_not_allowed` on every `/api`
  path, so an agent cannot, for example, approve its own sensitive MCP calls over REST.
- **Session.** The admin `gp_session` cookie also works. Unsafe methods (POST/PUT/PATCH/DELETE) then require
  the `X-CSRF-Token` header.
- **Scopes.** Each operation lists its required scopes in `x-scopes` (shown in the tables below). Session users
  pass when their role has the permission; API keys additionally need the scope. Failure is `401 unauthorized`
  (anonymous) or `403 forbidden`.
- **Key console.** Keys can also be managed over the API (`/v1/keys`: list, create, rotate with a grace period,
  revoke) and with `gorun key`. Creating a key over the API needs a signed-in session (an API key caller gets
  `403 session_required`); a key created or rotated through the API never carries scopes beyond the caller's,
  and only a key's owner can rotate it. See [WEBHOOKS.md](WEBHOOKS.md#key-console).
- **Webhooks.** Signed outgoing notifications of site events (`/v1/webhooks`, `gorun webhook`); payload,
  headers and signature verification are in [WEBHOOKS.md](WEBHOOKS.md).

## Envelope

```json
{"ok": true, "data": {}, "meta": {"total": 42, "page": 1, "per_page": 20}}
{"ok": false, "error": {"code": "validation_failed", "message": "validation failed", "fields": {"title": "core.validation.required"}}}
```

`meta` is present on list responses only; `fields` on validation errors only.

## Pagination and sorting

`?page=1&per_page=20&sort=created_at&order=desc` (or `sort=-created_at`). `per_page` defaults to 20 and is
capped at 100; invalid values fall back to the defaults, and unknown sort fields fall back to the operation's
default.

## Error codes

| Status | Code | Meaning |
|---|---|---|
| 400 | `invalid_json` | Body is not valid JSON, has unknown fields or wrong types (`fields` hints) |
| 400 | `empty_body` | A JSON body is required but missing or empty |
| 400 | `bad_request` | Other malformed input |
| 401 | `unauthorized` | No or invalid API key / session |
| 403 | `forbidden` | Missing scope or permission |
| 403 | `onboarding_required` | The auto-generated administrator must complete first-login setup at the local `/admin/onboarding` page before authenticated API access |
| 403 | `csrf_invalid` | Session request without a valid `X-CSRF-Token` |
| 403 | `agent_key_not_allowed` | An agent-kind key was used on `/api` (agent keys work only on `/mcp`) |
| 404 | `not_found` | Unknown path or resource |
| 405 | `method_not_allowed` | Path exists, method does not (see the `Allow` header) |
| 413 | `payload_too_large` | Body over the operation limit (JSON helper and builder: 1 MiB) |
| 422 | `validation_failed` | Input failed validation (`fields`) |
| 429 | `rate_limited` | Per-key rate limit exceeded |
| 500 | `internal_error` | Unexpected error (details only in server logs) |
| 503 | `unavailable` | Database unreachable (`/health`) |

Module operations add their own codes (found by grepping `api.WriteError` / `httpx.Error` call sites):

| Status | Code | Returned by |
|---|---|---|
| 400 | `invalid_name` | themes: invalid template name |
| 409 | `conflict` | builder, content types, taxonomies, users/settings (admin), migrations: the change collides with existing state (e.g. a taken slug) |
| 409 | `period_ended` | commerce: resuming an entitlement whose period has ended |
| 413 | `too_large` | media: file over the upload size limit |
| 415 | `unsupported_media_type` | media: file type not in the allowlist |
| 422 | `auth_failed` | migrations: WordPress rejected the application password |
| 422 | `auth_unavailable` | migrations: the authenticated profile is forbidden; no public fallback |
| 502 | `site_unreachable` | migrations: the WordPress site could not be reached |
| 502 | `billing_server_error` | commerce: the billing server answered with an error |
| 403 | `session_required` | keys: creating a key with an API key instead of a signed-in session |
| 403 | `scope_exceeds_caller` | keys: creating a key with scopes the caller does not hold |
| 403 | `not_key_owner` | keys: rotating a key owned by someone else, or beyond the caller's scopes |
| 409 | `key_inactive` | keys: rotating a revoked or expired key |
| 409 | `webhook_disabled` | webhooks: testing or redelivering to a disabled webhook |
| 500 | `internal` | some module handlers use this code instead of `internal_error` (same meaning) |

## Version and capability discovery

Use `GET /api/v1/openapi.json` for operation discovery. `info.version` describes
the API family. `x-gorun.core_version` carries the binary build version (including release suffixes);
`x-gorun.api_contract_version` versions these discovery/transport contracts. `dev` explicitly means an
unversioned build. Fleet clients should inspect mounted paths and `x-gorun.capabilities`, then `x-scopes`
and `/me` for the acting principal's permissions. Capability presence does not grant permission.

### Public site information and protected runtime

`GET /api/v1/site/info` is a read-only, anonymous projection of the running site. The existing
`GET /api/v1/site` structure endpoint keeps its `content.view` scope and response unchanged.
The new response uses `value`, `source`, `available` and, when unavailable, `reason` rather than
guessing missing values. It contains:

- `version`: the build string captured when this API instance starts (`process.build`). `dev` is
  explicitly an unversioned build. Update requests and feature acceptance do not change this value.
- `identity`: current title, tagline and canonical base URL. Sources identify specific settings,
  resolved runtime configuration, or the renderer's translated default title. The URL follows
  `site.base_url` → `config.BaseURL`, with whitespace/trailing slashes normalized.
- `logos.primary`: the public media URL referenced by `site.logo`; absent media/modules are explicit.
  Core has no separate light/dark logo settings, so it does not invent them or inspect arbitrary template content.
- `languages`: default `site.locale` (existing fallback `ar`), direction, and locales accepted by the
  running `i18n.Supported` registry. Supported locales do not promise every page has a translation.
- `appearance`: active theme identity, known color/font declarations and validated identity settings,
  with each source, last static-file observation time and limitations. An older downstream theme service
  without the optional `theme.AppearanceProvider` returns `null` plus `appearance_reason`.

Theme declarations are not browser-computed styles. The report resolves supported local root tokens
through the parent/child chain and respects the theme's explicit identity opt-in. Custom CSS and builder
identity can make effective values ambiguous, which is reported as unavailable. Page overrides,
conditional rules, external CSS and arbitrary stylesheet order are not evaluated. File scans are bounded
and cached for one theme per service; theme activation, cache invalidation or restart refreshes them.
Settings are read on each request. No external service is required or called by this endpoint.
Invalid theme identifiers are withheld with `invalid_theme_identifier`; the stored selection is
preserved and never copied into public values or provenance.

```sh
curl https://example.com/api/v1/site/info
curl -H "Authorization: Bearer $GORUN_API_KEY" https://example.com/api/v1/site/runtime
```

Example value fragments (the actual values come from the running site):

```json
{"version":{"value":"v0.13.0","source":"process.build","available":true},
 "identity":{"title":{"value":"My site","source":"settings:site.title","available":true}},
 "logos":{"primary":{"value":null,"source":"settings:site.logo","available":false,"reason":"not_configured"}}}
```

`GET /api/v1/site/runtime` requires **`settings.manage`** and returns the running build, API contract
version, Go runtime, OS/architecture and `api_started_at` (API registration time, not an OS process
creation timestamp). It never serializes runtime configuration, credentials or filesystem paths.
Both endpoints use the standard envelope; malformed/unreadable required settings fail with
`500 internal_error` instead of silently manufacturing defaults. Runtime access returns
`401 unauthorized` or `403 forbidden` as appropriate. Responses use `Cache-Control: no-store`;
internal theme snapshot caching is independent of HTTP caching.
Public URLs containing credentials, a query string, a fragment or an unsupported scheme are
reported as unavailable (`unsafe_public_url`), including legacy/configured base URLs and signed logo URLs.
Their saved settings/media records are left intact.

This is an additive API upgrade: no data migration or configuration rewrite is needed. Existing
themes, settings, content, and downstream implementations of `theme.Service` remain compatible.
The staged updater and durable background refresh work are tracked in
[CORE-UPGRADE-ROADMAP.md](CORE-UPGRADE-ROADMAP.md).

### Capability state and saved evidence

`GET /api/v1/site/capabilities` requires **`system.health`**. It reads the actual boot inventory and
saved managed AI/MCP/S3 connection profiles. It does not contact providers, enqueue work or change
configuration. `observed_at` is the report read time; `last_check.checked_at` is the saved probe time.

| Field | Meaning |
| --- | --- |
| `available` | The module was supplied to this boot, or Core recognizes the profile type. Configured add-ons missing from the binary are explicitly unavailable. |
| `enabled` | Actual boot selection for modules. `null` for a reusable connection profile: saving a profile does not activate its consumers. |
| `configured` | Local profile validation and readable required credentials. `null` when a module does not publish its requirements; never infer success from this value. |
| `connected` | `true` only for a current, unexpired, passed connectivity check; `false` after a failed or stale one; `null` when never checked. |
| `tested` | `true` only for a current, unexpired, passed **operation test** (see below). Connectivity never sets it. |
| `operation_supported` | Whether a safe operation test exists: S3 (write/read/delete one probe object), AI (one tiny completion), mail (one message to the requesting operator). MCP has none: invoking a tool would execute remote actions. |
| `last_check` / `operation` | `null` when never run; otherwise the saved result/time, `scope` (what ran), `expires_at`, applicability status, reason and remediation. No fabricated zero counts are emitted. |

Evidence is bound to the configuration revision and to time. Status is `passed` only while the
revision still matches and the result is younger than 7 days; an edit makes it `stale` and age makes
it `expired`, both keeping `result: passed` so the history is not erased. A newer failed attempt is
reported `failed` with its own time. `services` lists the core mail service with the same fields.

Running checks never happens on `GET`. Explicit requests queue background jobs and answer `202`
with a task reference (see Background observations):

| Endpoint | Scope | Runs |
| --- | --- | --- |
| `POST /api/v1/site/capabilities/connections/{id}/check` | `settings.manage` | Read-only connectivity/discovery (list models, discover tools, probe the bucket). Also scheduled every 12 hours per profile. |
| `POST /api/v1/site/capabilities/connections/{id}/operation` | `system.capability_test` | S3: write, read back and delete one `YYYY/MM/gorun-capability-probe-<id>.txt` object (always deleted, even after a failed read). AI: one chat completion of a fixed prompt capped at 16 output tokens. `409 operation_unsupported` for MCP. |
| `POST /api/v1/site/capabilities/services/mail/operation` | `system.capability_test` | One test message to the requesting operator's own address; `409 capability_disabled` until SMTP is configured. |
| `POST /api/v1/site/capabilities/services/storage/operation` | `system.capability_test` | Write, read back and delete one probe object through the storage new uploads actually use: the managed destination selected under Settings → Storage, else the `[s3]` bucket from config/env, else local disk. `services.storage` reports which one (`source`) and whether it is configured. |

MCP profiles have **no operation test** on purpose: invoking a tool executes an action on the
remote server, and a server's own read-only annotation is a claim, not a guarantee. The MCP
connectivity check performs `initialize` + `tools/list` only; `tested` stays `false` for MCP.
Modules that publish no requirements report `configuration_status: unknown` with
`module_publishes_no_requirements`; the media module's configuration comes from its active storage.

The admin "test" buttons (connections, mail) still run synchronously and record the same evidence.
Modules publish their configuration verdict through the `system.capabilities.modules` filter
(`admin.ModuleRequirement`: configured, reason, remediation); commerce reports missing Stripe keys.

Legacy checks without a configuration revision keep their historical result but report
`configuration_match: null`; they do not certify the current configuration. New explicit probes also
save an additive `test_version` field. A different current revision makes the evidence `stale` while
retaining its recorded result; configuration edits clear old probe metadata. Invalid/missing timestamps
remain unknown. Existing compare-and-swap writes reject delayed probe results after configuration edits
or removal. The `test_version` JSON field needs no SQL migration and old records remain readable.

```sh
curl -H "Authorization: Bearer $GORUN_API_KEY" https://example.com/api/v1/site/capabilities
```

Example connection evidence fragment:

```json
{"available":true,"enabled":null,"configured":true,"tested":false,
 "reason":"operation_not_verified",
 "last_check":{"kind":"connectivity","status":"passed","result":"passed",
               "checked_at":"2026-10-21T10:00:00Z","configuration_match":null,
               "reason":"connectivity_only","remediation":"Verify the specific operation separately; discovery does not prove execution permissions."}}
```

Module activation reuses `kernel.OptionalModule`, before catalogs, migrations, routes and job handlers
are registered. Disabled modules retain their tables/settings and re-enable through their existing
activation configuration and a restart. Core modules that do not implement that gate stay enabled;
`GORUN_ADDONS` alone is not a universal switch for arbitrary modules. The report captures boot state,
so editing desired configuration does not pretend that the running process has restarted.

Coverage is explicit: this is module inventory and managed connection evidence, not a complete report
of every business workflow, legacy mail/storage configuration, or caller's operation permissions.
Existing queued jobs from disabled add-ons may still be claimed by the generic queue and fail as
unregistered; parking those jobs, durable background refreshes and operation-specific evidence are
follow-up work in [CORE-UPGRADE-ROADMAP.md](CORE-UPGRADE-ROADMAP.md). Connection endpoints, names,
credentials and raw upstream errors are excluded. Unreadable saved records return the standard
`500 internal_error`; access failures are `401 unauthorized` / `403 forbidden`.

### Background observations

Expensive or external checks never run inside a page or read-only API request. Each registered
capability kind keeps one **observation** row per key with the last successful read (`saved`, with
its age, revision and evidence `scope`) separately from the newest attempt (`attempt`). A failed
attempt therefore never erases or relabels the last good result, and missing values are `null`,
never a fabricated zero.

| Endpoint | Scope | Behaviour |
| --- | --- | --- |
| `GET /api/v1/system/observations[?kind=…]` | `system.health` | Bounded local read of every saved observation (or one kind). |
| `GET /api/v1/system/observations/{kind}/{key}` | `system.health` | One key; `status: never` when it exists but was never refreshed; `404` for an unknown kind or a removed key. |
| `POST /api/v1/system/observations/{kind}/{key}/refresh` | `settings.manage` | Queues one background job and answers `202` with `{job_id, generation, deduplicated}`. A refresh already queued or running for the same key and configuration revision is returned instead of a second job; `409 capability_disabled` when the capability is off. |

`status` is one of `never`, `waiting`, `running`, `fresh`, `stale` (older than the kind's freshness
interval or saved for another configuration revision), `failed` (newest attempt failed; `saved`
still holds the last success) or `disabled`. Attempts run with a per-kind timeout and a bounded
number of retries through the job queue; the scheduler admits at most a few in-flight refreshes per
kind. Results are fenced: an attempt queued for one configuration revision is discarded
(`attempt.status: superseded`) when the key was edited or removed before or while it ran, so a late
result never certifies new credentials. Disabled capabilities are neither scheduled nor refreshed;
their queued jobs are parked (`parked_reason`) and released unchanged when the handler returns.

Registered kinds: `update.release` (key `latest`: the trusted release index), `commerce.billing_server`
(one key per billing server: read-only catalog fetch), `connection` (per profile connectivity),
`operation.s3`, `operation.ai` (per profile operation tests, on request only) and `operation.mail`
(key `user:<id>`, on request only). Error strings are
truncated and come from the module's own sanitized messages; credentials, hosts and raw upstream
bodies are not stored.

```json
{"kind":"commerce.billing_server","key":"01J…","status":"failed","enabled":true,
 "attempt":{"status":"failed","started_at":"2026-10-22T10:05:00Z","finished_at":"2026-10-22T10:05:05Z","number":2,
            "duration_ms":5000,"error":"timeout","remediation":"Check the billing server address, client id and secret, then run the server test.","job_id":"01J…","generation":3},
 "saved":{"at":"2026-10-22T09:00:00Z","age_seconds":3900,"revision":"4f1c…","revision_match":true,"duration_ms":212,"scope":"connectivity","data":{"plans":3}}}
```

### Staged updates

`GET /api/v1/system/update` (`system.update`) reports the serving identity (`running_version`,
with `is_release` false for development builds), the last durably accepted target
(`accepted_version`), the saved `latest` observation (version, observed time and age, source,
status) and `update_available` (`null` whenever latest or the running identity is unknown or stale),
plus the `active` and `last` operations. Nothing in this read contacts a provider.

`POST /api/v1/system/update` with `{"version":"0.15.0","expected_digest":"sha256:…"}` records an
operation, takes the durable data-directory lease and queues the lifecycle job; it answers `202`
with the **pending** operation. Errors: `422 validation_failed` (not a release tag / bad digest),
`409 operation_in_progress` with `fields.operation_id` and `fields.holder` (another operation, a
backup or a restore owns the data directory, or the provider still reports an earlier accepted
deployment in progress), `503 update_not_configured` (no trusted release source).

Operations (`GET /api/v1/system/update/operations[/{id}]`) carry independent phases —
`compatibility`, `verification`, `backup`, `activation`, `health`, `acceptance`, and on a health
failure `rollback_compatibility`, `rollback_activation`, `rollback_health` — each with `status`,
`started_at`, `finished_at`, `duration_ms`, `progress` (null when unmeasurable), `attempts`,
`result`, `error`, `remediation` and `retryable`. `requested_version`, `running_version` (serving
identity at request), `activated_version` (confirmed by the serving process) and `accepted_version`
are separate fields; `deployment_ref` is a provider reference and never means activation.
`migration_state` records whether activation applied migrations, independently from acceptance.
Terminal statuses are `succeeded`, `failed`, `interrupted` (process restarted mid-phase; nothing is
assumed), `rolled_back` and `rollback_failed`. See [UPGRADE.md](UPGRADE.md#7-staged-updates-in-app)
for configuration, adapters and limits.

### Transport limits

`x-gorun.limits` exposes the pagination/helper limits and installed builder/media module limits.
`x-max-body-bytes` is per-operation metadata. Media exposes its default (25 MiB per file), the setting
`media.max_upload_mb` and 1 MiB multipart overhead separately: the default is not an effective configured
limit. Read that setting with `settings.manage` for the configured value. The OpenAPI document is a
snapshot of the mounted registry when first requested; it does not promise to reflect later settings changes.

| Operation/boundary | Size limit and actual error |
|---|---|
| `api.Decode` JSON helper | 1 MiB; `413 payload_too_large` |
| Builder draft/publish JSON | 1 MiB; `413 payload_too_large`; stale token `409 conflict` |
| Content JSON decoder | Reads at most 4 MiB; oversized/incomplete JSON may give `400 bad_request`; no promised 413 |
| Media multipart upload | Configured file limit plus 1 MiB envelope overhead; `413 too_large`; rejected file type `415 unsupported_media_type` |
| WXR admin upload | Documented 200 MB boundary; native `/admin/migrations` workflow, not a REST JSON upload; see [Migration](MIGRATION.md) |

Module-specific error responses use the same Error envelope and include examples. `429 rate_limited`
includes `Retry-After`; retry generation only according to that workflow's own idempotency contract.
Creation and PATCH now use separate `CreateInput` and `PatchInput` schemas. Regenerate clients that
previously assumed the service-level `Input` was the partial REST contract.

## Content create and PATCH semantics

Creation requires `type`; title and other writable fields default when omitted (custom content types
can still require their own fields). Omitted `locale` uses the request/admin content locale when the
request carries one (admin session), otherwise `site.locale`. `PATCH /content/{id}` merges over the current writable state.
Unknown keys are ignored by the current content decoder. This endpoint has **no CAS/expected-version
contract**; a load-modify-write can overwrite an intervening edit. Builder endpoints provide CAS separately.

| Field | Omitted | JSON null | Empty value |
|---|---|---|---|
| `title`, `excerpt`, `body`, `parent_id`, `template`, `featured_media_id` | Keep | Keep | `""` clears; HTML is sanitized |
| `slug` | Keep | Keep | `""` regenerates from title |
| `type` | Keep | Keep | `""` keeps type; changing type is rejected |
| `author_id`, `locale` | Keep | Keep | `""` keeps existing value; ownership rules apply |
| `status` | Keep | Keep | `""` becomes draft; DELETE is required to trash |
| `menu_order` | Keep | Reset to 0 | Use integer 0 to reset |
| `publish_at` | Keep | Keep | `""` clears; scheduled status still requires a datetime |
| `fields`, `seo`, `terms` | Keep | Clear map before validation/defaults | `{}` merges and keeps existing keys |
| A taxonomy inside `terms` | Keep | Clear that taxonomy | `[]` clears that taxonomy |

Object patches merge by key. Field defaults and required-field validation still apply; fields removed from
the type's schema remain preserved. SEO key null is a stored null, not a deletion operator.
Content listing uses `order=price:desc,title`, `taxonomy.{tax}` and `fields.{key}.{op}` filters; it does not
use the generic helper's `sort` syntax. Its page/per_page defaults remain 1/20, capped at 100.

## Operations

The operations below come from the existing public OpenAPI endpoint. Regenerate offline with
`GORUN_THEMES=./themes go run ./cmd/gorun api-docs --update docs/API.md`; check with `--check docs/API.md`.
The command boots a fresh temporary fixture without job workers or a network listener. Tables show the
`/api` mount prefix and `x-scopes`. Every GET also answers HEAD.

<!-- BEGIN GENERATED API OPERATIONS -->
Generated from the current locally booted OpenAPI document: **177 operations** in 24 tags.

### builder

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/builder/{id}/revisions/{revision}` | Read one page-builder layout revision | `content.edit` |
| GET | `/api/v1/builder/{id}/revisions` | List page-builder layout revisions (newest first) | `content.edit` |
| GET | `/api/v1/builder/{id}` | Read a private structured page layout and optimistic draft token | `content.edit` |
| POST | `/api/v1/builder/{id}/publish` | Publish a saved layout using its current opaque draft token | `content.edit`, `content.publish` |
| POST | `/api/v1/builder/{id}/revisions/{revision}/restore` | Restore a layout revision into the private draft | `content.edit` |
| PUT | `/api/v1/builder/{id}/draft` | Save a validated page layout as a separate private draft | `content.edit` |

### canvases

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/canvases/{id}` | Get a readable canvas board document (grant-filtered for agent keys; REST refuses agent keys) | `canvas.read`, `content.view` |
| GET | `/api/v1/canvases` | List readable canvas boards (owner boards or grant-scoped) | `canvas.read`, `content.view` |

### comments

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/comments/{id}` | Delete a comment permanently | `comments.moderate` |
| GET | `/api/v1/comments` | List comments (newest first) | `comments.moderate` |
| PATCH | `/api/v1/comments/{id}` | Moderate a comment (set its status) | `comments.moderate` |

### commerce

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/commerce/products/{id}` | Delete a product | `commerce.manage` |
| GET | `/api/v1/commerce/account-settings` | Read customer account registration policy | `commerce.manage` |
| GET | `/api/v1/commerce/entitlements` | List entitlements | `commerce.view_orders` |
| GET | `/api/v1/commerce/orders/{id}` | Read order details and frozen line items | `commerce.view_orders` |
| GET | `/api/v1/commerce/orders` | List orders | `commerce.view_orders` |
| GET | `/api/v1/commerce/products` | List products | `commerce.manage` |
| GET | `/api/v1/commerce/servers` | List billing servers (secrets are never returned) | `commerce.manage` |
| PATCH | `/api/v1/commerce/account-settings` | Configure customer registration without changing payment or email | `commerce.manage` |
| PATCH | `/api/v1/commerce/requests/{id}` | Track service fulfillment independently of payment | `commerce.manage` |
| POST | `/api/v1/commerce/entitlements/{id}/reconcile` | Reconcile an entitlement on its billing server | `commerce.manage` |
| POST | `/api/v1/commerce/entitlements/{id}/resume` | Resume an entitlement on its billing server | `commerce.manage` |
| POST | `/api/v1/commerce/entitlements/{id}/suspend` | Suspend an entitlement on its billing server | `commerce.manage` |
| POST | `/api/v1/commerce/products` | Create or replace a product (id empty = create) | `commerce.manage` |
| POST | `/api/v1/commerce/requests` | Record a manual request without initiating payment | `commerce.manage` |
| POST | `/api/v1/commerce/servers/{id}/test` | Test a billing server (GET /catalog) | `commerce.manage` |
| POST | `/api/v1/commerce/servers` | Create or update a billing server (secret is write-only) | `commerce.manage` |

### content

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/content/{id}/translations` | Take the item out of its translations | `content.edit` |
| DELETE | `/api/v1/content/{id}` | Trash content (hard=1 deletes) | `content.delete` |
| GET | `/api/v1/content/{id}/revisions` | List revisions (newest first) | `content.view` |
| GET | `/api/v1/content/{id}/translations` | List the item and its translations (by locale) | `content.view` |
| GET | `/api/v1/content/{id}` | Get content | `content.view` |
| GET | `/api/v1/content` | Query content | `content.view` |
| PATCH | `/api/v1/content/{id}` | Update content (merge: omitted fields keep their value) | `content.edit` |
| POST | `/api/v1/content/query` | Query content with the full query DSL | `content.view` |
| POST | `/api/v1/content/{id}/publish` | Publish content now | `content.publish` |
| POST | `/api/v1/content/{id}/revisions/{revision}/restore` | Restore a revision | `content.edit` |
| POST | `/api/v1/content/{id}/translations/link` | Link another item (and its translations) as translations | `content.edit` |
| POST | `/api/v1/content/{id}/translations` | Copy the item into another locale as a linked draft | `content.create` |
| POST | `/api/v1/content/{id}/unpublish` | Return content to draft | `content.publish` |
| POST | `/api/v1/content` | Create content | `content.create` |

### extensions

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/extensions/{slug}` | Uninstall an extension | `extensions.manage` |
| GET | `/api/v1/extensions/{slug}` | Get an extension | `extensions.manage` |
| GET | `/api/v1/extensions` | List installed extensions | `extensions.manage` |
| POST | `/api/v1/extensions/{slug}/activate` | activate an extension | `extensions.manage` |
| POST | `/api/v1/extensions/{slug}/deactivate` | deactivate an extension | `extensions.manage` |

### keys

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/keys/{id}` | Revoke a key | `api_keys.manage` |
| GET | `/api/v1/keys` | List API and agent keys (never their secrets) | `api_keys.manage` |
| POST | `/api/v1/keys/{id}/rotate` | Rotate one of your keys; the new token is returned once | `api_keys.manage` |
| POST | `/api/v1/keys` | Create a key; the token is returned once | `api_keys.manage` |

### mcp

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/mcp/approvals` | List MCP approvals (filter with ?status=pending\|approved\|denied\|expired\|failed) | `mcp.approve` |
| POST | `/api/v1/mcp/approvals/{id}/approve` | Approve a pending MCP call: it executes once and the result is stored (no-op when already decided) | `mcp.approve` |
| POST | `/api/v1/mcp/approvals/{id}/deny` | Deny a pending MCP call (no-op when already decided) | `mcp.approve` |

### media

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/media/{id}` | Delete a media item | `media.delete` |
| GET | `/api/v1/media/{id}` | Get a media item | `media.upload` |
| GET | `/api/v1/media` | List media | `media.upload` |
| PATCH | `/api/v1/media/{id}` | Update alt, caption or title | `media.upload` |
| POST | `/api/v1/media` | Upload a media file | `media.upload` |

### membership

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/membership/manage` | Read all memberships | `membership.manage` |
| GET | `/api/v1/membership/submissions` | Read own submissions | `membership.read` |
| GET | `/api/v1/membership` | Read own membership, effective expiry and content quota | any key or session |
| POST | `/api/v1/membership/grants` | Create explicit local membership; never charges or renews payment | `membership.manage` |
| POST | `/api/v1/membership/plans` | Save explicit membership plan limits | `membership.manage` |
| POST | `/api/v1/membership/submissions` | Submit owned pending content within configured membership quota | `membership.submit` |
| PUT | `/api/v1/membership/grants/{id}` | Update membership with expected version and immutable owner | `membership.manage` |

### menus

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/menus/{id}` | Delete a menu | `menus.manage` |
| GET | `/api/v1/menus/{id}` | Get a menu by ID or slug (items resolved for display) | `menus.manage` |
| GET | `/api/v1/menus` | List menus with their items | `menus.manage` |
| POST | `/api/v1/menus` | Create or update a menu | `menus.manage` |
| PUT | `/api/v1/menus/{id}/items` | Replace a menu's item tree | `menus.manage` |

### migrations

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/migrations/sources` | List WordPress sources | `migrations.run` |
| GET | `/api/v1/migrations/{id}/log` | Migration log (newest window, oldest first) | `migrations.run` |
| GET | `/api/v1/migrations/{id}/mapping` | Current type/taxonomy/field mapping (defaults from the report) | `migrations.run` |
| GET | `/api/v1/migrations/{id}/report` | Last validation report (includes Markdown) | `migrations.run` |
| GET | `/api/v1/migrations/{id}` | Migration status and progress counts | `migrations.run` |
| GET | `/api/v1/migrations` | List migrations | `migrations.run` |
| POST | `/api/v1/migrations/sources/{id}/discover` | Scan a source and return the Discovery Report | `migrations.run` |
| POST | `/api/v1/migrations/sources` | Create a WordPress source (REST URL + optional application password) | `migrations.run` |
| POST | `/api/v1/migrations/{id}/discover` | Scan the migration's source | `migrations.run` |
| POST | `/api/v1/migrations/{id}/pause` | Pause the import | `migrations.run` |
| POST | `/api/v1/migrations/{id}/preview` | Dry run: state/date plan without content or media writes | `migrations.run` |
| POST | `/api/v1/migrations/{id}/resume` | Resume a paused import | `migrations.run` |
| POST | `/api/v1/migrations/{id}/retry` | Retry failed items | `migrations.run` |
| POST | `/api/v1/migrations/{id}/start` | Start (or re-run) the import | `migrations.run` |
| POST | `/api/v1/migrations/{id}/validate` | Run validation (counts, render checks, broken links, shortcodes) | `migrations.run` |
| POST | `/api/v1/migrations` | Create a migration of a source | `migrations.run` |
| PUT | `/api/v1/migrations/{id}/mapping` | Save the mapping | `migrations.run` |

### redirects

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/redirects/{id}` | Delete a redirect | `redirects.manage` |
| GET | `/api/v1/redirects` | List redirects | `redirects.manage` |
| POST | `/api/v1/redirects` | Create or update a redirect | `redirects.manage` |

### roles

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/roles/{slug}` | Delete a custom role | `users.manage` |
| GET | `/api/v1/roles/{slug}` | Get a role | `users.manage` |
| GET | `/api/v1/roles` | List roles | `users.manage` |
| PATCH | `/api/v1/roles/{slug}` | Update a role | `users.manage` |
| POST | `/api/v1/roles` | Create a custom role | `users.manage` |

### saas

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/saas/apps/{id}` | Delete unused native SaaS object; preserve imported/referenced history | `saas.manage` |
| DELETE | `/api/v1/saas/entitlements/{id}` | Delete unused native SaaS object; preserve imported/referenced history | `saas.manage` |
| GET | `/api/v1/saas/manage` | Read all apps, subscriptions and usage | `saas.manage` |
| GET | `/api/v1/saas` | Read owned apps, subscriptions and usage | any key or session |
| PATCH | `/api/v1/saas/apps/{id}` | Update app with expected version | `saas.manage` |
| PATCH | `/api/v1/saas/entitlements/{id}/services` | Edit recorded services and renew subscription; no payment or infrastructure provisioning | `saas.manage` |
| PATCH | `/api/v1/saas/entitlements/{id}` | Update subscription state with optimistic concurrency | `saas.manage` |
| POST | `/api/v1/saas/apps` | Create owned native application | `saas.manage` |
| POST | `/api/v1/saas/entitlements/{id}/consume` | Consume owned subscription quota atomically | any key or session |
| POST | `/api/v1/saas/entitlements` | Create owned subscription and quotas | `saas.manage` |
| PUT | `/api/v1/saas/entitlements/{id}` | Update owned plan quotas with expected version; retain usage | `saas.manage` |

### settings

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/settings` | Read site and mail settings (secrets are never returned) | `settings.manage` |
| PATCH | `/api/v1/settings` | Update site and mail settings | `settings.manage` |

### site

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/health` | Readiness: database reachable | any key or session |
| GET | `/api/v1/me` | Current principal and effective scopes | any key or session |
| GET | `/api/v1/site/capabilities` | Saved capability evidence (availability, activation, configuration, connectivity, operation tests) with no external checks | `system.health` |
| GET | `/api/v1/site/info` | Public site identity, running build, languages and theme appearance with provenance | public |
| GET | `/api/v1/site/runtime` | Protected running process information; no credentials or deployment paths | `settings.manage` |
| GET | `/api/v1/site` | Site structure: content types, taxonomies, menus, theme, templates | `content.view` |
| POST | `/api/v1/site/capabilities/connections/{id}/check` | Queue a read-only connectivity/discovery check of a connection profile; answers 202 with the task reference | `settings.manage` |
| POST | `/api/v1/site/capabilities/connections/{id}/operation` | Queue an isolated operation test (S3: write/read/delete one probe object; AI: one tiny completion); answers 202 | `system.capability_test` |
| POST | `/api/v1/site/capabilities/services/mail/operation` | Queue one test message to the requesting operator's own address; answers 202 | `system.capability_test` |
| POST | `/api/v1/site/capabilities/services/storage/operation` | Queue one write/read/delete probe on the storage new uploads use (managed destination, configured bucket or local disk); answers 202 | `system.capability_test` |

### system

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/audit` | List audit log entries | `system.health` |
| GET | `/api/v1/jobs` | List jobs | `system.health` |
| GET | `/api/v1/system/health` | System health report | `system.health` |
| GET | `/api/v1/system/observations/{kind}/{key}` | One saved observation; status never is a key that was never refreshed | `system.health` |
| GET | `/api/v1/system/observations` | Saved background observations of expensive or external checks, with age and attempt state (no external calls) | `system.health` |
| GET | `/api/v1/system/update/operations/{id}` | One staged update operation with every phase, its timing, result and remediation | `system.update` |
| GET | `/api/v1/system/update/operations` | List staged update operations, newest first (phases omitted) | `system.update` |
| GET | `/api/v1/system/update` | Running, accepted and latest versions with the active and last staged operations (no external calls) | `system.update` |
| POST | `/api/v1/jobs/{id}/retry` | Retry a failed or dead job | `system.health` |
| POST | `/api/v1/system/observations/{kind}/{key}/refresh` | Queue a background refresh and return its task reference at once; concurrent requests share one job | `settings.manage` |
| POST | `/api/v1/system/update` | Start a staged update operation; returns 202 with the pending operation, never a completed update | `system.update` |

### taxonomies

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/taxonomies/{tax}/terms/{id}` | Delete a term (children move up) | `taxonomy.manage` |
| DELETE | `/api/v1/taxonomies/{tax}` | Delete a taxonomy and its terms | `taxonomy.manage` |
| GET | `/api/v1/taxonomies/{tax}/terms/{id}` | Get a term | `content.view` |
| GET | `/api/v1/taxonomies/{tax}/terms` | List terms | `content.view` |
| GET | `/api/v1/taxonomies/{tax}` | Get a taxonomy | `content.view` |
| GET | `/api/v1/taxonomies` | List taxonomies | `content.view` |
| PATCH | `/api/v1/taxonomies/{tax}/terms/{id}` | Update a term | `taxonomy.manage` |
| PATCH | `/api/v1/taxonomies/{tax}` | Update a taxonomy | `taxonomy.manage` |
| POST | `/api/v1/taxonomies/{tax}/terms` | Create a term | `taxonomy.manage` |
| POST | `/api/v1/taxonomies` | Create or replace a taxonomy | `taxonomy.manage` |

### themes

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/templates/{name}/override` | Delete a template's override and draft (the theme file is used again) | `themes.manage` |
| GET | `/api/v1/templates/{name}` | Read a template (published source and draft) | `themes.manage` |
| GET | `/api/v1/templates` | List the active theme's templates and overrides | `themes.manage` |
| GET | `/api/v1/themes` | List installed themes | `themes.manage` |
| POST | `/api/v1/templates/{name}/publish` | Publish a template override draft | `themes.manage` |
| POST | `/api/v1/themes/{slug}/activate` | Activate a theme | `themes.manage` |
| PUT | `/api/v1/templates/{name}/draft` | Save a template override draft (not live until published) | `themes.manage` |

### types

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/types/{slug}` | Delete a content type | `types.manage` |
| GET | `/api/v1/types/{slug}` | Get a content type | `content.view` |
| GET | `/api/v1/types` | List content types | `content.view` |
| PATCH | `/api/v1/types/{slug}` | Update a content type (fields given replace) | `types.manage` |
| POST | `/api/v1/types` | Create or replace a content type | `types.manage` |

### users

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/users/{id}` | Delete a user | `users.manage` |
| GET | `/api/v1/users/{id}` | Get a user | `users.manage` |
| GET | `/api/v1/users` | List users | `users.manage` |
| PATCH | `/api/v1/users/{id}` | Update a user | `users.manage` |
| POST | `/api/v1/users` | Create a user | `users.manage` |

### webhooks

| Method | Path | Summary | Scopes |
|---|---|---|---|
| DELETE | `/api/v1/webhooks/{id}` | Delete a webhook and its delivery log | `webhooks.manage` |
| GET | `/api/v1/webhooks/events` | List subscribable events | `webhooks.manage` |
| GET | `/api/v1/webhooks/{id}/deliveries` | List recent deliveries, newest first | `webhooks.manage` |
| GET | `/api/v1/webhooks/{id}` | Get a webhook | `webhooks.manage` |
| GET | `/api/v1/webhooks` | List webhooks | `webhooks.manage` |
| PATCH | `/api/v1/webhooks/{id}` | Update a webhook (omitted fields keep their value) | `webhooks.manage` |
| POST | `/api/v1/webhook-deliveries/{id}/redeliver` | Queue a past delivery's payload again | `webhooks.manage` |
| POST | `/api/v1/webhooks/{id}/secret` | Rotate the signing secret; the new secret is returned once | `webhooks.manage` |
| POST | `/api/v1/webhooks/{id}/test` | Send a webhook.test event now (one attempt) | `webhooks.manage` |
| POST | `/api/v1/webhooks` | Create a webhook; the signing secret is returned once | `webhooks.manage` |

### workagents

| Method | Path | Summary | Scopes |
|---|---|---|---|
| GET | `/api/v1/workagents/agents` | List owned agent profiles | `workagents.read` |
| GET | `/api/v1/workagents/approvals` | List owned approvals | `workagents.read` |
| GET | `/api/v1/workagents/artifacts` | List owned artifacts | `workagents.read` |
| GET | `/api/v1/workagents/linear-flows/{id}` | Read flow output and exact approval step | `workagents.read` |
| GET | `/api/v1/workagents/runs` | List run status and persisted results | `workagents.read` |
| GET | `/api/v1/workagents/workflows` | List owned schedules | `workagents.read` |
| POST | `/api/v1/workagents/agents/{id}/run` | Queue a text agent run | `workagents.run` |
| POST | `/api/v1/workagents/agents` | Save a native agent profile | `workagents.manage` |
| POST | `/api/v1/workagents/linear-flows/{id}/decision` | Approve or reject the exact flow output | `workagents.manage` |
| POST | `/api/v1/workagents/linear-flows` | Start an explicit linear agent flow with human approval | `workagents.manage` |
| POST | `/api/v1/workagents/workflows` | Save a persistent agent schedule | `workagents.manage` |

<!-- END GENERATED API OPERATIONS -->

## Registering an operation (module authors)

Register operations in your module's `Register` with `d.API.Add`, using the
`api.Op` constructor:

```go
op := api.Op("GET", "/content/{id}", "Get a content item",
    []string{"content"},          // OpenAPI tags
    []string{"content.view"},     // scopes, each checked with auth.Can
    nil,                          // request example (nil: no body)
    content.Item{},               // response example → envelope data schema
    h.get)
d.API.Add(op)

list := api.Op("GET", "/content", "List content", []string{"content"},
    []string{"content.view"}, nil, []content.Item{}, h.list)
list.Params = append(list.Params, api.PageParams...) // page, per_page, sort, order
list.Params = append(list.Params, kernel.APIParam{Name: "type", In: "query", Type: "string"})
d.API.Add(list)
```

- **Paths** are chi patterns relative to the `/api` mount. `"/content"` and
  `"/v1/content"` are equivalent (the `/v1` prefix is added when missing).
  `{id}` and `{id:regex}` path parameters are detected automatically and
  documented as required strings; `chi.URLParam(r, "id")` works in handlers.
- **Scopes** are permission strings. Session users pass when their role has
  the permission; API keys additionally need the scope (or `*`). Failure is
  `401 unauthorized` (anonymous) or `403 forbidden`.
- **Schemas** come from the Go types of the request/response examples:
  `json` tags (`omitempty` → optional), `doc:"…"` → description,
  `enum:"a,b"` → enum, pointers → nullable, `*_at` strings → `date-time`.
  Named structs become `components.schemas` (generics such as
  `Page[Item]` are named `PageItem`; equal names from different packages are
  package-qualified, e.g. `ContentItem` / `MenusItem`). Request bodies are
  documented for POST/PUT/PATCH only. For list operations pass a slice
  (`[]T{}`) as the response example; the envelope's `meta` is documented for
  you.
- Handlers run behind a guard that recovers panics (500 envelope, stack only
  in the log) and defaults `Content-Type` to `application/json`.
- Every GET operation also answers HEAD (no body) unless you register a HEAD
  operation yourself. A wrong method gets `405 method_not_allowed` with an
  `Allow` header.
- Two operations that become equal after `/v1` normalization (`GET /x` and
  `GET /v1/x`) are a programming error: the first handler is mounted, guarded
  by the union of both scope lists, and an error is logged.
- Mounting is lazy: the router and the OpenAPI document are built once, on
  the first API request, from everything registered during boot. Register
  only inside `Register`.

### Handler helpers (`internal/api`)

| Helper | Purpose |
|---|---|
| `api.Pagination(r) (page, perPage)` | `?page` (default 1), `?per_page` (default 20, max 100) |
| `api.Sort(r, allowed) (by, dir)` | `?sort` (must be in `allowed`; `-field` = desc) and `?order=asc\|desc`; default `allowed[0] desc` |
| `api.Decode(w, r, &v) error` | JSON body, 1 MiB cap, unknown fields rejected; missing/empty body → `400 empty_body` |
| `api.WriteItem(w, v)` | `200 {"ok":true,"data":v}` |
| `api.WriteList(w, items, total, page, perPage)` | `200` list envelope with `meta`; nil slices become `[]` |
| `api.WriteError(w, status, code, msg, fields)` | error envelope |
| `api.NotFound(w)` | `404 not_found` |
| `api.Fail(w, r, err)` | maps any error: decode → 400/413, validation → 422 with fields, not-found sentinels → 404, else 500 (logged, generic message) |
| `api.ParseValidation(err) (status, fields)` | recognises `*api.DecodeError`, `api.FieldErrorer` (`FieldErrors() map[string]string`) and errors with a `Fields map[string]string` field (`content.ValidationError`, `auth.ValidationError`) |

### Testing (`internal/api/apitest`)

```go
app := apitest.Boot(t, content.New(), taxonomy.New())      // settings+auth+api added for you
c := apitest.NewClient(t, app, "content.view")               // admin user, key with these scopes ("*" if none)
status, body := c.Get("/content")                            // "/content" → /api/v1/content
status, body = c.Post("/content", map[string]any{"title": "Hi"})
id := apitest.Data(body)["id"]
apitest.New(t, app, apitest.Options{Role: "editor", RatePerMin: 2})
```

Any package's tests may import `apitest`: the import-order test exempts
`_test.go` files. Non-test code must never import it.
Set `kernel.LogOutput = io.Discard` in `TestMain`.

## Content read boundaries

WordPress REST migrations request `publish,draft,pending,future,private` with `context=edit` for Application Password sources, and `publish` for anonymous sources. The content collection order is stable by source ID. Trash, auto-drafts and custom statuses are excluded; attachments keep their native inheritance policy. Valid restricted accounts see only content permitted by WordPress. Inaccessible collections are explicit findings and are not default-mapped.

Discovery `counts` contain accessible content rows, while `collections` separately records `visible`, `reported_total`, `reported_pages`, `pages_read`, `count_known`, `pages_known` and `limited`. WordPress totals can precede permission filtering. All advertised pages are read even when one accessible page is empty; unknown pagination and the 500-page full-read bound are errors. Media discovery intentionally samples at most five pages and labels that bound. Discovery reads every accessible content page, so it may take more requests than the previous header-count scan.

Discovery and preview expose optional `content_state_counts` as content type → source state → readable row count. Discovery omits unavailable types because their counts are unknown. Preview counts selected source rows including planned skips; its existing `counts` maps planned operations separately. Neither state map proves transferred rows or semantic reconciliation. REST content payloads outside the requested state scope are refused: discovery reports that collection unavailable, and selected import content fails rather than accepting an unexpected state (including attachment `inherit`).

Preview `content_states` includes source/local state, `source_date` (UTC) and the native `publish_at` plan. Draft/pending source dates remain provenance, not a local publication timestamp. Dry run changes migration plans but does not write content or media. Validation `counts` compare selected readable source rows with imported/skipped/failed rows. Migration `status` and item-operation `percent` do not establish source coverage or browser acceptance: a failed fetch can leave 100% of already-known operations done. Readiness, errors, warnings and unverified checks remain authoritative and separate.

Configured credential failures are fatal throughout discovery/import, including WordPress's permission-first status validator response. A fresh authenticated profile probe distinguishes a revoked Application Password from a valid account denied a collection; transport/cancellation failures retain their original cause. The WooCommerce catalog adapter remains a separate published Store API contract; this change does not claim draft/private product migration.

Author and contributor principals can read their own content or published content from public types. Queries apply that restriction before counting and pagination; users cannot broaden it with author_id. Revision histories of other users remain forbidden, and hidden translation members are filtered. Admin/editor and internal system contexts retain their existing access.

## Structured page layouts

The private draft endpoints require `content.edit` and enforce page ownership. `GET /api/v1/builder/{id}` returns a `Draft`; use its opaque `updated_at` token as `base_updated_at` in `PUT /api/v1/builder/{id}/draft`, alongside `document`. Documents have `version: 1` and an ordered `sections` array. Tokens are versions, not human timestamps.

`POST /api/v1/builder/{id}/publish` takes `{ "updated_at": "current-draft-token" }` and additionally requires `content.publish`. Saving remains private; publishing replaces public page HTML. Conflicts return 409 and require reloading/reviewing the latest layout. Strict JSON bodies are bounded to 1 MiB, layouts to 60 sections and each repeated section to 24 entries. Empty drafts are allowed, but publication requires visible content with a heading. The JSON document is portable; media URLs must be migrated separately.

Layout history lives in `builder_revisions`. `GET /api/v1/builder/{id}/revisions` returns newest-first snapshots (`kind` is `save`, `publish`, `restore` or `reset`) with the full `document`. `POST …/revisions/{revision}/restore` takes `{ "updated_at": "current-draft-token" }`, snapshots the current draft, then replaces only the private draft. Public HTML is unchanged until publish. Stale tokens return 409. History length defaults to 25 (`builder.revisions_keep`).

## Template editor contract

All template operations require `themes.manage`. The template list response has
explicit `templates` and `overrides` arrays in OpenAPI. Reading a template returns
`name`, `source`, published `body`, and `draft` (`null` when absent, `""` for an
intentionally empty draft). This is also the MCP `get_template` response.

`PUT /api/v1/templates/{name}/draft` accepts one JSON object with a required,
non-null string `body`, for example `{"body":"<footer>{{.Site.Title}}</footer>"}`.
An empty string is valid. Missing/null bodies and trailing JSON return 400
`bad_request` without replacing the existing draft. The request byte limit is
published in OpenAPI `x-max-body-bytes`. Saving does not publish. Review using the
authenticated admin draft preview, then explicitly call
`POST /api/v1/templates/{name}/publish` to publish the saved draft.

Media reads and uploads include `variants` (variant name → URL, width, height),
which callers should inspect rather than assuming every image has every size.
`PATCH /api/v1/media/{id}` changes metadata only: omitted/null fields are retained,
while an explicit empty string clears the corresponding field. MCP `get_media`
exposes the same media object; `update_media` uses the approval pipeline.
