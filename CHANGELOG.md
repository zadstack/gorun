# Changelog

All notable changes to GoRun are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow semantic versioning.

## [Unreleased]

### Added

- **System → Modules** (`/admin/system/modules`, `system.health`) and `GET /api/v1/system/modules`:
  a catalog of every built-in module, core service (forms, storage, backups, mail) and installed
  external extension with its function (ar/en), source, version, compatibility, declared
  dependencies and dependents, permissions, what it registered in this process (observed by the
  kernel: permissions, API operations, MCP tools, job kinds, crons, admin navigation and widgets,
  theme funcs and kinds, services published and looked up, hook subscriptions), activation state
  (booted, desired, pending restart, forced by config), configuration verdict and queued/parked
  jobs, plus actionable messages (dependency off and its consequence, parked work, restart needed).
  `PUT /api/v1/system/modules/{name}/activation` (`settings.manage`, audited `module.activation`)
  and the page's button switch an optional module for the next boot. See `docs/MODULES.md`.
- Kernel: `kernel.ModuleRegistration` on every `ModuleState` (what a module registered) and
  job parking for disabled modules: at boot the queued jobs of a module that is off are parked by
  its last known job kinds (`kernel.inventory.<name>.job_kinds`) with the reason
  `module <name> disabled`, never run, failed or deleted; enabling the module releases them
  unchanged. Accessors `hooks.Registry.Subscriptions`, `jobs.Queue.CronNames`,
  `jobs.Queue.QueuedByKind`, `ThemeRegistry.Kinds`, `AdminRegistry.NavIDs`, `ServiceRegistry.Names`.


### Changed

- Commerce, saas and membership are optional modules behind the existing `kernel.OptionalModule`
  gate, **enabled by default** so existing installs boot the same set of modules. They answer with
  the new `kernel.AddonEnabled`, which reads the site setting `addons.<name>.enabled` (JSON
  boolean; missing means enabled). `GORUN_ADDONS` / `[addons]` mode `disabled` now switches an
  optional module off (restrict only: `enabled` and `auto` leave the decision to the module); a
  mode set for a module that is not optional is logged and ignored. `gorun migrate` applies the
  same selection as `gorun serve` (`kernel.MigrateAllWithConfig`). While a module is off its
  public routes, API operations, job kinds, crons, admin pages, theme kinds and hooks are absent
  and its tables are kept; re-enabling reads the same data back.

### Added

- `docs/BOUNDARIES.md`: the core / theme / built-in module / external extension boundary map with
  the import graph (generated and checked by `internal/kernel/boundaries_test.go`), runtime
  contracts (services, hooks, job kinds, crons, routes, API operations, MCP tools, permissions,
  theme helpers), the audit findings and the `CoreModules` decision. The test also fails on an
  undeclared import between feature modules, on Go code under `themes/` or `patterns/`, and on a
  `pkg/` contract package importing `internal/`.

## [0.15.1] - 2026-10-22

### Fixed

- `GET /api/v1/site/info`: every `logos.*.url` value now carries provenance. When a logo is not
  configured, its media id no longer exists, the media module is absent, or the mark comes from
  the theme manifest, `url` reports the same source and reason as the logo itself instead of an
  empty value. Found during the 0.15.0 rollout; no change to site content, settings or logos.

## [0.15.0] - 2026-10-22

### Staged update lifecycle

- Replace the single deploy request with a persisted operation whose phases — compatibility,
  release verification, backup, activation, health and links, final acceptance — are recorded
  independently with status, timing, result, retryability and remediation. A requested or queued
  deployment is never reported as a completed update: activation passes only when the serving
  process reports the expected identity, and acceptance only after every required check passes.
- Add a durable data-directory lease shared by updates, backups and restores (`409` with the
  owning operation), a resumable lifecycle job with heartbeat fencing, and boot/cron reconciliation
  that continues an activation whose identity now serves and marks anything else `interrupted`.
- Add a trusted release-index source (`GORUN_UPDATE_SOURCE_URL`, digest-verified) and a deployment
  adapter contract with Coolify (existing operator configuration) and manual activation. On a health
  failure after activation, a compatible prior artifact is re-activated on the current data when no
  migration was applied; a database or media snapshot is never restored automatically.
- Document `GET/POST /api/v1/system/update` and `/api/v1/system/update/operations[/{id}]` in OpenAPI.

### Identity and appearance

- Add `site.logo_dark`, `site.logo_light` and `site.icon` to General settings, the settings API and
  the MCP settings tool, with the same media validation as `site.logo`. Themes may ship marks in
  `theme.json` `logos` (files under `assets/`); site settings always win. New template funcs
  `{{logo "dark"}}`, `{{themeLogo "icon"}}`, `{{brandImg}}` and `{{favicon}}`; every shipped theme
  header, footer and head uses them, keeping the existing markup when no variant is set.
- `GET /api/v1/site/info` resolves every logo variant with explicit precedence and `fallback`, and
  reports the site-served `/media/` path beside the storage URL so a logo that renders on pages is no
  longer reported unavailable when its storage URL is signed. Appearance now separates `declared`
  theme values from effective ones, lists `overrides` with provenance, and reports scheme, radius and
  custom CSS status; undecidable values stay `unavailable`.

### Capability evidence

- `GET /api/v1/site/capabilities` now separates available, enabled, configured, connected and
  operation-tested, with each piece of evidence bound to the configuration revision and expiring
  after 7 days (`stale` / `expired` keep the recorded result). Modules publish configuration
  verdicts with a reason and repair step (`system.capabilities.modules`); the mail service is listed.
- Add explicit, permissioned operation tests that run as isolated background jobs: S3 write/read/
  delete of one probe object, one tiny AI completion, one mail to the requesting operator
  (`system.capability_test`). Connectivity checks can be queued through the API and run on a
  12-hour schedule; MCP has no operation test by design. The storage new uploads use (managed
  destination, configured bucket or local disk) has its own operation test, and the media module
  reports its configuration from that storage.

### Background observations

- Add durable, revision-fenced observations for expensive or external checks: pages and read-only
  APIs serve the last saved result with its age and state; refreshes are bounded background jobs
  (timeout, limited retries, atomic deduplication, per-kind concurrency cap); results for edited or
  removed configuration are discarded; a failed attempt keeps the last success visible. The release
  index and billing-server reachability now use this path, so System Health never calls a server.
- Job workers only claim kinds with a registered handler; work of a disabled or missing module waits
  unchanged (`parked`) and is released when the handler returns. Health reports parked counts.
- Document `/api/v1/system/observations` in OpenAPI.

### Scheduled encrypted remote backups

- Allow explicit opt-in to upload verified scheduled backups to the active R2/S3
  backup destination; protect the new local pair while keeping configured
  retention bounded during upload failures.
  Keep the last fully acknowledged upload separately from the latest attempt,
  including an explicit warning when its local receipt could not be saved.
- Run scheduled snapshots/uploads in a deduplicated background job, and pause
  uploads when the selected destination is disabled or replaced.
- Add bilingual backup controls, remote failure warnings in System Health, and
  documented `GET/PUT /api/v1/system/backups/schedule` operations.

### Integrations hub

- Add Settings → Integrations: an encrypted Cloudflare API token with account selection and
  per-scope permission lamps (token verify, R2, D1, Workers) refreshed as background observations;
  paginated inventory of R2 buckets, D1 databases and Workers with an explicit incomplete state;
  GoRun Edge confirmed only through the manager identity check (association recorded as
  `workers_dev_host` or `operator_asserted`, never inferred from a name); binding an existing
  bucket as media or backup destination with separate prefixes; and an SMTP section reusing the
  mailer with a rate-limited, fully audited test message. Discovery never creates or modifies
  resources. Mail operation evidence is re-bound to the provider and reply-to settings, so saved
  mail evidence goes stale once after upgrading.

### Known limits in this release

- MCP profiles have no operation test: invoking a tool executes a remote action, so `tested` stays
  `false` for MCP and only `initialize` + `tools/list` connectivity is recorded.
- Modules other than commerce, media and mail do not publish module-level configuration requirements
  yet; the capability report says `module_publishes_no_requirements` rather than guessing.
- Staged updates through Coolify work for Docker-image resources only (pinned tag); git-built
  resources are refused. Schema/theme preflight against the target needs the release index to publish
  `migrations`/`themes`; otherwise it records `release_data: unknown`.
- Appearance reports declared and effective tokens with provenance; it never claims browser-computed
  CSS, and custom CSS makes effective values unavailable while the declared layer stays.

Existing installations keep data, settings, themes and saved checks; all schema changes are additive
(`jobs.parked_reason`, `observations`, `update_operations`, `update_phases`, `ops_leases`).
Publishing this release does not upgrade a running site process.

## [0.14.1] - 2026-10-21

### Edge

- Content filters apply status/language before the result cap and preserve Unicode-aware title/type search. `%` and `_` are literal characters. Searches stop at 100 matches; a bounded scan that cannot finish returns an explicit error instead of a false empty result.
- Creating or rotating the primary manager key redirects and shows the current key once. Reloading does not issue another key; a key rotated or revoked before the redirect is consumed is withheld, and logout clears its handoff cookie.
- Saving or checking Edge settings uses atomic writes. Connection checks read one consistent configuration and discard results if the saved origin or encrypted key changed while the request was in flight. Failures preserve the last stored configuration without reporting success.

Existing installations keep content, settings, credentials and themes; no schema migration is required.
Publishing this release does not update an existing Full site or deploy a Cloudflare Worker.

## [0.14.0] - 2026-10-21

### Shared core discovery

- Add public `/api/v1/site/info` with the running build, existing site identity/logo, supported locales,
  and active theme appearance declarations with provenance and explicit unavailable values. Keep
  the existing authenticated `/site` contract and downstream theme services compatible.
- Separate protected `/api/v1/site/runtime` details behind `settings.manage`; document both in the
  generated OpenAPI contract. Discovery does not alter site content, settings, themes or media.
- Add `/api/v1/site/capabilities` behind `system.health`, separating module availability/activation,
  local connection configuration, and saved connectivity evidence from operation verification.
  Preserve legacy probe records and bind new checks to the configuration revision without a SQL migration.
- Document follow-up acceptance criteria for staged updates and durable background checks, including
  runtime rollback without overwriting newer data with a pre-upgrade database.

Existing installations keep data, settings and themes. Publishing this release does not upgrade a
running site process. Background observation and staged runtime updates remain follow-up work.

## [0.13.0] - 2026-10-21

### Edge on Cloudflare

- Add a Deploy to Cloudflare button for the experimental Edge edition. Cloudflare clones `edge/`, provisions the D1 database and private R2 bucket from `wrangler.jsonc`, applies the Edge migrations, loads the synthetic preview, and deploys the Worker. The published address is the site, managed from `/admin` on that origin with the owner key. This does not install the Full binary.
- Shape the Edge owner admin as a miniature of the GoRun workspace: sidebar, content counts, status badges, and a split editor. Agents, uploads, and the rest of the Full dashboard stay out.
- Add content search, status, and language filters, status tabs, and the public link on a published editor. Add a Managers button that issues one primary key for GoRun. GoRun stores that key encrypted under Settings → Edge and checks `GET /api/v1/manager`. The check confirms identity only.

Existing installations keep data and settings. Publishing this release does not upgrade a running GoCloud/site process, and it does not install the Edge Worker onto an existing Full site.

## [0.12.0] - 2026-10-21

### Commerce launch checklist

- Add **Commerce → Launch store** (`/admin/commerce/setup`): read-only checklist for Stripe keys, webhook URL, billing servers and sellable provisioning products, with links to settings and docs. Store settings registration copy uses i18n catalogs (EN/AR).
- Seed three **inactive example products** on first boot (`example-provisioning`, `example-service`, `example-digital`) so operators can learn each product kind before going live.
- Add **Orchestrator** agent role template (7th example) and an inactive **Example: Orchestrator** workagent profile per admin on first boot.

### Stripe subscriptions

- Sell a plan as a Stripe subscription or a one-time payment from the same store. Checkout reuses
  the Stripe customer, records subscription and invoice rows, and lets billing status turn access
  on, into a grace period, or off. Customers open the billing portal from `/account`. Admins refund
  a PaymentIntent from the order screen. Test and live webhook URLs are separate, and plugins can
  subscribe to `subscription.activated`, `subscription.past_due` and `subscription.canceled`.

Existing installations keep data and settings. Publishing this release does not upgrade a running GoCloud/site process.

## [0.11.0] - 2026-10-20

### Pattern plugins (site layouts)

- Adds a declarative **pattern** layer (`patterns/`, `internal/pattern`, `pkg/patternkit`) so opinionated page layouts sit above theme identity and below site overrides (`draft → override → pattern → theme`) (#105).
- Ships four first-party starters: `editorial`, `company`, `showcase`, `guidebook` — selectable under **Appearance → Patterns** independently of Themes (English and Arabic admin copy).
- Setting `site.pattern` records the active layout; clearing it restores the theme’s own templates. Pattern assets are served at `/patterns/<slug>/assets/`.

### Theme catalog as identity systems

- Treat ship-with themes as **identity systems** (type, color, spacing, chrome), not
  site-pattern packs. Display names: Classic, Modern, Minimal, Serif, Docs (Arabic:
  كلاسيكي، حديث، بسيط، سيرف، توثيق). Slugs stay `default`, `business`, `portfolio`,
  `magazine`, `docs` — no install migration. Full magazine/business/portfolio *layouts*
  move to pattern plugins (#104).
- Refresh identity tokens across the three looks (editorial, clean-technical, warm-local)
  and regenerate admin `screenshot.png` previews. Update starter / launch copy and docs.

### Canvas MCP/REST read parity

- Add grant-scoped `list_canvases` and `get_canvas` MCP tools plus resource `gorun://canvases/{id}` (#91).
- Mirror the same reads on `GET /api/v1/canvases` and `GET /api/v1/canvases/{id}` for REST keys (agent keys remain refused on `/api`).
- Agent keys see only boards with an explicit read grant and only grant-allowed cards/edges. No public anonymous canvas routes.

### Agents: Updates Researcher recipes

- Editable connection recipes for the Updates Researcher story (#100), plus save-failure /
  keyboard-overflow fixes (#103). README shows the working story (#102).

### Canvas card and note presentation

- Refine all canvas card types and provide a read-only full-note reader (#99).

### Backup inspect readiness clarity

- Distinguishes `local_ok` (local checksums and database integrity passed) from `review_required` (site-specific cutover warnings such as external media) and `blocked` (restore refused) (#94).
- Moves expected recovery requirements (secrets, themes, stopped writes, remote copy) into a `cutover_checklist` so a healthy local backup is not framed as a failed preflight.
- Updates English and Arabic admin inspect copy and operator docs accordingly.

### Site identity on first-run setup

- Collect site name, optional tagline and language during `/admin/setup`, and require a site name on the workspace first-run path when it is still empty (for example after CLI admin creation). Public pages no longer stay on the generic GoRun title until Settings (#96).
- Reject clearing `site.title` to empty in General settings / MCP `update_settings`. Site readiness fails when the title is missing. `gorun start` first-login onboarding is unchanged.

### Admin navigation drag-and-drop

- Admin `/admin/navigation` supports drag-and-drop reordering of sidebar groups
  and items (with existing arrow / Alt+arrow keyboard fallbacks), persisting
  through the current `item_order` / `group_order` preferences (#98).

### Content locale follows the editor, not only the site default

- New content defaults to the request/admin UI locale when one is present, instead of always inheriting `site.locale`. An English admin drafting on an Arabic-default site stores `en` unless they choose otherwise (#90).
- The create editor shows an explicit **Content language** picker (defaults to the admin chrome language). REST/MCP create without `locale` follows the same request-locale-then-site-locale rule.

### Performance

- Cache settings reads in process memory (invalidate on Store writes and when
  SQLite `data_version` advances, so out-of-band SQL updates are visible) and
  memoize `setting` / `menu` template lookups per render (#95).
- Local `make loadtest` / `tools/loadtest` now boots the spawned serve process
  with `GORUN_ENV=prod` by default (cached themes), matching shipping behaviour.
  Use `-serve-env dev` for the previous hot-reload measurement path (#92).

### Security

- Refresh `SECURITY.md` supported versions for the 0.11 line (0.11.x / 0.10.x / 0.9.x).
- Harden `gorun start` LAN preview: treat locale-prefixed privileged paths the same as
  unprefixed ones, and keep private intake (`/reports`), public forms (`/forms`) and
  customer `/start` on loopback only (alongside admin, API, MCP, accounts and checkout) (#89).
- Redact locale-prefixed magic-login paths (`/en/auth/magic/…`, `/ar/auth/magic/…`) in
  request logs the same way as `/auth/magic/…`.

### Documentation

- Refresh the root README: concise product framing, quick start, identity themes
  versus pattern plugins, grant-scoped canvas reads, and a lightweight animated
  SVG hero (`assets/branding/readme-hero.svg`) (#106).
- Bilingual product screenshot refresh for the README campaign story (#93).

Existing installations: theme slugs and settings preserved. Selecting a pattern is opt-in
(`site.pattern`). Display names and screenshots change only; layouts move to Appearance →
Patterns. Publishing this release does not upgrade a running GoCloud/site process.

## [0.10.0] - 2026-10-20

### Reviewed canvas workflow suggestions

- Add optional workflow suggestions to the canvas list using the existing writing AI configuration. Show the recipient provider, model and host, plus the exact instructions and site-context prompt, before a separate approval and confirmation. Previewing never contacts the provider.
- Bind each expiring, owner-scoped, single-use review to the actual configuration snapshot and revision. Editing and restoring settings cannot revive old approval. A changed context or writing configuration requires a fresh review. Recheck the active user’s current writing permission before generation and accepting output; discard output if that permission is lost, or context or configuration changes.
- Use bounded, race-safe caches and idempotent Apply to create private boards. Suggested boards start without a bound executor, enabled schedule, grants or automatic execution. Existing canvas search, presets and private templates remain available.

### Honest extension installation methods

- Group the extension inventory by its recorded installation method: server directory, uploaded ZIP, or legacy method not recorded. Show the same method on extension details in English and Arabic, including mobile layouts.
- Preserve existing records, subscriptions and delivery history during the additive schema upgrade. Existing installations remain unknown rather than being labeled bundled or trusted. New installations record the method actually used, including a retained ZIP package later installed from a directory.

This release includes the independently reviewed repairs in #78 and #82. Installing it does not configure a provider, grant tools, run an agent or publish content automatically. Existing installations and accounts remain preserved.

## [0.9.0] - 2026-10-19

This release improves daily administration and adds inspectable agent role templates.

- Colored, labeled navigation categories and private Quick access shortcuts in the main sidebar, rechecking current permissions and visibility without changing roles. Legacy identity pins map to the authorized unified Site design entry.
- A unified Site design workspace for draft preparation and visual identity, with separate explicit save/activation, preserved deep links and a local identity sample.
- A refreshed default reading theme with genuine starter examples, a featured article, recent posts and guide links; existing authored content and stored identity are preserved.
- A work-first dashboard, explicit customization, clearer content publishing actions, page hierarchy, media list/details and guided website setup.
- Shared staff navigation for agent management and app subscriptions, with customer-facing routes and permissions preserved.
- A detailed MCP/AI/S3 connection directory with searchable columns, saved credential presence and historical test results. Browsing the directory never probes a provider.
- Six bilingual agent teaching roles, each combining five bounded skills. Team members can inspect the examples; authorized owners can prepare an unsaved bundle for an existing agent and explicitly review/save it.
- Clearer provider forms, role grants and before-save permission comparison, account audience filters, diagnostics and backup outcomes.
- Per-kind migration queue summaries distinguish completed items, preview plans, pending, failed and skipped items. Preview counts are not reported as imported content; completion remains separate from final validation.

Existing data and accounts are preserved. Agent templates do not grant permissions, install external clients, select a model, enable MCP tools, publish content or automate infrastructure. Connections still require explicit setup and testing. GoCloud operators update their running application manually.

GHCR access depends on package permissions. Public installer and binary archives do not require registry login.

## [0.8.0] - 2026-10-19

### Reviewed MCP tool execution

- Let an owned, named OpenAI-compatible managed text agent use an explicitly
  selected set of MCP tools. Discover and pin the server configuration, catalog
  and bounded input schemas; existing bundles stay disabled by default.
- Pause every proposed call for the owner's review of its destination, tool and
  exact arguments. Require recent authentication, reject replay and claim the
  attempt before making the call. Return its bounded text result to the model
  and save the final answer as a private canvas note.
- Preserve limits and consent across pauses and restarts. Revoking and restoring
  permissions cannot restore an old approval. Recheck source, graph, profile,
  model, bundle and owner authority at execution and result boundaries; never
  automatically repeat a call with an ambiguous external outcome.
- Restrict this increment to manually reviewed text runs, with at most three
  tool calls and four model turns. Scheduled and incoming-webhook runs cannot
  use a tool-enabled policy. Executable skill packages, unattended tool use and
  automatic publishing are not added.

### Separate Edge owner administration

- Add an optional, default-off owner interface to the standalone Workers/D1/R2
  edition. Generate its owner key through an offline operator command, store
  only the hash and rotate access without replacing content. Recovery revokes
  existing sessions; there is no public bootstrap or reset endpoint.
- Edit private drafts separately from published articles, review the exact saved
  version before publishing, restore saved revisions into drafts and export
  content without authentication state. Guard writes, revisions and audit
  records atomically against stale versions and expired or revoked sessions.
- Support compact English and Arabic owner pages, including mobile. Require a
  pinned origin, secure production cookies and same-origin CSRF protection.
  Keep public reads site-scoped, escaped and restricted to published content
  and explicitly public R2 records.
- Edge remains a separate experimental edition, outside the Full binary. Media
  upload, Full themes and agents, manager pairing, a supported public deployment
  template and an Edge benchmark are not part of this increment.
- Verify the owner flow through actual HTTPS on an isolated Cloudflare Worker,
  including offline initialization/recovery, private drafts, exact publication,
  Arabic mobile, export, unpublish and session revocation. Recheck public D1/R2
  boundaries and revoke temporary owner access, remove the test secret and return
  administration to disabled after acceptance. Record the source and limits
  without credentials; this is functional validation, not a performance result.

Publishing a release does not update an existing running installation.

## [0.7.0] - 2026-10-19

### Agent skills and connection bundles

- Manage a private bundle for an owned, active agent key: bounded inline skill
  instructions and reference text, plus multiple version-pinned MCP connection
  profiles. Review the combined effective instructions in the canvas before
  execution. Skills become part of real managed-text and external-agent requests.
- Recheck the current bundle version and authority at execution/result boundaries.
  Bundle changes retire previous incoming-event and schedule consent; restoring
  old text does not restore previous consent. Keep ownership, permission, size and
  configuration-version checks active when a saved profile changes or disappears.
- Show the saved discovery result and timestamp separately from live availability.
  Export a manifest without stored credentials or endpoint URLs for an external
  client to configure explicitly. Attaching MCP profiles does not grant every tool,
  install server packages or create an automatic managed-model tool loop.

### Current interface screenshots

- Refresh the English/Arabic README campaign story and admin screenshots for
  grouped navigation, contextual page links and the opposite-side agent drawer.
  Include real desktop and mobile captures using isolated synthetic data, with
  configured status distinguished from an online external client.

### Separate Edge read-only proof of concept

- Add a locally runnable Workers implementation with its own D1 schema and
  explicitly public R2 media. Scope reads to one site, expose published content
  only, escape text output and reject write/admin routes. Keep the prototype
  separate from the GoRun Full database and release binary.
- Test the Worker through the actual local workerd runtime with D1/R2 bindings.
  Remote Cloudflare deployment remains unverified; this is not a complete Edge
  CMS, manager pairing, a deploy template/button or an Edge benchmark result.

Existing sites require an operator deployment to receive application changes.
Publishing this release does not upgrade a running installation.


## [0.6.0] - 2026-10-19

### Admin navigation

- Group the admin sidebar into Home, Content, Website, Canvas & agents, Inbox
  and Settings, with Store shown only when permitted commerce tools are available.
  Put related pages in contextual category/page tabs; nested editors keep their
  parent section selected. Preserve existing URLs, permissions, personal visibility,
  dashboard pins and saved navigation order. Unknown extension groups remain
  discoverable under Settings.
- Keep the compact navigation usable in Arabic/RTL and English/LTR, on small
  screens and without browser storage. Contextual page links also work without
  JavaScript; dashboard pin buttons are available beside the page tabs.

### Agents rail and details

- Add an opposite-side rail for agent/model cards on the current user's own
  canvases. Open a compact drawer with Integrations, Instructions and Defaults
  tabs showing the real saved model/mode, effective instructions, card identity
  and output type. Links open the original canvas card for configuration/review.
- Keep card, key and model identities distinct. Exclude source context and
  credentials, reject foreign/revoked/expired agent keys and recheck current
  preparation/grants. Opening the panel never dispatches work. Configured does
  not mean online: external agents still need their clients, and Skills/MCP
  resource bundles are not assembled or verified by the drawer.
- Hide the entire agents surface in Simple CMS mode. Support RTL/mobile,
  keyboard tab navigation, modal focus containment/return and safe sidebar
  handoff. Discard late responses after closing/leaving, and preserve logical
  focus when refreshed cards change or disappear.

### Validation and upgrades

- Add bilingual browser and permission/ownership regressions, including dashboard
  pin/unpin, runtime content types, small-screen lazy previews, unavailable
  browser storage and delayed drawer responses. The combined interface passed
  27 focused Chromium checks and race tests before merge.
- Existing installations need a manual deployment to receive these UI changes;
  publishing this release does not upgrade a running site.

## [0.5.0] - 2026-10-19

### Installation and server operations

- Add guided Linux server setup using verified public release archives, a local
  non-root runtime image, persistent data and Caddy HTTPS. Validate domain/DNS,
  ports, proxy trust, certificates and application health before reporting success.
- Resolve the latest public stable release for new server installations and record
  its numeric version and immutable image identities. An explicit `--version`
  pins a release; `--resume` keeps the original manifest release without resolving
  latest, downloading, pulling or rebuilding. API errors never silently select an
  older release. Add a manually dispatchable isolated Linux acceptance workflow.
- Add an optional admin update page for a server-configured Coolify resource,
  with recent authentication, a verified backup, audited deployment requests
  and duplicate-request cooldown. Existing sites need a manual deployment before
  this page is available; no running customer site is upgraded automatically.
- Preserve sidebar scroll position across navigation/reloads with a safe fallback
  when browser storage is unavailable.

### Canvases and agent workflows

- Add workflow, grid and list views, collision-aware card arrangement, clearer
  compact card tools and separate connection ports. View-only changes preserve
  the saved graph; refresh the README with a three-step synthetic workflow story.
- Add one compact dashboard activity board combining the current owner's agent
  results with permission-scoped webhook delivery metadata. Exclude webhook URLs,
  credentials, payloads and raw errors; refresh does not run work again.
- Convert a reviewed text result into a new draft using current source, owner and
  permission checks. Publishing remains a separate action. Save private structural
  workflow templates without copying prompts, notes, outputs, credentials or runs.
- Show configured agent identity/model/mode alongside permission context. An
  external agent still requires its client to connect and execute; configuration
  is distinct from verified readiness.
- Add opt-in signed commands for individual agent/provider cards. Owner review,
  recent authentication and CSRF protect configuration; encrypted signing secrets
  appear once. The receiver accepts only the configured command, verifies HMAC and
  freshness, deduplicates deliveries and enqueues atomically within bounded rates.
  Grant/key/role/provider or relevant graph changes require fresh consent; disabled
  or stale rules cannot append results after an in-flight call. Accepted events
  are audited without prompts, payloads or secrets. External clients remain in
  `waiting_agent`; no automatic publishing or arbitrary shell/tools are added.

### Bundled themes

- Give the five bundled themes descriptive category names and distinct visual
  treatments. Replace standalone demo brands in the theme gallery and README
  previews with generic sample identities. Keep existing theme IDs, saved site
  titles, logos and identity settings compatible; custom palettes take precedence.

### Evidence and boundaries

- Publish reproducible GoRun/WordPress/MariaDB article-serving measurements with
  raw evidence, a validated summary and an executed notebook. Astro static output
  is a separate delivery reference. The measured GoRun source remains the recorded
  0.4.0-era checkout, not a benchmark of the new canvas UI.
- Keep Cloudflare Edge labelled in development: permission contracts are not a
  runnable Edge CMS or a deployable template. Combined Skills/MCP resource bundles
  remain a future design and are not presented as automatic agent provisioning.

## [0.4.0] - 2026-10-19

### Installation and documentation

- Add a copy-and-paste macOS/Linux installer with Intel/ARM detection, HTTPS
  release downloads, SHA-256 verification, atomic user-local installation and
  direct startup without Go, sudo or manual PATH edits. Preserve existing
  installations on failed verification or when an old release lacks `start`.
- Put the English quick start first and preserve a complete linked Arabic README.
  Include hands-on plugin and theme development tutorials.

### Workspace and foundations

- Add Simple CMS and Agents + Commerce workspace modes to tailor the admin UI.
- Add portable, site-scoped Edge manager capability contracts and regression tests;
  this is an authorization foundation, not a hosted Edge runtime.
- Clarify API key and webhook command help and scope errors.

### Automatic backup and safe recovery

- Add daily/weekly local scheduling choices, atomic policy saves, recorded verified
  success and storage usage, persistent failure/retention alerts and System Health checks.
- Fix relative/URI-character SQLite paths in read-only backup inspection.
- Verify local admin/CLI backups before success or pruning. Retention counts only
  freshly inspected pairs; corrupt/legacy/partial copies cannot displace good copies.
- Add `gorun backup-preview` with a disposable sanitized restore, fresh secret,
  fixed public-content module allowlist, no workers/extensions/integrations,
  loopback/Host checks and read-only browser isolation. Source backups and live data
  remain unchanged; final restore/cutover still uses a separate new directory.


### First-login setup and diagnostics

- Require the auto-generated administrator to replace the temporary password and
  choose email, site name and site/dashboard language before admin or authenticated
  API/MCP access. Save all fields atomically, rotate the browser session and revoke
  old sessions, keys and magic links; completed/existing configured accounts skip it.
- Add read-only `gorun doctor [--json]` with configuration, permissions, SQLite,
  migration drift, module activation, admin, theme and local port checks plus recovery
  advice; do not initialize a site, generate secrets or apply migrations.

### Operations

- Local load harness (`tools/loadtest`, `make loadtest`): boots a real SQLite
  `gorun serve`, seeds published posts, and reports p50/p95/p99 latency, error
  rate, throughput, and approximate server RSS/CPU under N concurrent readers
  (optional light write mix). Docs: [docs/LOADTEST.md](docs/LOADTEST.md). Not
  wired into CI (avoids flaky host-noisy gates).

### Local startup

- Add `gorun start` with SQLite migrations, bundled default-theme provisioning,
  atomic first-admin creation and a 256-bit password printed once. Print local
  and private IPv4 LAN preview URLs and open the local admin in a browser.
- Keep administration, accounts, API and writes on localhost; validate Host and
  peer addresses, ignore forwarded identity and discard LAN credentials.
  `--local-only` disables LAN; `--no-browser` supports headless use.
- Advertise/open the IPv4 loopback address to match the listener and avoid
  localhost resolving to an unrelated IPv6 listener on the same port.
- Add a durable `admin` login alias for newly bootstrapped first administrators,
  preserving email login and shared throttling. Existing users are untouched.


## [0.3.0] - 2026-10-19

Third tagged release: theme quality brief (#33–#37) and compact admin/default density
(#32) after v0.2.0.

### Changed

- Compact density (#32): tighter admin shell padding, gaps, and control heights plus denser
  default-theme layout/type tokens; other ship-with themes unchanged; 44px touch targets kept.

### Documentation

- `SECURITY.md` supported-versions table lists `0.3.x` and `0.2.x` (Arabic summary matches);
  `0.1.x` is no longer supported.

### Themes

- Starter content honesty (PR5 / #37): company starter omits testimonials (no invented
  quotes/clients); illustrative project blurbs stay labeled as concepts; regression
  tests lock theme lang (no em dash / lorem), starter copy honesty, and catalog home
  OG + indexable (never `robots: noindex`). Theme versions `1.5.0`.
- Performance (PR4 / #36): site identity and builder brand tokens ship as cached
  `/site/identity.css` and `/site/builder-identity.css` (hash `?v=`) instead of per-request
  inline `<style>`; default 500 page drops `style=` attributes; theme scripts carry CSP
  nonce; docs fonts trimmed to ≤4 weights; catalog themes `1.4.0`.
- A11y + RTL hardening (PR3 / #35): shared `a11y.css` (after tokens) across catalog themes with
  contrast-safe `--muted`, `:focus-visible` rings (light accent / dark `--focus-ring`), 44px tap
  targets (prose inline links excluded), visible skip links, `prefers-reduced-motion`, Arabic
  `letter-spacing: 0` + no mono on body/dates, mirrored go arrows. Headless §6 acceptance audit in
  Playwright (`theme-acceptance.spec.ts`: home + 3 inners @ 1440 & 390). Theme versions `1.3.0`.
- Page anatomy (PR2 / #34): shared `anatomy.css` via `parts/shell.html`, reusable hero /
  section-head / list-row / closing-band components, default header with filled CTA + 44px
  mobile `<details>`, facts `<dl>` from real content/product counts, hairline cards with CSS
  cover fallback, numbered archive rows, footer `aria-labelledby` columns, and reading rhythm
  (~18px / 1.9 / 760px) on inner templates. Catalog themes keep personality CSS; versions bump
  to 1.2.0.
- Shared base design tokens (#33): `themes/_base/tokens.css` (served as
  `default/assets/tokens.css`) load first in every catalog theme (`default`, `business`,
  `magazine`, `portfolio`, `docs`). Themes override personality tokens; default migrates off
  hard-coded `:root` hex/spacing.
- Default RTL ship-blockers: dates no longer force LTR on `<time>` (isolate + `<bdi>`), Arabic
  headings keep `letter-spacing: 0` (home + editorial shell), magazine RTL drops Latin-only
  Georgia fallthrough, `/shop` product grid uses real CSS columns (3 / 2 / 1).
- Shipped themes still use precompiled Tailwind only; the Play runtime part stays opt-in for
  custom children (no CDN).

## [0.2.0] - 2026-10-18

Second tagged release after the CMS hosting gap sequence, launch-in-core path, admin/theme
polish, and operator runbooks landed on `main` (#16–#30).

### Added

- Productized staging / promote-to-live for single-process SQLite: `gorun staging seed`
  and `gorun staging promote` wrap backup + confined restore into a new `--dest`
  (never merges into a live data dir), with operator next-steps. Runbook:
  `docs/STAGING.md`.
- Embedded launch overlay stylesheet at `/static/launch.css` (`web/launch/launch.css`),
  served by the binary so Coolify/Traefik file mounts for that path can be removed
  after upgrade (see `deploy/coolify.md`).
- Launch journey in core: `/start`, `/start/login`, `/start/verify` with site-title
  titles (em dash) distinct from staff `/admin/login`; embed `/static/launch-polish.js`
  (title/validation agent). Drop Traefik mounts for that script too.

### Changed

- Admin UI polish: light non-flat workspace atmosphere, clearer sidebar/topbar hierarchy,
  brand-first auth panels (login/setup/magic/reauth), shared empty-state treatment on
  high-traffic lists, and quieter flash/form focus rhythm — still Zad + green selection, RTL-safe.
- Public theme polish: `default` home is a full-bleed brand-first band (pill featured badge
  removed); shared `home-composition` intro is edge-to-edge; light focus/empty/hero polish on
  business, portfolio, docs, and magazine (personalities unchanged). Themekit doctor still green.

### Themes

- Shared catalog contract in `pkg/themekit` (`ValidateDir` / `ValidateCatalogRoot`): required
  templates (via inheritance), own `lang/en.json` + `lang/ar.json`, `rtl` + `identity` supports,
  screenshot, sections/identity/builderStyle head, token selector, no third-party script CDNs.
  Documented in `docs/THEMES.md` and `docs/THEME-CONTRACT.md`; covered by ship-with Go tests.
- `gorun theme doctor [slug] [--json] [--warnings]`: filesystem contract checks via the same
  `pkg/themekit` validators (no database). Soft `{{t}}` lang-key scans are opt-in warnings.
- Ship-with uplift: `business` and `portfolio` gain bilingual lang files; business/portfolio
  comments and portfolio contact forms no longer depend on Tailwind utilities; full-bleed home
  heroes for business, magazine, portfolio and docs (each keeps its existing personality); docs
  tokens move under `:where(html[data-theme="docs"])`. E2E theme project smokes
  magazine/portfolio/docs home.

### Added

- Custom roles UI and REST API: Settings → Roles (`/admin/roles`) and `/api/v1/roles`
  (`users.manage`) create bilingual packs with explicit permissions on the existing
  `roles` / `role_permissions` tables. System roles stay undeletable; Administrator `*`
  is locked; actors may only grant permissions they hold; UI permission changes and
  deletes require recent password confirmation.
- Optional Prometheus text scrape at `GET /metrics` when `GORUN_METRICS=true` (stdlib exposition:
  HTTP status-class counters, job gauges including dead/failed, backup last-success timestamp).
  Optional `GORUN_METRICS_TOKEN` Bearer; off by default. Operator guide: `docs/OBSERVABILITY.md`.
- Local backup schedule + retain-N: System → Backups settings (`backup.schedule_hours`,
  `backup.retain`) drive hourly cron `backup.schedule` (secrets still excluded; no remote upload).
  `backup.Prune` keeps the newest N complete local pairs; `gorun backup --retain N` for host
  cron / Coolify `docker exec`. Manual create and encrypted S3 upload unchanged.
- `gorun backup-restore --input <stamp>.manifest.json --dest <new-directory-path>` restores a local
  backup pair into a destination that must not already exist (inspect preflight; never merges into a
  live data dir).
- Operations → Backups → Inspect / restore: bilingual preflight and confined restore under
  `<data>/restores/<stamp>/`, gated by recent password confirmation and `system.backup`.
- Restores are staged in a hidden `.<name>.partial` sibling and renamed into place only after the
  database and archive checksums verify, so an interrupted restore never looks complete; a leftover
  partial directory is reported on the inspect page and blocks the next restore until removed (admin
  can discard it with the same permission and reauth gate as restore).
- Restored files are always `0600` and directories `0700`, whatever modes the archive records.
- The inspect page explains each preflight error and warning in English and Arabic (the code stays
  alongside for support).
- Page builder layout history: `builder_revisions` stores save/publish/reset/restore snapshots
  (default keep 25 via `builder.revisions_keep`). Admin lists history with preview and
  restore-to-draft (restore requires recent password confirmation). REST and MCP expose
  list/get/restore; MCP restore is sensitive. Public HTML is unchanged until publish.

### Documentation

- Upgrade / rollback runbook (`docs/UPGRADE.md`): tag → binary or GHCR/Coolify image → migrate on
  `serve` → smoke → rollback with prior artifact + pre-upgrade backup pair + original secret key.
  Cross-linked from install, production, troubleshooting, Coolify §8, and the docs index. System
  Health and `/api/v1/system/health` expose the binary build as `version` (read-only; compare to
  GitHub Releases — no in-app updater).
- Coolify / Docker HTTPS acceptance checklist (`deploy/coolify.md` §9): discover trusted-proxy CIDR,
  verify cookie `Secure`, default HSTS (`max-age=31536000`) and opt-ins, and probe `/healthz` +
  `/readyz` from outside the container — linked from `docs/PRODUCTION.md`, `docs/TROUBLESHOOTING.md`,
  `docs/GORUN-INSTALLATION.md`, and `docs/README.md`.
- Operator docs expansion: `docs/README.md` index; `docs/TROUBLESHOOTING.md`; install/upgrade from
  release tags (binary + GHCR) in `docs/GORUN-INSTALLATION.md`; HTTPS / proxy / HSTS and restore
  cutover in `docs/PRODUCTION.md`; Coolify restore + upgrade + HSTS in `deploy/coolify.md`; bilingual
  admin and first-site / theme-switch paths in `docs/ADMIN_WORKSPACE.md` and `docs/SITE_BUILDER.md`;
  MCP operator overview in `docs/MCP.md`; README documentation tables link the new index.
- `docs/GORUN-INSTALLATION.md` no longer describes an old MIT license or a "source-available" policy:
  it states `AGPL-3.0-only`, as LICENSE, NOTICE and README do, and points to the public repository and
  releases. CONTRIBUTING names the SPDX identifier.
- README: new screenshots (1440×900 at 2×, fictional demo content) under each pillar in both languages
  (content list, editor, agent keys, approvals, a canvas with an agent result, the page builder, and the
  default theme on desktop and mobile), a `docker run` example for the GHCR image, links to the v0.1.0
  release, and short notes on precompiled theme CSS, trusted proxies, HSTS, backups and the DCO check.

### Fixed

- Job workers no longer leave a job `running` when shutdown cancels the context while the job is being
  claimed: the claim itself is not cancellable, so a claimed job is always finished or requeued (it
  previously waited for stale-job recovery). This also fixes the flaky `TestStartRequeuesStaleRunningJobs`.
- Provider truthfulness: System Health no longer marks a stored Stripe secret key or webhook secret as
  `ok` (both are `warn` until checkout/delivery are verified elsewhere). Content Studio, AI writing, and
  connection admin copy state that save or list/discovery probes do not prove generation works.


## [0.1.0] - 2026-10-18

The first tagged release: a multi-arch image on `ghcr.io/zadstack/gorun` and binary archives for
linux and darwin (amd64, arm64) with checksums.

### Release

- `.github/workflows/release.yml` runs on `v*` tags: it builds and pushes `ghcr.io/zadstack/gorun`
  for linux/amd64 and linux/arm64 (tags `X.Y.Z`, `X.Y`, `latest`; `VERSION` from the tag; OCI labels
  for source and license), checks that `gorun version` in the image matches, builds the binary
  archives with `SHA256SUMS`, and creates the GitHub release from this file. Pull requests that change
  the Dockerfile or the workflow run the builds without publishing.
- `deploy/Dockerfile` cross-compiles on the build platform (`TARGETOS`/`TARGETARCH`) and carries OCI
  source and license labels.

### Media privacy

- New JPEG, PNG and WebP uploads are stored without camera metadata (GPS, device serials, names,
  XMP/IPTC, comments, JPEG trailers such as HDR gain maps). Stripping is lossless; ICC profiles and
  the JPEG EXIF Orientation are kept. Setting `media.strip_metadata` (default on) turns it off.
  Existing files are unchanged.
- Variants of rotated photos (EXIF orientation 2–8) are now generated upright, and the recorded
  width and height are the displayed ones.

### Fixed

- A process that runs job workers (`gorun serve`, `gorun mcp --jobs`, `gorun reindex` without
  `--no-jobs`) now holds an exclusive OS lock on `<data>/gorun.lock`; a second one on the same data
  directory refuses to start and names the holder. The lock is released on shutdown or if the
  process dies.
- Startup no longer requeues every running job. Workers refresh `locked_at` every 30 seconds, and only
  running jobs whose heartbeat is older than 2 minutes are requeued, at startup and on every scheduler
  tick, so jobs still executing elsewhere are never run twice.
- Forwarding headers from a peer outside `GORUN_TRUSTED_PROXIES` are logged as a warning (first
  occurrence, then at most hourly), since behind an unlisted proxy every visitor shares its address.
- A data directory whose path contains `?`, `#` or `%` opened the wrong database file and silently
  lost the WAL, foreign-key and busy-timeout pragmas; the SQLite `file:` URI is now percent-encoded
  (backups too).
- The checkout result page loads the embedded HTMX instead of an unpinned jsDelivr copy.
- WordPress migration render checks no longer disable the private-network block because of the
  admin-editable site base URL; they reach a private address only when it matches the configured
  `GORUN_BASE_URL` (or `GORUN_ALLOW_PRIVATE_NET` is set).
- `HEAD` requests to GET routes (`/admin/setup`, `/healthz`, …) answer like GET without a body instead
  of 405.
- An MCP approval decided with an API key now requires that key's owner to differ from the owner of
  the request; one admin with two keys can no longer approve their own calls. Single-admin sites can
  opt out with `GORUN_MCP_SAME_OWNER_APPROVAL=true` / `mcp_same_owner_approval`. Approving in the admin
  UI is unchanged.

### Changed

- **Default theme CSS is precompiled.** The default theme (and `docs`) links `themes/default/assets/tailwind.css`
  (12.4 KB, 3.3 KB gzipped) instead of loading the in-browser Tailwind compiler (407 KB, 127 KB gzipped)
  on every page. `make css` rebuilds it with the pinned, checksum-verified standalone CLI; CI fails when it is
  stale. Custom themes that need the runtime add `{{part "tailwind-runtime" .}}` to `parts/head-extra.html`.
- Text responses (HTML, CSS, JS, JSON, XML, SVG) are gzip-compressed when the client accepts it.
  Admin pages stay uncompressed (they carry the CSRF token; BREACH).
- Version-named vendor files and `?v=` asset URLs are served with
  `Cache-Control: public, max-age=31536000, immutable`.
- **Public CSP:** public pages no longer allow scripts from `cdn.tailwindcss.com`,
  `cdn.jsdelivr.net` or `unpkg.com`, nor `'unsafe-eval'`; the bundled themes need none of them. A custom
  theme that does can be allowed with `GORUN_CSP_SCRIPT_SRC` / `csp_script_src`. `/api/docs` allows only
  the pinned Scalar bundle, now with SRI. The admin keeps `'unsafe-eval'` for Alpine.
- CI runs staticcheck (`staticcheck.conf`); unused code it found was removed.
- **Local backups leave out `secret.key` and `gorun.toml` by default.** Keep them separately (a password
  manager or offline copy) and restore them next to the archive; `gorun backup create --include-secrets`
  restores the old behaviour for an archive you protect yourself. Admin and remote backups never include
  them.
- Downloading a backup from the admin requires confirming your password within the last 10 minutes
  (`/admin/reauth`); signing in counts. Migration `0009` adds `sessions.reauth_at`.
- **HSTS:** the header is now `max-age=31536000` without `includeSubDomains` by default.
  `GORUN_HSTS_INCLUDE_SUBDOMAINS=true` and `GORUN_HSTS_PRELOAD=true` (`hsts_include_subdomains`,
  `hsts_preload`) add the directives; preload requires includeSubDomains. Set the first to keep the
  previous header.
- GitHub Actions are pinned to commit SHAs, and Dependabot proposes weekly Go module, Actions and e2e
  npm updates.
- `deploy/docker-compose.yml` passes `VERSION` (or Coolify's `SOURCE_COMMIT`) to the image build.

### Documentation

- Outside contributions are accepted under the Developer Certificate of Origin: commits are signed off with
  `git commit -s`, checked by the `dco` workflow (bot commits skipped). There is no CLA.
- `deploy/docker-compose.yml` and `deploy/coolify.md` configure `GORUN_TRUSTED_PROXIES`.
- SECURITY.md and docs/PRODUCTION.md now state that no forwarding headers are trusted by default.

### First public release

- GoRun is published as open source under the GNU Affero General Public License v3.0 (see
  `LICENSE` and `NOTICE`).
- The command line lives in the importable package `pkg/cli`; `cmd/gorun` is a thin wrapper.
  A separate binary can add modules, commands, boot-order entries, error-inventory categories and
  a trusted site-policy directory through `cli.Option`s (see docs/ARCHITECTURE.md, "Building on
  the core"). Every core command and flag is unchanged.
- The trusted directory for `GORUN_SITE_POLICY` files defaults to `/var/lib/gorun-site-policies`;
  a downstream build can set another one with `cli.WithSitePolicyDirectory`.
- Hosting-platform add-ons, brand themes and the commands that served them (`tenant-init`,
  `addon-install`, `page-catalog`) are no longer part of the core.

[Unreleased]: https://github.com/zadstack/gorun/compare/v0.14.1...HEAD
[0.14.1]: https://github.com/zadstack/gorun/compare/v0.14.0...v0.14.1
[0.14.0]: https://github.com/zadstack/gorun/compare/v0.13.0...v0.14.0
[0.13.0]: https://github.com/zadstack/gorun/compare/v0.12.0...v0.13.0
[0.12.0]: https://github.com/zadstack/gorun/compare/v0.11.0...v0.12.0
[0.11.0]: https://github.com/zadstack/gorun/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/zadstack/gorun/compare/v0.9.0...v0.10.0
[0.9.0]: https://github.com/zadstack/gorun/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/zadstack/gorun/compare/v0.7.0...v0.8.0
[0.7.0]: https://github.com/zadstack/gorun/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/zadstack/gorun/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/zadstack/gorun/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/zadstack/gorun/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/zadstack/gorun/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/zadstack/gorun/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/zadstack/gorun/releases/tag/v0.1.0
