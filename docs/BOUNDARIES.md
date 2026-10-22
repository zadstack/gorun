# Dependency boundaries

> **ملخص بالعربية.** هذه الخريطة تصف حدود GoRun: النواة (kernel والبنية التحتية والوحدات الأساسية)،
> القوالب (`themes/` و`patterns/`)، الوحدات المدمجة الاختيارية (commerce وsaas وmembership وغيرها)،
> والإضافات الخارجية (`internal/ext`). القالب يستهلك بيانات عرض معلنة فقط ولا يحمل منطق أعمال؛
> الوحدة الاختيارية تملك مساراتها وعمّالها وواجهتها وترحيلاتها وصلاحياتها وتصل إلى غيرها عبر الخدمات
> والخطافات لا بالاستيراد؛ وما يُشترك فيه فعلًا يبقى في النواة. يتحقق
> `internal/kernel/boundaries_test.go` من قواعد الاستيراد ومن مطابقة الجدول المولَّد أدناه للكود.

This document is the boundary map for GoRun: which code is core, which is a theme, which is a
built-in optional module and which is an external extension, with the dependencies between them
(compile-time imports, runtime services and hooks, job kinds and crons, routes, template helpers
and assets). It records the audit of 2026-10-22 against `main` at `a3bccae` and the decision
about `cli.CoreModules()`.

The import graph section is **generated and checked** by
`internal/kernel/boundaries_test.go` (`TestBoundariesDocumentMatchesImportGraph`); the rest was
produced from the commands listed in [How this was produced](#how-this-was-produced) and
verified by hand. The same test file enforces the import policy below.

## Definitions

| Term | Meaning in GoRun | Definition of clean separation |
|---|---|---|
| **Kernel and infrastructure** | `internal/kernel` and the packages it constructs or that sit under every module: `config`, `db`, `hooks`, `httpx`, `settings`, `i18n`, `audit`, `jobs`, `diagnostics`, `observe`, `update`, `metrics`, `backup`, `mailer`, `brand`, `starter`. | Never imports a domain module. Knows module *vocabulary* (service names, nav groups) but no module behaviour. |
| **Core module** | `kernel.BootOrder` from `settings` to `webhooks`: settings, auth, content, taxonomy, media, menus, redirects, seo, theme, pattern, builder, site, api, admin, webhooks. | Boots in every install. Imports only earlier core modules or infrastructure. Never imports a feature module. |
| **Built-in feature module** | `BootOrder` after `webhooks`: commerce, saas, membership, launch, workagents, wpmigrate, mcp, ext. | Owns its routes, workers, admin UI, migrations and permissions. Reaches other modules through `kernel.Get`, hooks or an adapter published in `Deps.Services`; imports another feature module only through a declared edge (`featureEdges` in the test). |
| **Theme / pattern** | `themes/<slug>` (templates, parts, components, `lang/`, `assets/`, `theme.json`) and `patterns/<slug>`. Public contract: `pkg/themekit`, `pkg/patternkit`, [THEME-CONTRACT.md](THEME-CONTRACT.md). | Consumes declared display data (`theme.Context`, template funcs) and nothing else. No Go code, no business logic, no hard dependency on an optional module: a func an optional module provides must have a core fallback. |
| **External extension** | A program described by a manifest (`internal/ext`): signed hook deliveries over HTTP and a scoped API key. [EXTENSIONS.md](EXTENSIONS.md). | Sees GoRun only through hooks and the REST API. GoRun never loads extension code. |
| **Composition root** | `pkg/cli` (`CoreModules()`, `Modules(cfg)`, commands) and `cmd/gorun`. | Imports everything by design; downstream binaries extend it with `cli.WithModules`. |

## Import graph (generated)

Module-level, non-test files, sub-packages collapsed into their module (`internal/settings/settingsmod`
is the settings *module*; `internal/settings` is the store). `web` is the embedded admin/launch
asset FS (`web/embed.go`); `themes` is the bundled-theme embed (`themes/bundled.go`).

<!-- BEGIN GENERATED IMPORT GRAPH -->
| Package | Layer | Imports (repository packages, non-test files) |
|---|---|---|
| `internal/backup` | infra | `db` |
| `internal/brand` | infra | — |
| `internal/config` | infra | — |
| `internal/diagnostics` | infra | `db` |
| `internal/hooks` | infra | `jobs` |
| `internal/httpx` | infra | `audit` `db` `i18n` |
| `internal/kernel` | infra | `audit` `config` `db` `diagnostics` `hooks` `httpx` `i18n` `jobs` `metrics` `observe` `settings` `update` |
| `internal/mailer` | infra | `db` `settings` |
| `internal/metrics` | infra | `backup` `jobs` `settings` |
| `internal/observe` | infra | `db` `jobs` |
| `internal/starter` | infra | — |
| `internal/update` | infra | `db` `jobs` |
| `internal/db` | infra | — |
| `internal/settings` | infra | `db` `kernel` |
| `internal/i18n` | infra | — |
| `internal/audit` | infra | `db` |
| `internal/jobs` | infra | `db` |
| `internal/auth` | core | `audit` `db` `httpx` `i18n` `kernel` `web` |
| `internal/content` | core | `audit` `auth` `db` `hooks` `httpx` `i18n` `kernel` `settings` |
| `internal/taxonomy` | core | `audit` `auth` `content` `db` `hooks` `httpx` `i18n` `kernel` |
| `internal/media` | core | `audit` `auth` `db` `hooks` `httpx` `kernel` `mailer` `settings` |
| `internal/menus` | core | `audit` `auth` `content` `db` `hooks` `httpx` `i18n` `kernel` `taxonomy` |
| `internal/redirects` | core | `audit` `auth` `db` `hooks` `httpx` `kernel` |
| `internal/seo` | core | `auth` `content` `db` `hooks` `httpx` `kernel` `menus` `settings` `taxonomy` |
| `internal/theme` | core | `auth` `brand` `config` `content` `db` `httpx` `i18n` `kernel` `media` `menus` `redirects` `seo` `settings` `taxonomy` `web` |
| `internal/pattern` | core | `auth` `config` `httpx` `i18n` `kernel` `pkg/patternkit` `settings` `theme` |
| `internal/builder` | core | `auth` `brand` `content` `db` `httpx` `i18n` `kernel` `media` `menus` `settings` `theme` |
| `internal/site` | core | `audit` `auth` `brand` `content` `db` `httpx` `i18n` `kernel` `mailer` `redirects` `seo` `settings` `taxonomy` `theme` |
| `internal/api` | core | `auth` `config` `content` `db` `httpx` `i18n` `jobs` `kernel` `media` `menus` `settings` `taxonomy` `theme` |
| `internal/admin` | core | `api` `audit` `auth` `backup` `config` `content` `db` `diagnostics` `httpx` `i18n` `jobs` `kernel` `mailer` `media` `observe` `seo` `settings` `theme` `update` |
| `internal/webhooks` | core | `audit` `auth` `db` `httpx` `i18n` `jobs` `kernel` |
| `internal/commerce` | feature | `admin` `api` `audit` `auth` `content` `db` `httpx` `i18n` `jobs` `kernel` `mailer` `media` `observe` `redirects` `seo` `theme` |
| `internal/saas` | feature | `admin` `api` `audit` `auth` `commerce` `db` `httpx` `kernel` |
| `internal/membership` | feature | `api` `audit` `auth` `content` `db` `kernel` |
| `internal/launch` | feature | `auth` `httpx` `i18n` `kernel` |
| `internal/workagents` | feature | `admin` `auth` `content` `db` `httpx` `jobs` `kernel` |
| `internal/wpmigrate` | feature | `admin` `api` `audit` `auth` `commerce` `config` `content` `db` `hooks` `httpx` `i18n` `jobs` `kernel` `media` `menus` `redirects` `seo` `settings` `taxonomy` `theme` |
| `internal/mcp` | feature | `admin` `api` `audit` `auth` `content` `db` `httpx` `i18n` `kernel` `media` `menus` `seo` `taxonomy` `theme` |
| `internal/ext` | feature | `admin` `api` `audit` `auth` `config` `content` `db` `hooks` `httpx` `i18n` `jobs` `kernel` `settings` |
| `pkg/edge` | contract | — |
| `pkg/patternkit` | contract | — |
| `pkg/themekit` | contract | — |
| `cmd/gorun` | binary | `pkg/cli` |
| `pkg/cli` | binary | `admin` `api` `audit` `auth` `backup` `builder` `commerce` `config` `content` `db` `diagnostics` `ext` `i18n` `jobs` `kernel` `launch` `mcp` `media` `membership` `menus` `observe` `pattern` `pkg/themekit` `redirects` `saas` `seo` `settings` `site` `starter` `taxonomy` `theme` `themes` `update` `webhooks` `workagents` `wpmigrate` |
<!-- END GENERATED IMPORT GRAPH -->

Reading the graph:

- No infrastructure or core package imports a feature module (also implied by `BootOrder`, now
  asserted directly by `TestFeatureModulesImportOnlyDeclaredFeatureEdges`).
- Two declared feature→feature edges exist: `saas → commerce` (the three `subscription.*` hook
  names and `commerce.SubscriptionEvent`) and `wpmigrate → commerce` (the product importer adapter
  in `adapter_commerce.go`, published as `wpmigrate.product_importer` only when commerce booted).
  Any new edge must be added to `featureEdges` with a reason, or go through a service.
- `pkg/themekit`, `pkg/patternkit` and `pkg/edge` import nothing from `internal/`
  (`TestContractPackagesAndThemesStayStandalone`), so theme and pattern authors build against a
  stable surface. `themes/` and `patterns/` contain no Go besides `themes/bundled.go`.

## Runtime contracts

Services are published with `d.Services.Set` and consumed with `kernel.Get[T]`; a consumer that
finds no service degrades (see the *Absent* column). Hooks: `Hooks.Action`/`Filter` subscribe,
`hooks.Do`/`Apply`/`Emit` fire. Full hook, job and cron tables are in
[ARCHITECTURE.md](ARCHITECTURE.md#hooks).

### Services

| Service name | Published by | Consumed by (non-test) | Absent → |
|---|---|---|---|
| `auth`, `auth.keys`, `auth.roles`, `admin.templates` | auth | every module with admin pages; commerce, wpmigrate, ext, mcp, workagents, launch | boot error in most consumers (auth is core, always present) |
| `content`, `content.comments` | content | taxonomy, menus, seo, theme, site, api, admin, builder, membership, workagents, wpmigrate, mcp, ext | wpmigrate refuses to boot; ext falls back to no content types; others are core |
| `taxonomy`, `media`, `media.storage`, `menus`, `redirects`, `seo`, `theme`, `pattern` | the module of that name | core consumers plus commerce (media, redirects, theme), wpmigrate, mcp, builder | core, always present |
| `webhooks` | webhooks | admin (canvas activity), `gorun webhook` | admin reads `any` and skips |
| `mailer` (`mailer.ServiceName`) | admin | commerce (`service.go`), site forms | commerce logs and skips mail |
| `observe`, `update` (`observe.ServiceName`, `update.ServiceName`) | admin | commerce (billing-server observation), admin | commerce skips observation registration |
| `kernel.module_inventory`, `kernel.schema`, `diagnostics` | kernel | admin capability report, health, updates | kernel, always present |
| `kernel.navigation_pins`, `admin.canvas_read` | admin | auth navigation, mcp | optional reads |
| **`commerce`** (`kernel.ServiceCommerce`) | commerce | wpmigrate adapter (`kernel.Get[commerce.Service]`), mcp (`kernel.Get[any]` + reflection), commerce itself | wpmigrate publishes no product importer; mcp registers no `list_products`; themes get the nil fallbacks for `products`/`product` |
| `wpmigrate`, `wpmigrate.product_importer` | wpmigrate | mcp (`get_migration_status`, `run_migration_preview` via `any`) | mcp registers no migration tools |
| `saas`, `membership`, `builder`, `workagents`, `ext`, `mcp` (internal) | the module | nothing outside the module (string keys, not kernel constants) | — |

### Hooks

| Hook | Fired by | Subscribed by (non-test) |
|---|---|---|
| `content.before_save` (filter), `content.after_save`, `content.published`, `content.deleted` | content | webhooks (every catalog event), ext bridge |
| `content.authorize_save` (filter) | content | **membership** (quota check) |
| `taxonomy.term_saved`, `media.uploaded`, `media.deleted`, `menus.saved`, `redirects.saved`, `user.login`, `site.form.submitted` | the core module | webhooks, ext bridge; site (form mail) |
| `menus.resolve_url`, `seo.sitemap_entry`, `theme.render.context` (filters) | menus, seo, theme | theme (links) |
| `system.health.checks` (filter) | admin | commerce, saas, wpmigrate, mcp, ext, workagents |
| `system.capabilities.modules` (filter) | admin | commerce (`requirements`: Stripe keys present or not) |
| `commerce.order.paid`, `commerce.entitlement.activated` | commerce | webhooks, ext bridge |
| `subscription.activated`, `subscription.past_due`, `subscription.canceled` | commerce | **saas** (`applySubscription`) |
| `migration.item.imported` | wpmigrate | webhooks, ext bridge |

### Workers: job kinds and crons

Workers claim only kinds registered in this process; queued work of a disabled module waits and
can be parked (`internal/jobs/park.go`). Crons run only when their module registered them.

| Module | Job kinds | Crons |
|---|---|---|
| kernel/hooks | `hooks.event` | — |
| auth | — | `auth.cleanup` 1h |
| content | — | `content.publish_due` 30s, `content.purge_trash` 1h |
| media | `media.fetch_url` | — |
| admin | `canvas.generate`, `canvas.tools`, `studio.generate`, `backup.scheduled`, `update.run`, `observe.*` | `canvas.schedule` 1m, `studio.recover` 1m, `jobs.prune` 1h, `observe.tick` 1m, `update.reconcile` 1m, `backup.schedule` 1h |
| webhooks | `webhooks.deliver` | `webhooks.prune` 24h |
| **commerce** | `commerce.provision`, `commerce.suspend`, `commerce.resume`, `commerce.magic_mail`, `commerce.billing_mail` | `commerce.expire` 24h, `commerce.billing_grace` 1h, `commerce.request_receipts` 10s |
| workagents | `workagents.run` | `workagents.schedules` 30s, `workagents.linear` 30s |
| wpmigrate | `migration.step`, `migration.item`, `migration.advance` | `wpmigrate.watchdog` 5m |
| mcp | — | `mcp.approvals.expire` 1h |
| ext | `ext.deliver` | — |
| saas, membership, launch | — | — |

### Routes, API operations, MCP tools, permissions

Public paths are mounted at `/`, admin paths under `/admin`, API operations under `/api/v1`
(the OpenAPI document lists only booted modules), webhooks under `/webhooks`.

| Module | Public routes | Admin pages (under `/admin`) | API ops | MCP tools | Permissions declared |
|---|---|---|---|---|---|
| auth | `/auth/magic/{token}`, static assets | `/login`, `/setup`, `/onboarding`, `/reauth`, `/locale`, `/navigation`, `/security/*` | — | — | core role catalog (`auth/perms.go`) |
| content, taxonomy, media, menus, redirects, seo | `/media/*` (media), redirects middleware | `/content/*`, `/types/*`, `/comments`, `/taxonomies/*`, `/media/*`, `/menus/*`, `/redirects/*`, `/settings/seo` | content 1 set, taxonomy 1, media 5, menus 5, redirects 3 | — | `content.*`, `types.manage`, `media.*`, `taxonomy.manage`, `menus.manage`, `redirects.manage` |
| theme, pattern, builder | `/themes/*/assets`, `/patterns/{slug}/assets/*`, `/static/builder/*` | `/appearance/*`, `/site-design`, `/site-builder`, `/builder/{id}/*` | theme 1, builder 6 | builder 6 (page layouts) | `themes.manage` |
| site | `/`, `/forms/contact`, `/forms/comment`, `/_page/{id}/unlock`, `/reports/{form}` (front controller) | `/reports/*`, `/settings/availability`, `/settings/page-access`, `/settings/reports` | — | — | `settings.manage` (reuse), `reports.review` |
| api | — | — | `/v1/openapi.json`, `/docs`, `/v1/*` mount, `site/info` | — | — |
| admin | `/webhooks/canvas/{endpoint}` | dashboard, `/users/*`, `/roles/*`, `/settings/*` (general, mail, storage, ai, connections, edge, integrations, studio, metricool), `/system/*` (health, jobs, backups, updates, errors), `/canvases/*`, `/agents/*`, `/metricool`, `/workspace` | 22 (+ keys, roles, canvas read) | canvas read/operations 3 | `users.manage`, `settings.manage`, `api_keys.manage`, `system.health`, `system.backup`, `system.update` |
| webhooks | — | `/webhooks/*` | 1 | — | `webhooks.manage` |
| **commerce** | `/shop`, `/product/{slug}`, `/cart*`, `/checkout*`, `/account*`; `/webhooks/stripe{,/test,/live}` | `/commerce/*` (products, orders, entitlements, servers, settings, setup) | 14 | `list_products` (registered by mcp when the service exists) | `commerce.manage`, `commerce.view_orders` |
| **saas** | `/saas`, `/saas/assets.css` | `/saas`, `/saas/new`, `/saas/apps/*`, `/saas/subscriptions/*` | 8 | — | `saas.read`, `saas.use`, `saas.manage` |
| **membership** | `/membership`, `/membership/submissions` | `/membership`, `/membership/grants` | 7 | — | `membership.manage`, `membership.read`, `membership.unlimited`, `membership.submit` |
| launch | `/start`, `/start/login`, `/start/verify` | — | — | — | — |
| workagents | `/agents`, `/agents/*`, `/static/workagents.css` | `/workagents`, `/workspace` | `/v1/workagents/*` | — | `workagents.manage`, `workagents.read`, `workagents.run` |
| wpmigrate | — | `/migrations/*` | 1 | `get_migration_status`, `run_migration_preview` | `migrations.run` |
| mcp | `/mcp` | `/agents/approvals/*` | 3 | content, media, taxonomy, templates, comments, settings, logs, site structure | `mcp.approve`, `mcp.approve_self` |
| ext | — | `/extensions/*` | 4 | — | `extensions.manage` |

### Themes and patterns: what they consume

Every bundled theme is a child of `default` (`theme.json` → `parent`). Template helpers used per
theme (non-control-flow), from a grep of `{{ … }}` across `themes/<slug>/**/*.html`:

| Theme | Helpers used | Commerce display contract | External assets |
|---|---|---|---|
| default | `t url asset part component img brandImg favicon identity builderIdentity builderBody money date dir locale csrf nonce safe seo add` | `products` (hero facts count), `product`; templates `shop.html`, `product.html`, `cart.html`, `checkout.html`, `checkout-result.html`, `account.html` rendered only for kinds commerce registers; `assets/commerce.css` | none (Tailwind precompiled, Lucide local) |
| business | default set + `excerpt` | `{{with products}}` on the front page (services strip), degrades to nothing | none |
| magazine, portfolio | default set | none | none |
| docs | `t url asset part component brandImg date` | none | none |
| patterns (company, editorial, guidebook, showcase) | `t url asset part img date excerpt` | company: `{{with products}}` offerings strip | none |

The theme engine (`internal/theme/funcs.go`) registers `products` and `product` fallbacks that
return nil, then overlays `Deps.Theme.Funcs()`; commerce replaces them when booted. This is the
declared display contract: a theme may *show* store data, never compute it. The engine funcs
(`t`, `url`, `menu`, `query`, `content`, `media`, `img`, `logo`, `brandImg`, `favicon`, `seo`,
`paginate`, `money`, …) are the only business-adjacent helpers and all live in core.

### External extensions

`internal/ext` subscribes extensions to the same `hooks` registry as modules and delivers over
signed HTTP; it consumes `auth.keys` (scoped API key per extension) and `content` (declared content
types) and registers `ext.deliver`. An extension never shares process memory with GoRun.

## Findings

Severity: **violation** (crosses the definition above), **smell** (works, but couples layers
through knowledge rather than a contract), **accepted** (deliberate, documented).

| # | Where | Finding | Severity | Action |
|---|---|---|---|---|
| 1 | `pkg/cli/modules.go` | commerce, saas and membership were unconditional registrations: every install (a blog, a docs site) booted a store with five job kinds, three crons (one every 10 s), `/shop`, `/cart`, `/account`, Stripe webhook endpoints, example products seeded on first boot and a `/my-account` redirect. Nothing in core needs them (graph above), yet they could not be turned off. | violation | **Fixed here**: the three modules implement `kernel.OptionalModule` (`Enabled` → `kernel.AddonEnabled`), enabled by default; see [Decision](#decision-coremodules). |
| 2 | `internal/kernel/optional.go` | `config.Addons` (GORUN_ADDONS) was parsed and shown in the inventory but no production module implemented the gate, so the documented "existing activation configuration" switched nothing. | violation | **Fixed here**: the kernel honours mode `disabled` for optional modules (restrict-only; `enabled`/`auto` leave the decision to the module, as `TestModuleInventoryTracksActualBootAndPreservesDisabledData` requires). `gorun migrate` uses the same selection (`MigrateAllWithConfig`). |
| 3 | `internal/saas`, `internal/wpmigrate` → `internal/commerce` | Compile-time imports of a feature module by two others. saas uses hook names and an event type; wpmigrate wraps `commerce.Service` in a `ProductImporter` adapter resolved at runtime with `kernel.Get`. Both tolerate commerce being disabled (tested). | accepted | Declared in `featureEdges`; any new feature→feature import fails the boundary test. |
| 4 | `internal/theme/funcs.go` | Core theme engine names two optional funcs (`products`, `product`) to provide nil fallbacks. | accepted | It is the display contract that lets every theme parse without commerce; documented above. |
| 5 | `themes/default/templates/{cart,checkout,account,...}.html` | Store templates ship with the default theme and carry hard-coded Arabic copy (`السلة`, `إفراغ السلة`, `ملخص الطلب`) instead of `{{t}}` keys, so an English store renders Arabic. No business logic found (totals come from `.Extra.cart`). | smell (quality) | Theme PR (bundled theme quality), not a boundary change. |
| 6 | `internal/saas/module.go`, `internal/membership/module.go` | Public pages are inline Go templates (`lang="ar" dir="rtl"`, Arabic only) that bypass the active theme and hard-link `/shop`, `/account`, `/agents`, `/saas`, which 404 when their modules are off. | smell | Catalog/extension PR: report cross-module links as dependencies with an actionable message; rendering through the theme is a feature change outside this audit. |
| 7 | `internal/auth/perms.go` | The core role catalog grants `commerce.manage`, `commerce.view_orders`, `mcp.approve`, `extensions.manage`, `migrations.run` to default roles, so core knows feature permission names. Harmless when the module is off (undeclared permissions are inert). | smell | Document; a later change could let modules contribute default role grants. |
| 8 | `internal/httpx/navigation_sections.go`, `render.go` | Infrastructure knows the `commerce` nav section and special-cases the `saas` nav id when it is the only item. Display vocabulary, no behaviour. | smell | Leave; nav groups are a core vocabulary modules opt into. |
| 9 | `internal/admin/workspace.go` | "Simple" workspace hides the `commerce` and `agents` nav groups; it hides UI, it does not stop routes or workers. | accepted | Distinct from activation; the catalog PR will say so where both appear. |
| 10 | `internal/commerce/module.go` `Register` | Seeds example products and a redirect at boot (idempotent via settings flags). Side effects at registration are allowed by the module contract but surprising for an install that never sells. | accepted | Disabling the module now prevents them; no change to the seed. |
| 11 | `internal/admin` | Core admin registers AI canvas/studio job kinds and crons. | accepted | Part of the core product (see PRODUCT.md), not a module boundary. |

## Decision: CoreModules

**Question.** Do commerce, saas and membership stay unconditional in `cli.CoreModules()` or become
optional registrations behind the existing `kernel.OptionalModule` gate?

**Evidence.**

1. Dependency direction: no infrastructure or core module imports them or needs their services; the
   three runtime consumers (mcp, wpmigrate, theme fallbacks) already degrade when the service is
   absent (`TestOptionalBuiltinsDisabledByConfigKeepData` boots the full module list with all three
   off and renders `/`, `/sitemap.xml`, `/robots.txt`, serves the API and the capability report).
2. Ownership: each registers its own routes, API operations, job kinds, crons, admin pages, theme
   kinds, migrations and permissions (tables above). Nothing of theirs is shared code.
3. Cost of always-on: 5 job kinds and 3 crons (one every 10 s), 20+ public routes, 3 webhook
   endpoints and seeded example products on every install.
4. Compatibility: the gate defaults to enabled (`addons.<name>.enabled` missing ⇒ on; config has
   no entry ⇒ ask the module), so existing installs boot exactly the same set of modules, the
   generated `docs/API.md` is unchanged, and e2e specs that exercise the store keep running.
5. Data safety: `kernel.OptionalModule` skips migrations and registration but never touches
   tables; the test disables, reboots, re-enables and reads the same product and plan rows back.

**Decision.** They become optional registrations, enabled by default. `CoreModules()` keeps its
name and order (downstream binaries call it); the modules themselves implement `Enabled`. launch,
workagents, wpmigrate, mcp and ext are feature modules by the same definition and are candidates
for the same gate; they are left unchanged in this PR to keep the diff reviewable, and the
catalog PR will list them with their state.

**How to switch.** Operator: `GORUN_ADDONS='{"commerce":"disabled"}'` or `[addons]` in the site
config, then restart. Site setting: `addons.commerce.enabled = false` (JSON boolean; written by
the admin toggle in the catalog PR), then restart. Mode `disabled` always wins; mode `enabled`
cannot force a module on over its own setting. The capability report (`GET
/api/v1/site/capabilities`) and System → Health show the result of the last boot, never the
intent.

## How this was produced

```sh
# import graph (also what the test parses)
go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... ./pkg/... ./cmd/...
# runtime contracts
grep -rn "Services.Set(" --include='*.go' internal pkg | grep -v _test.go
grep -rn "kernel.Get\[" --include='*.go' internal pkg | grep -v _test.go
grep -rn "Hooks\.\(Action\|Filter\)(" --include='*.go' internal | grep -v _test.go
grep -rn "Jobs\.\(Register\|Cron\)(" --include='*.go' internal | grep -v _test.go
grep -rn "Theme\.\(AddFunc\|AddKind\)(" --include='*.go' internal | grep -v _test.go
grep -rn "Perms\.Declare(\|API\.Add(\|MCP\.AddTool(\|Admin\.AddNav(" --include='*.go' internal | grep -v _test.go
# routes
grep -rhoE '\.(Get|Post|Put|Patch|Delete|Handle|Mount)\("[^"]+"' internal/<module> --include='*.go' --exclude='*_test.go'
# theme helpers
grep -rhoE '\{\{-?\s*[a-zA-Z_]+' themes/<slug> --include='*.html' | sort | uniq -c
# verification
go test ./internal/kernel -run 'TestImportOrder|TestFeatureModules|TestContractPackages|TestBoundaries'
go test ./pkg/cli -run TestOptionalBuiltins
```
