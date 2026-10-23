# Modules, services and extensions

> **ملخص بالعربية.** صفحة **النظام ← الوحدات** (`/admin/system/modules`) وعملية
> `GET /api/v1/system/modules` تعرضان كل ما يشغّله هذا الإصدار: الوحدات المدمجة (النواة والميزات)، الخدمات
> الأساسية (النماذج والتخزين والنسخ الاحتياطي والبريد) والإضافات الخارجية، ولكلٍّ: وظيفتها، مصدرها، إصدارها،
> توافقها، ما تعتمد عليه، صلاحياتها، ما سجّلته فعلًا في هذه العملية، حالتها، وهل تحتاج إعدادًا. الوحدات
> الاختيارية (commerce وsaas وmembership وlaunch وworkagents وwpmigrate وmcp وext وidentityai) تُوقَف وتُفعَّل من الصفحة أو عبر
> `PUT /system/modules/{name}/activation`؛ يُطبَّق التغيير بعد إعادة التشغيل، ولا تُحذف بيانات، وتُوقَف مهام
> الوحدة المعطّلة في الطابور (parked) حتى تفعيلها. كل ما لم يُرصد يُعرض «غير معروف» ولا يُفترض.

GoRun runs three kinds of things, and this page tells them apart by what they are, not by what they
are called:

| Kind | What it is | Where it lives | Switch |
|---|---|---|---|
| **Core module** | A domain every install boots (settings, auth, content, taxonomy, media, menus, redirects, seo, theme, pattern, builder, site, api, admin, webhooks). | `internal/<name>`, `kernel.BootOrder` up to `webhooks` | always on |
| **Built-in feature module** | A feature compiled into the binary that owns its routes, workers, admin pages, migrations and permissions (commerce, saas, membership, launch, workagents, wpmigrate, mcp, ext, identityai). | `internal/<name>`, after `webhooks` | All nine implement `kernel.OptionalModule` using `kernel.AddonEnabled` |
| **Core service** | A general service that is a feature of a core module and is configured by settings: forms (site), storage (media), backups (admin), mail (admin). | the owning module | never switched; configured |
| **External extension** | A separate program described by `manifest.json`, receiving hooks over signed HTTP and calling the REST API with a scoped key ([EXTENSIONS.md](EXTENSIONS.md)). | `<data>/extensions/<slug>` | activate / deactivate / uninstall on its own page |

The boundary definitions and the audit behind them are in [BOUNDARIES.md](BOUNDARIES.md).

## The catalog

**System → Modules** (`/admin/system/modules`, permission `system.health`) and
`GET /api/v1/system/modules` list one entry per module, service and extension:

| Field | Source | Honest value when unknown |
|---|---|---|
| function (`name`, `summary`, ar/en) | admin catalog strings for built-ins; the manifest for extensions | "No description in this build" / "No description in the manifest" |
| `source`, `version`, `compatibility` | built-ins: the binary (`build`), `built_in`; extensions: manifest version, `manifest_unverified` (compatibility with this build is not verified) | a module named in `GORUN_ADDONS` but absent from the build is `unavailable`, version `unknown`, `not_in_build` |
| `dependencies`, `optional_dependencies`, `dependents` | the declared table in `internal/admin/modules.go` (checked against `kernel.BootOrder` by `TestBuiltinCatalogCoversBootOrder`) | — |
| `permissions` | what the module declared in this boot (`registered.permissions`); the manifest for extensions | empty while a module is off |
| `registered` | **observed by the kernel**: the difference of every registry (permissions, API operations, MCP tools, job kinds, crons, admin nav and widgets, theme funcs and kinds, services published and looked up, hook subscriptions) before and after the module's `Register` ran | absent while off; lookups a module makes later at request time are not seen |
| `state` | `enabled` is the booted state; `desired` is what the next boot will apply (operator config mode, else the `addons.<name>.enabled` setting, missing = on); `pending_restart` when they differ; `switchable` when optional and not forced by `GORUN_ADDONS` | always-on modules have `desired: null` |
| `configuration` | the module's own verdict through the `system.capabilities.modules` filter (today commerce reports its Stripe keys); services read their settings (`forms.recipients`/`contact.email`, storage profile, `backup.schedule_hours`, SMTP) | `not_required` when a module publishes nothing; `unknown` while it is off |
| `jobs` | queued and parked counts for the module's job kinds (registered now, or the last known kinds while off) | — |
| `messages` | pending restart, data kept while off, parked jobs, a dependency that is off (with the concrete consequence), what turning this off affects | — |

Nothing in the catalog runs an external check; `observed_at` is when the snapshot was read.

## Turning a built-in module off and on

Only optional modules can be switched. Three sources decide, in this order:

1. **Operator config** (`[addons]` in the site config or `GORUN_ADDONS='{"commerce":"disabled"}'`):
   mode `disabled` always wins and the page shows "Forced by GORUN_ADDONS"; `enabled` and `auto`
   leave the decision to the module.
2. **The site setting** `addons.<name>.enabled` (JSON boolean), written by the page's button, by
   `PUT /api/v1/system/modules/{name}/activation` (`{"enabled": false}`, permission
   `settings.manage`, audited as `module.activation`) or by `gorun settings set addons.commerce.enabled false`.
   A missing key means enabled. A non-boolean value (including `null`) stops the boot with the key named.
3. **Restart.** Activation is read once at boot; until then the catalog shows
   `pending_enable` / `pending_disable`.

What happens at the next boot of a module that is **off**:

- its migrations, catalog, routes, API operations, MCP tools, job kinds, crons, admin navigation,
  theme funcs and kinds, services and hooks are **not registered**: public pages return 404, API
  operations are absent from the OpenAPI document, admin pages are gone;
- its **tables and settings are untouched** (`kernel.OptionalModule` skips migration and
  registration, nothing else);
- its **queued jobs are parked** by kind (`parked_reason = "module <name> disabled"`) using the kinds
  it registered at its last enabled boot (`kernel.inventory.<name>.job_kinds`). Workers never claim
  parked work or unregistered kinds, so nothing runs, fails or is deleted; System → Health and the
  catalog show the parked count.

What happens when it is **on** again: the same tables and settings are read, parked jobs of its
kinds are released unchanged (payload, attempts, idempotency key) and run, routes and pages return.
`Unpark` resets `run_at` to the current time, including jobs previously scheduled in the future;
the registered handler may then complete, fail or retry them.
`TestDisabledModuleParksQueuedJobsWhileWorkersRunAndResumesOnEnable` (kernel) and
`TestModuleCatalogListsBuildAndSwitchesOptionalModules` (`pkg/cli`, with the real commerce module)
exercise this with workers running.

### Dependencies when a module is off

Built-in modules reach each other through services and hooks, so a dependency being off never
stops a boot; it changes behaviour, and the catalog says how on the dependent's row:

| Off | Dependent | Consequence shown |
|---|---|---|
| commerce | saas | Customer sign-in is unavailable and subscription sync from Stripe stops; local SaaS grants remain usable by authenticated users. |
| commerce | membership | Customer sign-in is unavailable; memberships, quotas and submission data are preserved and remain usable by authenticated users. |
| commerce | wpmigrate | WooCommerce products are skipped during imports; posts, pages and media still import. |
| commerce | mcp | The `list_products` tool is not offered to agents. |
| commerce | theme | Store pages (`/shop`, `/cart`, `/account`) are not served; themes render with empty product lists. |
| wpmigrate | mcp | The migration tools are not offered to agents. |
| workagents | membership | The Agents navigation link and `/agents` route are unavailable; membership data are preserved. |

### Effects of switching feature modules off

| Off | Consequence after restart |
|---|---|
| launch | The `/start` onboarding and sign-in routes are unavailable. |
| workagents | Agent routes and admin pages are unavailable; its crons and job handlers are absent. Previously registered queued jobs are parked while workers run, then released on re-enable. Membership hides its Agents link. |
| wpmigrate | Migration routes, workers and watchdog are absent; MCP migration tools disappear. CLI import and preview refuse a disabled migrator. Existing sources and import data are retained. |
| mcp | Module-owned MCP routes and tools are absent; the stdio entry point refuses to start without the booted MCP service. Shared core AI clients are unaffected. |
| ext | System → Extensions and the extension bridge are unavailable. Installed extensions receive no new hook deliveries while the module is off; previously queued delivery jobs are parked while workers run. Extension files and records are retained. Hook events emitted while off are not replayed on re-enable. |

A hard dependency that is off shows "Depends on X, which is off".

Membership and SaaS each serve their own stylesheet (`/membership/assets.css` and
`/saas/assets.css`). Their account navigation follows the services actually registered at boot;
Membership also hides `/agents` when WorkAgents is off. With Commerce off, anonymous visitors
receive an actionable sign-in-unavailable response (503); authenticated users can still use
the standalone pages. The independent-addon boot/browser tests cover Commerce/SaaS/Membership
combinations, and `TestMembershipNavigationTracksWorkAgentsAcrossDisableAndReenable` covers
the Agents link and preserved membership data.

### Orphan jobs

A queued job whose kind has no handler in this process is never claimed. The catalog attributes it
to the module whose last boot registered that kind and counts it as parked (after a boot that
disabled the module) or queued (when the module was never booted by this kernel version, for
example after an update that renamed a kind). System → Health lists `unregistered_jobs` by kind;
System → Jobs shows the parked reason per job. Enabling the module, or retrying the job once a
handler exists, is the remedy; nothing deletes them.

## Core services

Forms, storage, backups and mail are listed as **core services** with their owning module because
that is how they are built: features of a core module with settings, not modules with a lifecycle.
Their `configuration` comes from those settings:

| Service | Owner | Configured when | Where |
|---|---|---|---|
| forms | site | `forms.recipients` or `contact.email` is set (submissions are stored regardless; without recipients the first active admin is mailed) | Inbox → Form submissions |
| storage | media | the active storage profile validates (local, or S3 with endpoint, bucket and credentials) | Settings → Storage |
| backups | admin | `backup.schedule_hours` ≥ 1 (manual backups always work) | System → Backups |
| mail | admin | SMTP settings exist; a test send records delivery evidence | Settings → Mail |

## External extensions

The ext module adds one entry per installed extension (`ext:<slug>`) with the manifest's name,
description, version, permissions (its key's scopes), hook subscriptions, declared settings
(`incomplete` while any is empty), activation state (`installed`, `active`, `disabled`, `error`)
and a link to its page, where it is activated, deactivated or uninstalled
([EXTENSIONS.md](EXTENSIONS.md#lifecycle)). Compatibility with the running build is not verified
and is reported as such.

## For module authors

Nothing new is required: the kernel observes what `Register` does. To be described well:

- add a summary to `internal/admin/catalog/admin.{en,ar}.json` (`modules.name.<name>`,
  `modules.desc.<name>`) and the kind and dependencies to `moduleCatalogTable` in
  `internal/admin/modules.go` (the test fails when a `BootOrder` module is missing);
- implement `kernel.OptionalModule` with `kernel.AddonEnabled(ctx, db, "<name>")` to become
  switchable; keep registering everything in `Register` so disabling removes all of it;
- publish configuration requirements on the `system.capabilities.modules` filter to replace
  `not_required` with a real verdict;
- declare your own permissions in `Register` (even when the auth base catalog already names them
  for default roles): the inventory attributes a permission to every module that declares it.

### Extension module activation and API keys

Disabling the `ext` module removes its Extensions UI, routes, hook subscriptions and delivery
worker. It does not revoke existing `ext:<slug>` API keys: scoped REST access remains available
through the core API. Deactivating an individual extension stops its hook delivery but also keeps
its API key. Uninstalling the extension revokes its key; operators can explicitly revoke a key
under **Admin → API keys** when immediate REST access removal is required.
