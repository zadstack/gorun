# GoRun architecture

> **ملخص بالعربية.** GoRun «مونوليث معياري»: ملف تنفيذي واحد (`cmd/gorun`) وقاعدة SQLite واحدة،
> وواجهات HTML مُصيَّرة على الخادم، وواجهة REST وخادم MCP فوق الخدمات نفسها. النواة (`internal/kernel`)
> تُقلع الوحدات بترتيب ثابت (`kernel.BootOrder`)، ولا يجوز لوحدة أن تستورد إلا وحدات تسبقها في الترتيب،
> ويفرض ذلك اختبار `importorder_test.go`. تتواصل الوحدات عبر سجل خدمات (`Deps.Services`) وخطافات
> (`hooks`: إجراءات متزامنة، ومرشحات، وأحداث غير متزامنة عبر طابور المهام). يوثّق هذا الملف جدول الوحدات
> والخدمات والخطافات والمهام والصلاحيات، ومسار الطلب، وقائمة خطوات إضافة وحدة جديدة.

GoRun is a modular monolith: one Go binary (`cmd/gorun`), one SQLite database, server-rendered
HTML (Go templates + HTMX + Alpine; Tailwind precompiled for the default theme, a local runtime for the admin) and a REST API and MCP server over the same
services.

## Kernel and infrastructure

`internal/kernel` boots everything. `kernel.Boot(ctx, cfg, cmd/gorun.Modules())` opens the database
and then:

1. migrates the kernel infrastructure (`audit`, `jobs`);
2. migrates and registers the `settings` module;
3. loads i18n: the core catalog plus every module that implements `kernel.CatalogProvider`;
4. migrates and registers the remaining modules, in boot order;
5. mounts the routers (`Public`, `Admin` at `/admin`, `API` at `/api`, `Webhooks` at `/webhooks`);
6. starts the job workers and the cron scheduler.

`kernel.BootWithOptions(..., kernel.BootOptions{StartJobs: false})` skips step 6. CLI commands that
may run beside a live server use it: `gorun mcp` and `gorun import-wxr` always, `gorun reindex`
with `--no-jobs` (`gorun mcp --jobs` opts back in). The boot log says which (`jobs: workers
started` / `jobs: workers not started`). With `StartJobs` the kernel first takes an exclusive OS lock
(`flock`) on `<data>/gorun.lock` (`kernel.LockDataDir`) and fails with `kernel.ErrDataDirLocked`, naming
the holder, when another process has it; `App.Close` releases it and the OS drops it if the process dies.

Infrastructure packages are importable by every module and never import a domain module:

| Package | Role |
|---|---|
| `kernel` | `Module` interface, `Deps`, boot, `BaseURL(ctx, d)` (the one public base URL: setting `site.base_url`, else `GORUN_BASE_URL`; used for absolute links and the redirects own-host check; cookie `Secure` and HSTS follow the request instead: TLS or a trusted proxy's `X-Forwarded-Proto`, cookies falling back to the `GORUN_BASE_URL` scheme), routers, registries (nav, per-request nav providers, widgets, perms, theme funcs, API ops, MCP tools), service locator |
| `config` | flags → env (`GORUN_*`) → `gorun.toml` → defaults |
| `db` | SQLite open (WAL), migrations, ULIDs, time helpers, `InTx`, `Encrypt`/`Decrypt` (AES-256-GCM) |
| `hooks` | actions, filters, async events; `Action`/`Filter` return a token for `Remove` |
| `httpx` | middleware (request id, logging, CSP, recovery), JSON envelope, form binding, admin templates, `NewSafeClient`/`NewSafeClientOpts` |
| `settings` | key/value JSON settings: `Get`, `Set`, `SetTx`, `All`, `Delete`, `DeletePrefix`(`Tx`) |
| `i18n` | ar/en catalogs, locale negotiation, RTL |
| `audit` | append-only audit log (`Record`, `RecordTx`) |
| `jobs` | durable queue with retries and backoff, cron; handlers read their attempt with `jobs.AttemptFromContext` |
| `mailer` | SMTP mail from the `mail.*` settings; when `mail.host` is unset it logs the message instead (the body only when `GORUN_ENV=dev`; otherwise recipient + subject at WARN), and the admin "Send sign-in link" action shows the link once (published by admin as the `mailer` service) |

**Transaction rule:** inside `db.InTx` use only the `…Tx` variants (`audit.RecordTx`,
`jobs.EnqueueTx`, `settings.SetTx`, `settings.DeletePrefixTx`). The pool variants (`Record`,
`Enqueue`, `Emit`, `Set`, `Delete`) must run after commit, or they wait on the transaction's lock.

## Modules and boot order

`kernel.BootOrder` is the canonical order. A module may import only modules earlier in the list.
`internal/kernel/importorder_test.go` enforces this for non-test files; test files may import any
module.

| # | Module | Package | Role |
|---|---|---|---|
| 1 | settings | `internal/settings/settingsmod` | settings storage, migrations |
| 2 | auth | `internal/auth` | users, roles/permissions, sessions, CSRF, API keys (`api` and `agent` kinds), magic links, admin shell (login, setup, locale) |
| 3 | content | `internal/content` | content types, custom fields, items, revisions, queries, editor |
| 4 | taxonomy | `internal/taxonomy` | taxonomies and terms |
| 5 | media | `internal/media` | uploads, storage, image variants, SVG sanitizer, picker, `/admin/x/media/{id}/thumb` |
| 6 | menus | `internal/menus` | navigation menus |
| 7 | redirects | `internal/redirects` | URL redirects (public middleware; reserved prefixes such as `/admin`, `/api`, `/media`, `/checkout`, `/account` can never be redirected) |
| 8 | seo | `internal/seo` | meta tags, sitemap.xml, robots.txt, SEO settings |
| 9 | theme | `internal/theme` | theme loading (`./themes`, `/app/themes` or `$GORUN_THEMES`), template hierarchy, overrides |
| 10 | builder | `internal/builder` | starter site drafts/activation, structured page layouts, identity, REST/MCP, independent draft and published JSON snapshots |
| 11 | site | `internal/site` | public front controller (URL → template kind); contact form: stores every submission in `form_submissions` (optional private attachment in `<data>/form-uploads/<id>`) before mailing it; System → Form submissions (`settings.manage`): inbox / unread / spam, detail, download, resend, delete, CSV export and the form settings (`forms.notify`, `forms.recipients`, `contact.attachments`, `contact.attachment_max_mb`) |
| 12 | api | `internal/api` | REST layer: mounts every registered operation, OpenAPI |
| 13 | admin | `internal/admin` | dashboard, users, settings, health, jobs, API keys, audit viewer, admin search, mailer |
| 14 | webhooks | `internal/webhooks` | outgoing webhooks: subscriptions, signed deliveries, retries |
| 15 | commerce | `internal/commerce` | **optional, on by default**: products, Stripe Checkout, orders, entitlements, billing servers (mcp-billing/v1) |
| 16 | saas | `internal/saas` | **optional, on by default**: SaaS plans and tenants on top of commerce |
| 17 | membership | `internal/membership` | **optional, on by default**: member-only content and access rules |
| 18 | workagents | `internal/workagents` | work agents and their workflows |
| 19 | wpmigrate | `internal/wpmigrate` | WordPress/WooCommerce migrator: REST/WXR sources, 7-step wizard, job pipeline, validation report, `gorun import-wxr`; imports WooCommerce products through commerce (`wpmigrate.product_importer` adapter) |
| 20 | mcp | `internal/mcp` | Model Context Protocol server (`/mcp`, `gorun mcp`), human approvals |
| 21 | ext | `internal/ext` | extensions: manifests, HTTP hook bridge, scoped API keys |

`db`, `i18n`, `audit` and `jobs` appear in `BootOrder` for ordering, but they are kernel
infrastructure, not modules you list in `Modules()`.

### Optional modules

A module that implements `kernel.OptionalModule` (`Enabled(ctx, db) (bool, error)`) is evaluated
before any catalog, migration, route, job or hook registration. Disabled, it registers nothing and
its tables, settings and queued jobs are left untouched; enabled again, it reads them back.
Built-in optional modules (commerce, saas, membership) answer with `kernel.AddonEnabled`, which
reads the site setting `addons.<name>.enabled` (a JSON boolean; missing means **enabled**, so
existing installs change nothing). The operator config (`[addons]` in the site config or
`GORUN_ADDONS='{"commerce":"disabled"}'`) can only restrict: mode `disabled` turns an optional
module off without asking it; `enabled` and `auto` leave the decision to the module, and a mode
set for a module that is not optional is ignored (logged, shown in the inventory). `gorun migrate`
applies the same selection as `gorun serve`. A non-boolean value in that setting stops every
command that opens the database; the error names the repair (`GORUN_ADDONS='{"<name>":"disabled"}'
gorun settings set addons.<name>.enabled true`). Events are not held for a module that is off:
`hooks.Emit` jobs run against the subscribers of the current process, so with commerce on and saas
off the `subscription.*` events are delivered to nobody and are not replayed when saas is enabled
again (its local grants then need a manual review). Activation is read at boot; a change needs a restart,
and `kernel.Modules(d)`, the capability report and System → Health report the booted state, never
the intent. **System → Modules** and `GET /api/v1/system/modules` list every module with what it
registered (observed by the kernel during `Register`), its dependencies, activation and
configuration; the page switches optional modules for the next boot, and queued jobs of a disabled
module are parked until it is on again ([MODULES.md](MODULES.md)). The layer map, dependency
policy and the audit behind this are in [BOUNDARIES.md](BOUNDARIES.md);
`internal/kernel/boundaries_test.go` enforces the import policy.

### Building on the core / البناء فوق النواة

`cmd/gorun` is a thin wrapper around `pkg/cli`. A separate binary (for example a hosting
platform with its own modules) imports `github.com/zadstack/gorun/pkg/cli` and calls
`cli.Main(os.Args[1:], opts...)`:

- `cli.WithModules(fn)` receives the site config and the module list (`cli.CoreModules()` first)
  and returns the list to boot; `cli.InsertAfter` keeps a module at its intended position.
- `cli.WithBootOrder(after, names...)` adds module names to `kernel.BootOrder` (boot-order checks
  and System → Health list them).
- `cli.WithCommands(cmds...)` adds top-level commands; their help lines appear before `api-docs`.
  Core command names cannot be replaced.
- `cli.WithAddons(names...)` lists the optional add-ons `gorun api-docs --addon NAME` can boot.
- `cli.WithLogCategory(name, before)` adds an error-inventory category (System → Errors).
- `cli.WithSitePolicyDirectory(dir)` sets the root-owned directory trusted for `GORUN_SITE_POLICY`
  files (default `/var/lib/gorun-site-policies`). It is a build-time choice, never read from the
  environment.
- `cli.WithVersion(v)` sets the version printed by `gorun version` and reported by the API.

Modules can add admin pages with `(*httpx.Templates).RegisterExtension` and translations with
`kernel.CatalogProvider`; no CLI option is needed for those.

## Services

Modules publish services in `Register` with `d.Services.Set(name, impl)`. Later modules consume them
with `kernel.Get[T](d, name)`.

| Name | Constant | Type | Published by |
|---|---|---|---|
| `auth` | `kernel.ServiceAuth` | `auth.Service` | auth |
| `auth.keys` | `kernel.ServiceAPIKeys` | `auth.KeyService` | auth |
| `admin.templates` | `kernel.ServiceAdminTemplates` | `*httpx.Templates` | auth |
| `content` | `kernel.ServiceContent` | `content.Service` | content |
| `taxonomy` | `kernel.ServiceTaxonomy` | `taxonomy.Service` | taxonomy |
| `media` | `kernel.ServiceMedia` | `media.Service` | media |
| `menus` | `kernel.ServiceMenus` | `menus.Service` | menus |
| `redirects` | `kernel.ServiceRedirects` | `redirects.Service` | redirects |
| `seo` | `kernel.ServiceSEO` | `seo.Service` | seo |
| `theme` | `kernel.ServiceTheme` | `theme.Service` | theme |
| `commerce` | `kernel.ServiceCommerce` | `commerce.Service` | commerce |
| `mailer` | `mailer.ServiceName` | `*mailer.Mailer` | admin |
| `mcp` | (internal) | `*mcp.server` | mcp |
| `wpmigrate` | `wpmigrate.ServiceName` | `wpmigrate.Service` | wpmigrate |
| `wpmigrate.product_importer` | `wpmigrate.ProductImporterService` | `wpmigrate.ProductImporter` (+ `ProductMediaReplacer`) | wpmigrate (commerce adapter, when commerce is booted) |
| `ext` | `ext.ServiceName` | `ext.Service` | ext |

`kernel.Modules(d)` returns a copied snapshot of modules supplied to this boot and
the selection made by the existing `OptionalModule` gate. It includes configured
add-ons absent from the supplied build, and never re-runs activation on an HTTP
request. The snapshot is stored privately as `kernel.module_inventory`; callers
should use the accessor. Disabled modules still skip registration and migrations.

The theme service optionally implements `theme.AppearanceProvider` for public
appearance declarations and provenance. This does not expand `theme.Service`, so
downstream implementations remain compatible. The new `/api/v1/site/info` API
uses only its public projection; `/site/runtime` requires `settings.manage` and
the admin module's `/site/capabilities` requires `system.health`. See [API.md](API.md)
for evidence semantics and [CORE-UPGRADE-ROADMAP.md](CORE-UPGRADE-ROADMAP.md) for the
separate background-observation and update-lifecycle work.

## Request lifecycle

`kernel.assemble` builds the root `chi` router after every module has registered:

1. **Root middleware**, for every request: `httpx.RequestID` → `httpx.RealIP` (trusts forwarded headers only
   from a private/loopback peer, i.e. a reverse proxy) → `httpx.Logger` (one `slog` line per request) →
   `httpx.Recover` (panics become 500 and are logged) → `httpx.SecurityHeaders` (CSP with a per-request nonce,
   `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, and HSTS when the request
   is https: TLS, or `X-Forwarded-Proto: https` from a trusted proxy; `includeSubDomains`/`preload` only when
   configured) → `httpx.Compress` → chi `middleware.GetHead` (HEAD served by GET routes).
2. **Probes** on the root: `GET /healthz` (liveness, always `ok`) and `GET /readyz` (pings the database, 503
   when it is unreachable).
3. **Mounts**, each a late-bound sub-router that modules filled during `Register`. Admin and API also carry a
   kernel guard that answers 503 until the auth module has installed its authenticator, so they are never
   served unauthenticated.

| Mount | Middleware (in order) | Notes |
|---|---|---|
| `/admin` | guard → `auth.SessionMiddleware` → locale → `adminGate` (anonymous → `/admin/login`, customers → 403; `/admin/login`, `/admin/setup`, `/admin/logout` are open) → `auth.CSRFMiddleware` | Screens add `auth.RequirePerm(…)`. Pages render through `httpx.Templates` (`web/admin/templates`); HTMX requests get only the page content. |
| `/api` | guard → `auth.APIKeyMiddleware` (bearer `api`-kind key with per-key rate limit, or session + `X-CSRF-Token` on unsafe methods; agent keys → `403 agent_key_not_allowed`; `/api/v1/openapi.json` and `/api/docs` are anonymous) | The api module builds the operation router and the OpenAPI document lazily, on the first API request, from everything registered during boot. Each operation checks its scopes with `auth.Can` and runs behind a panic guard. |
| `/webhooks` | none | No session, no CSRF. Each handler verifies its own signature (`POST /webhooks/stripe`). |
| `/` (Public) | `auth.SessionMiddleware` → `redirects` middleware | The site front controller resolves the URL to a template kind and renders it with the theme. Public POST routes (`/forms/contact`, `/checkout/{slug}`, `/account/login`, `/account/open/{entitlement}`) wrap `auth.CSRFMiddleware()`. The MCP endpoint `/mcp` is also mounted here, behind `auth.MCPKeyMiddleware` (bearer keys of both kinds, no cookies). Media is served under `/media/`, theme assets under `/themes/<slug>/assets/`. |

4. **Handler → service.** Handlers call their module's service, or another module's through
   `kernel.Get[T](d, name)`. Services validate, write inside `db.InTx`, record audit rows (`audit.RecordTx`)
   and enqueue jobs (`jobs.EnqueueTx`) in the same transaction, then fire hooks after commit.
5. **Hooks and jobs.** `hooks.Do` and `hooks.Apply` run in the request. `hooks.Emit` persists a `hooks.event`
   job that a worker delivers to every action subscriber (including the extension HTTP bridge), with retries.

### Generated administrator first-login setup

`auth.BootstrapAdmin` atomically creates the empty site's administrator with the
persistent login alias and `users.onboarding_required = 1`. Regular `Create` and
browser setup do not set this flag. Migration `0011_onboarding.sql` recognizes
only the previous release's unconfigured bootstrap alias/email combination.

Session/key user reads include the internal flag, which is not exported in user
JSON. The admin gate redirects pending accounts to `/admin/onboarding` (HTMX uses
`HX-Redirect`); API/MCP authentication and permission/scope grants are denied.
Reauthentication, locale changes and logout remain reachable. Completing the form
requires session CSRF and recent authentication. One immediate transaction
compares the prior password hash and current session, updates account/site
identity, clears the flag, revokes old sessions/keys/magic links and records
`user.onboarding_complete`. A fresh browser session starts after commit.

The CLI `doctor` does not call Boot, OpenDB or migration inspectors that may
initialize legacy metadata: it loads existing configuration without generating a
secret, opens SQLite with `mode=ro` and `query_only`, and reads recorded schema
checksums and optional-module activation directly. It validates the theme through
`pkg/themekit` and probes only local listen addresses. See [local start](local-start.md).

## Data

Everything lives under the data directory (`GORUN_DATA`, default `./data`):

- `gorun.db` (+ WAL): the SQLite database;
- `media/`: uploaded files and variants;
- `overrides/<theme>/`: template overrides edited in the admin or over MCP;
- `extensions/<slug>/`: installed extension packages;
- `imports/`: WXR files uploaded in the migration wizard;
- `backups/`: output of `gorun backup` (`<timestamp>.db` copied with `VACUUM INTO`, plus
  `<timestamp>.tar.gz` of `media/`, `overrides/`, `extensions/`, `form-uploads/`; local `secret.key` and `gorun.toml` only with
  `gorun backup --include-secrets`, never from the admin).
- `restores/<timestamp>/`: confined output of the admin restore, staged in `.<timestamp>.partial`
  and renamed into place when complete (files `0600`, directories `0700`). It never merges into the
  live data directory; move it out before serving it with `--data`. The offline CLI
  (`gorun backup-restore --dest`, or `gorun staging seed|promote --dest`) should target a new
  directory outside the data directory instead. Staging / promote-to-live is a second data dir
  plus cutover, not a shared database — see [STAGING.md](STAGING.md).

### Backup scheduling and disposable recovery previews

`backup.schedule` serializes with manual creation/restore. Policy saves and verified
success metadata use transactions; state in `backup.schedule.state` records attempt,
status and stage without raw errors. `backup.local.last_success` records the last
locally verified admin/manual or scheduled copy; CLI creation does not write that setting.
Scheduled and CLI retention use `backup.PruneVerified`, rechecking integrity before
counting a pair and leaving incomplete/unverified pairs untouched.

`backup-preview` restores to an owner-only OS temp tree, strips deployment settings,
credentials and queued jobs, supplies a fresh in-memory secret and local base URL,
and boots a fixed subset of built-in public-content modules without workers. It
ignores downstream module options and archived/environment credentials. A loopback
HTTP guard rejects writes/privileged routes, sanitizes HTML and enforces restrictive
CSP; cleanup happens after shutdown. It never mutates the original backup or serves
the live data tree. See [backup and recovery](BACKUPS.md).

### Migrations must be additive

A fleet upgrades one site at a time, and a rollback runs the previous binary on a database the new
one has already migrated. Migrations are forward-only (`internal/db/migrate.go`), so every migration
must leave a schema the previous binary can still boot and serve on:

- **Allowed:** `CREATE TABLE`, `CREATE INDEX`, `ALTER TABLE … ADD COLUMN` (nullable or with a
  default), triggers, and data backfills (`INSERT`/`UPDATE`).
- **Not allowed:** `DROP TABLE`, `ALTER TABLE … DROP [COLUMN]`, `ALTER TABLE … RENAME` (table or
  column), `DROP INDEX`, and table rebuilds (`CREATE x_new`, copy, `DROP x`,
  `RENAME x_new TO x`). Destructive cleanup ships at least one release after the code stopped
  using the object, in its own PR.
- **Never edit an applied migration.** Add a new file instead.

`TestMigrationsAreAdditive` (`internal/db/migratelint_test.go`) walks every
`migrations/NNNN_*.sql` in the repository and fails on those statements, ignoring comments and
string literals. A file that genuinely needs one goes in `internal/db/migration_allowlist.txt` as
`path/to/NNNN_x.sql # justification`. The justification should say why older binaries are still
safe (for example "keeps the column name, so older readers still work") or why the data involved
cannot matter, and the PR needs a reviewer's agreement. The test also fails on stale entries.

At boot, the migrator also checks the following. Neither check stops the boot:

- **Checksums.** `schema_migrations.checksum` holds the sha256 of each applied file. It is a
  nullable column; old binaries leave it empty and the next new boot backfills it. A WARN
  `applied migration changed since it ran (checksum mismatch)` with `module` and `name` means that
  file was edited after databases applied it. Revert the edit and put the change in a new
  migration.
- **Schema ahead.** A WARN `database has migrations unknown to this binary` lists the
  `module/name` rows this binary has no file for. Either a newer binary migrated this database
  (an expected, temporary state during a rollback, and safe only because migrations are additive)
  or a module was removed. A migration whose absence is a security risk (for example a new auth
  table that an old binary would ignore) must document that downgrade risk. Check this state
  without reading logs in the System Health `schema` check (`/admin/system/health`,
  `/api/v1/system/health`) or in `unknown_migrations` / `changed_migrations` of `gorun info`.

## Hooks

`hooks.Do(ctx, name, payload)` runs action subscribers synchronously; `hooks.Apply(ctx, name, v)` runs the
filter chain; `hooks.Emit(ctx, name, payload)` is asynchronous (a `hooks.event` job, 5 attempts) and reaches
the same action subscribers. Subscribe with `d.Hooks.Action(name, prio, fn)` / `d.Hooks.Filter(name, prio, fn)`
(lower `prio` runs first; both return a token for `Remove`). Emit must run after commit, never inside `db.InTx`.

Hooks fired by the core (from `Do`/`Apply`/`Emit` call sites under `internal/`):

| Hook | Fired with | By | Payload |
|---|---|---|---|
| `content.before_save` | `Apply` | content | the content input being saved |
| `content.after_save` | `Do` | content | the saved item |
| `content.published` | `Emit` | content | the item that went live |
| `content.deleted` | `Emit` | content | `{id, type, slug, hard}` |
| `taxonomy.term_saved` | `Do` | taxonomy | the term |
| `media.uploaded`, `media.deleted` | `Emit` | media | the media record |
| `menus.saved` | `Emit` | menus | summary |
| `menus.resolve_url` | `Apply` | menus, theme | a menu item whose URL is being resolved (theme subscribes to resolve content/term links) |
| `redirects.saved` | `Emit` | redirects | summary (also after a CSV import) |
| `seo.sitemap_entry` | `Apply` | seo | one sitemap entry (theme subscribes to set `Loc` to the permalink) |
| `theme.render.context` | `Apply` | theme | the `*theme.Context` before rendering |
| `site.form.submitted` | `Emit` | site | the contact form submission (stored in `form_submissions` first; payload carries attachment metadata only; site subscribes and, unless `forms.notify` is false, mails it to each of `forms.recipients` (else `contact.email`, else the first active admin) with the file attached up to 8 MB; records `delivery` sent/failed/off; audited `site.form.delivered`/`site.form.failed`) |
| `user.login` | `Emit` | auth | `{user_id, method}` |
| `system.health.checks` | `Apply` | admin | the System Health check list (commerce, wpmigrate, mcp and ext add checks) |
| `commerce.order.paid` | `Emit` | commerce | the order |
| `commerce.entitlement.activated` | `Emit` | commerce | the entitlement |
| `subscription.activated` | `Emit` | commerce | subscription is `active` or `trialing`; saas turns matching entitlements on |
| `subscription.past_due` | `Emit` | commerce | payment failed and access is in the grace window |
| `subscription.canceled` | `Emit` | commerce | subscription canceled, unpaid, or suspended after grace |
| `migration.item.imported` | `Emit` | wpmigrate | the imported item summary |

`admin.nav` and `api.response`, named in the design spec, are not fired in v0.1. System Health lists the
hook names that currently have subscribers.

## Jobs

Durable jobs live in the `jobs` table. `gorun serve` runs 2 workers and the cron scheduler; CLI commands
that boot without workers only enqueue. While a job runs, its worker refreshes `locked_at` every 30 s; at
startup and on every scheduler tick, running jobs with no `locked_at` or one older than 2 minutes are
requeued (without counting an attempt), so jobs abandoned by a crash are retried while jobs another live
worker is executing are left alone. A handler that keeps failing is retried with backoff up to its maximum
attempts, then marked dead (retry from **System → Jobs** or `POST /api/v1/jobs/{id}/retry`).

| Kind | Registered by | Max attempts | Purpose |
|---|---|---|---|
| `hooks.event` | hooks (kernel) | 5 | deliver an emitted event to action subscribers |
| `media.fetch_url` | media | 3 | download a media file from a URL (SSRF-safe) |
| `commerce.provision` | commerce | 8 | create the billing account and activate the entitlement |
| `commerce.suspend` | commerce | 8 | suspend a billing account (refund, expiry) |
| `commerce.magic_mail` | commerce | 3 | look up a customer and send the account magic link |
| `ext.deliver` | ext | 5 | deliver an action/event to an extension endpoint |
| `migration.step`, `migration.item`, `migration.advance` | wpmigrate | 3 | the import pipeline |

| Cron | Every | Purpose |
|---|---|---|
| `content.publish_due` | 30 s | publish scheduled items |
| `content.purge_trash` | 1 h | hard-delete items trashed more than `content.trash_days` (default 30) days ago |
| `auth.cleanup` | 1 h | remove expired sessions, stale login attempts and used or expired magic links |
| `mcp.approvals.expire` | 1 h | mark undecided MCP approvals expired |
| `wpmigrate.watchdog` | 5 min | fail items of a running migration that sit pending with no job, so the run advances |
| `commerce.expire` | 24 h | expire entitlements past `ends_at` and enqueue suspensions |
| `jobs.prune` | 1 h | delete finished jobs: `done` after `jobs.retain_done_days` (7), `dead` after `jobs.retain_dead_days` (30), jobs with an idempotency key after `jobs.retain_keyed_days` (90); System Health shows the job table row count |
| `backup.schedule` | 1 h | when `backup.schedule_hours` ≥ 1, create a local backup pair (secrets excluded) if due; then prune to `backup.retain` newest complete pairs when retain ≥ 1; no remote upload |

## Permissions and roles

Permissions are declared with `d.Perms.Declare(name, labels)`. The auth module declares the base set; other
modules declare (or re-declare with labels) their own.

| Permission | Declared by | Permission | Declared by |
|---|---|---|---|
| `content.view` | auth, content | `themes.manage` | auth, theme |
| `content.create` | auth, content | `extensions.manage` | auth, ext |
| `content.edit` | auth, content | `migrations.run` | auth, wpmigrate |
| `content.publish` | auth, content | `commerce.manage` | auth, commerce |
| `content.delete` | auth, content | `commerce.view_orders` | auth, commerce |
| `types.manage` | auth, content | `mcp.approve` | auth, mcp |
| `media.upload` | auth | `mcp.approve_self` | mcp (API key scope only; `*` does not imply it) |
| `media.delete` | auth | `system.health` | auth, admin |
| `taxonomy.manage` | auth, taxonomy | `api_keys.manage` | admin |
| `menus.manage` | auth | `redirects.manage` | redirects |
| `users.manage` | auth, admin | `settings.manage` | auth, settings, admin |

Seeded roles (`internal/auth/migrations/0001_auth.sql`, `0002_editor_redirects.sql`):

| Role | Grants |
|---|---|
| `admin` | `*` (every permission, including ones declared later) |
| `editor` | `content.view`, `content.create`, `content.edit`, `content.publish`, `content.delete`, `media.upload`, `media.delete`, `taxonomy.manage`, `menus.manage`, `redirects.manage` |
| `author` | `content.view`, `content.create`, `content.edit`, `content.publish`, `media.upload` |
| `contributor` | `content.view`, `content.create`, `content.edit` |
| `customer` | none (shop customers; refused by the admin gate) |

Custom roles (`system=0`) are created under **Settings → Roles** (`/admin/roles`) or
`/api/v1/roles` (`users.manage`). Slugs are immutable; bilingual `name_json` and the
permission set are editable. System roles cannot be deleted; the `admin` role’s `*` grant
is locked. Actors may only assign permissions they themselves hold (`CanGrant`). UI
permission changes and role deletes require recent password confirmation (`ReauthWindow`).
After grant mutations, `auth.ReloadRoles` refreshes the in-memory map used by `Can`.

## Adding a module

1. **Place it in the order.** Add the name to `kernel.BootOrder` (`internal/kernel/order.go`) after every module
   it imports, and add `yourmod.New()` at the same position in `Modules()` (`cmd/gorun/modules.go`). Only the
   orchestrator edits these two lists and `go.mod`. `Boot` rejects a list out of order, and
   `importorder_test.go` fails if non-test code imports a later module.
2. **Implement `kernel.Module`:** `Name()`, `Migrations()` (an embedded FS with `migrations/NNNN_name.sql`,
   applied in order and tracked per module in `schema_migrations`), `Register(d *kernel.Deps) error`. Implement
   `kernel.CatalogProvider` (`Catalogs()`, `catalog/*.json`) for ar/en strings.
3. **Publish a contract.** Put the public interface in `contract.go`, add a service name in
   `internal/kernel/services.go` (or a `ServiceName` constant in your package) and `d.Services.Set` it in
   `Register`. Consume earlier modules with `kernel.Get[T](d, name)`, never by importing a later module.
4. **Register everything in `Register`:** permissions (`d.Perms.Declare`), admin nav and widgets (`d.Admin`; `AddNavProvider` for entries that change at runtime, evaluated per request),
   routes (`d.Router.Admin/Public/Webhooks` with `auth.RequirePerm`), API operations (`d.API.Add(api.Op(…))`),
   MCP tools (`d.MCP.AddTool`), theme funcs and kinds (`d.Theme`), jobs and crons (`d.Jobs.Register/Cron`),
   hooks (`d.Hooks.Action/Filter`), health checks (filter `system.health.checks`). Nothing registers after
   boot.
5. **Follow the transaction rule:** inside `db.InTx` use only the `…Tx` variants; emit hooks after commit.
6. **Audit every mutation** (`audit.Record`/`RecordTx`) and return the standard envelope from API handlers.
7. **Test with real SQLite** (`apitest.Boot` or `kernel.Boot` on a temp dir), `go test -race ./...`, and
   update `docs/` (this file, `API.md` if you add operations, `MCP.md` if you add tools).


## Guided site profiles and local readiness

Builder starter runs store an optional profile in their existing settings JSON; no new database migration or public API is required. Profile validation precedes immutable run capture and page creation. Resumable preparation uses the captured profile, preserving independent edits. Description/hero metadata is saved on the draft homepage. Batch publication updates content, draft baselines, menu, front page, identity and nonempty profile settings in one transaction. The theme preview uses request-local logo settings and never mutates global branding.

Readiness is an authenticated GET with `settings.manage`, served by builder at `/admin/appearance/readiness`. It queries public published content (100-item cap) and raw primary menu references (200-link cap), rather than trusting resolved menus that hide draft targets. Local files use confined filesystem roots; external resources are marked unverified without HTTP requests. Identity/metadata, image/alt/sample text, SMTP configuration, URL configuration and backup-pair metadata have separate evidence rows. DB failures and partial inspection remain unverified; receipt, DNS/TLS, rendered routes and restore need separate runtime acceptance.

## Scheduled remote backups

Admin owns the opt-in `backup.schedule.remote_destination` policy. Its hourly
cron queues one `backup.scheduled` job (at most one queued/running), instead of
performing network I/O on the serial cron loop. The existing data lease and
backup mutex cover local snapshot creation, verification, bounded encrypted
upload and pruning. A remote failure records a separate warning while protecting the just-created
local pair; configured retention still bounds older verified pairs. Losing the
data lease after local success skips cleanup with a warning instead of recording
a false backup failure. No bucket retention/ACL is changed.
The job has one attempt; a successfully persisted upload warning completes it
without a dead-job alert. Provider uploads are retried with the next due
snapshot rather than creating duplicate pairs through queue retries. Job errors
that cannot be represented by a saved warning are generic and redacted. The API and
form share one transactional policy writer. No new modules or boot-order changes.
