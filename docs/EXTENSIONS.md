# GoRun extensions

> **ملخص بالعربية.** الإضافة في GoRun **برنامج خارجي** لا كود Go يُحمَّل داخل الخادم. تصفها ملف
> `manifest.json` (الاسم والإصدار والصلاحيات والخطافات وأنواع المحتوى والإعدادات ورابط الاستقبال)، وتُثبَّت من
> **النظام ← الإضافات** (الصلاحية `extensions.manage`). عند التثبيت يُولَّد سر HMAC ومفتاح API بنطاقات
> الصلاحيات المعلنة، ويُعرضان مرة واحدة فقط. يرسل GoRun أحداث الخطافات إلى رابط الإضافة بطلبات POST موقّعة
> (المرشحات متزامنة، والإجراءات والأحداث عبر طابور المهام مع إعادة المحاولة)، وتستدعي الإضافة واجهة REST
> العادية بمفتاحها. تثبيت إضافة قرار ثقة: الخطافات تكشف البيانات كاملة بغض النظر عن نطاقات المفتاح. بعد 20
> فشلًا متتاليًا تتوقف الإضافة وتظهر في حالة النظام.

An extension is an **external program** that reacts to GoRun hooks and calls
back into GoRun through the REST API. GoRun has no Go native plugins: an
extension is described by a manifest, receives hook deliveries over signed
HTTP (the "HTTP bridge"), and acts with a scoped API key issued at install.

Internal modules register on the very same `hooks` registry (`hooks.Action`,
`hooks.Filter`, `hooks.Emit`); the bridge is just another subscriber. Anything
an internal module can observe or filter by hook name, an extension can too.

- Code: `internal/ext` (admin: **System → Extensions**, permission `extensions.manage`)
- Installed extensions also appear in **System → Modules** next to the built-in modules and core
  services, with their manifest version, hooks, permissions and configuration state
  ([MODULES.md](MODULES.md))
- Example: `examples/extensions/hello-http` (Go, standard library only)

## Package layout

```
<slug>/
  manifest.json      required
  assets/            optional, any files (stored, not executed or served in v1)
```

Install from the admin either by uploading a `.zip` (max 20 MB; `manifest.json`
at the root or inside one top-level folder) or by dropping the folder into
`<DataDir>/extensions/<slug>/` and clicking **Install** next to it. Only
`manifest.json` and `assets/**` are extracted (directory entries, as written
by `zip -r` or Finder, are fine); absolute paths, `..`, symlinks, duplicate
entries, more than 500 files or more than 50 MB uncompressed are refused. A
failed install is rolled back completely (row, types it created, setting
defaults, package directory it placed, API key).

## Manifest reference

```json
{
  "slug": "hello",
  "name": {"en": "Hello", "ar": "مرحبا"},
  "version": "1.0.0",
  "description": {"en": "…", "ar": "…"},
  "author": "Example Ltd",
  "permissions": ["content.view"],
  "hooks": [
    {"name": "content.before_save", "kind": "filter", "timeout_ms": 2000},
    {"name": "content.after_save", "kind": "action"},
    {"name": "content.published", "kind": "event"}
  ],
  "admin_nav": [{"label": {"en": "Dashboard", "ar": "اللوحة"}, "url": "https://hello.example.com/"}],
  "content_types": [ { "slug": "greeting", "name": {"en": "Greetings"}, "fields": [] } ],
  "settings": [ {"key": "greeting_word", "label": {"en": "Word"}, "type": "text", "default": "hello"} ],
  "endpoint": {"url": "https://hello.example.com/gorun", "auth": "hmac"}
}
```

| Field | Rules |
|---|---|
| `slug` | `^[a-z][a-z0-9-]{1,40}$`, unique among installed extensions |
| `name`, `description` | `{ar, en}` maps; `name` needs at least one non-empty value |
| `version` | semver (`1.2.3`, `1.2.3-beta.1`) |
| `permissions` | permission names declared in GoRun (see **Admin → API keys** for the list), e.g. `content.view`, `content.create`, `media.upload`. They become the API key's scopes. |
| `hooks[].name` | lowercase dot-separated name (`content.published`). Any name is accepted: modules register at boot, so a name nobody fires simply never delivers. |
| `hooks[].kind` | `action`, `filter` or `event` (one subscription per name and kind; `action` and `event` on the same name are mutually exclusive) |
| `hooks[].timeout_ms` | filters only; default 2000, max 10000 |
| `admin_nav[]` | links shown on the extension's admin page (`/path` or `http(s)://`) |
| `content_types[]` | `content.Type`-shaped (same schema as **Structure → Content types**); validated like a type you create in the admin; slugs must not collide with existing types (system or user) |
| `settings[]` | `content.Field`-shaped; types `text`, `textarea`, `number`, `boolean`, `select`; editable on the extension's admin page |
| `endpoint.url` | `http(s)` URL; loopback/private addresses are refused unless `GORUN_ALLOW_PRIVATE_NET=1` (deliveries also go through GoRun's SSRF-safe client, so a public name resolving to a private IP is blocked at dial time) |
| `endpoint.auth` | `"hmac"` (the only scheme in v1; the default) |

Unknown manifest fields are rejected.

## Lifecycle

| Step | What happens | Audit |
|---|---|---|
| **Install** | manifest validated; package stored in `<DataDir>/extensions/<slug>/`; row `installed`; declared **content types registered** (`system=false`); setting defaults written to `ext.<slug>.settings.<key>` (existing values kept); type slugs recorded in `ext.<slug>.types`; an **HMAC secret** generated and stored encrypted (AES-GCM under `GORUN_SECRET_KEY`); an **API key** named `ext:<slug>` issued with the manifest permissions as scopes (owned by the installing admin). The secret and the key are **shown once** in the install response and never again. | `ext.install` |
| **Activate** | missing declared types re-created (existing ones are never overwritten); hooks subscribed; status `active`; failure counter reset | `ext.activate` |
| **Deactivate** | delivery stops immediately (no restart); data kept; status `disabled`; the `ext:<slug>` API key remains valid for scoped REST access. Uninstall the extension or revoke the key under **Admin → API keys** to remove REST access. | `ext.deactivate` |
| **Uninstall** | subscriptions removed, API key revoked, extension rows deleted. Content types, their content and settings are **kept** unless **purge** is chosen, which hard-deletes every item of the declared types, the types, the `ext.<slug>.*` settings and the package directory. | `ext.uninstall` |

Reinstalling a slug (after an uninstall without purge) reuses the kept
types as they are: admin edits made to them since are not overwritten; only
types that do not exist yet are created.

After 20 consecutive failures an extension switches to status
`error`. A filter call counts as one failure when it fails; an action or
event delivery counts as **one** failure only once its last (5th) attempt
has failed, so a short outage absorbed by retries does not trip the
threshold. A success resets the counter. On `error`, the extension is audited as `ext.error`, delivery stops, System Health reports it,
and re-activating resets the counter. Active extensions are resubscribed at
boot.

## HTTP bridge protocol

Every delivery is a `POST` to `endpoint.url`:

```
POST /gorun HTTP/1.1
Content-Type: application/json
X-GoRun-Extension: hello
X-GoRun-Timestamp: 1700000000
X-GoRun-Delivery: 01JABCDEF...
X-GoRun-Signature: <hex hmac>

{"hook":"content.published","kind":"event","payload":{…},
 "site":{"base_url":"https://example.com","name":"My site"},
 "settings":{"greeting_word":"hello"},
 "ts":1700000000,"delivery_id":"01JABCDEF..."}
```

- `payload` is the hook payload as JSON (for `content.*` hooks: the content
  item; for `content.before_save`: the input being saved).
- `settings` holds the extension's own settings.
- `ts` in the body equals the `X-GoRun-Timestamp` header (unix seconds).
- `delivery_id` is stable across retries of the same action/event: use it to
  de-duplicate (delivery is at-least-once).

### Signature

```
X-GoRun-Signature = hex( HMAC-SHA256( secret, X-GoRun-Timestamp + "\n" + raw_body ) )
```

`secret` is the string shown at install, used as-is (its bytes are the key).
Verify in constant time and reject timestamps more than a few minutes old.
Test vector: secret `topsecret`, timestamp `1700000000`, body `{"a":1}` gives
`5a5686462a74a9b13d69e0cde0a0b3a76f9c2c54dc84d1ca8d3a0019c1e48d1c`:

```sh
printf '1700000000\n{"a":1}' | openssl dgst -sha256 -hmac topsecret
```

Go:

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write([]byte(r.Header.Get("X-GoRun-Timestamp") + "\n"))
mac.Write(body)
ok := hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-GoRun-Signature")))
```

### Kinds

| Kind | Fired by | Delivery | Response |
|---|---|---|---|
| `filter` | `hooks.Apply` (e.g. `content.before_save`) | **synchronous**, within `timeout_ms` | 2xx JSON `{"value": …}` is **merged** onto the value: it is decoded on top of a copy of the original, so you may return a partial object (`{"value":{"title":"New"}}`) and every key you omit keeps its value (arrays and scalars you return replace the original ones). Missing/`null` `value`, or the value echoed back unchanged = unchanged. Timeout, transport error, non-2xx or bad JSON → the **original value is kept**, the failure is logged and counted; the request in GoRun is never failed by an extension. |
| `action` | `hooks.Do` (e.g. `content.after_save`) | **async** job `ext.deliver` (10 s timeout, 5 attempts with backoff) | any 2xx; body ignored |
| `event` | `hooks.Emit` (e.g. `content.published`, `media.uploaded`) | **async** job `ext.deliver` (same) | any 2xx; body ignored |

Responses over 1 MB are treated as failures. Deliveries whose envelope
would exceed 1 MB are skipped (logged as `ext.deliver.too_large` and shown in
the delivery log) and not counted as endpoint failures; a skipped filter
keeps the original value. Queued deliveries for an
extension that was deactivated, uninstalled or put in `error` are dropped.
Every attempt is recorded in the delivery log (last 200 kept, last 50 shown
in the admin: hook, result, HTTP status, duration, error).

## Trust model

Installing an extension is a trust decision, comparable to installing a
WordPress plugin that phones home:

- **Hooks deliver full payloads regardless of the key's scopes.** An
  extension subscribed to `content.after_save` receives every saved item,
  drafts and private items included, even if its API key only has
  `content.view` or no key at all. `permissions` limit what the extension
  can *do* through the REST API, not what hooks *show* it.
- **Filters can rewrite data on its way into GoRun** (e.g. every saved
  title and body for `content.before_save`). GoRun still validates and
  sanitizes the filtered value like any other input.
- Install only extensions you trust, served from endpoints you control, over
  HTTPS. Review the manifest's hooks and permissions on the install page.
- Rotate by reinstalling (new secret and key); uninstalling revokes the key.

Payloads of the core hooks (JSON as delivered in `payload`):

| Hook | Kind fired | Payload |
|---|---|---|
| `content.before_save` | filter | the content input being saved: `type, slug, title, excerpt, body, fields, author_id, status, parent_id, menu_order, template, featured_media_id, seo, locale, publish_at, terms` |
| `content.after_save` | action | the saved content item (input fields plus `id`, `body_format`, `published_at`, `created_at`, `updated_at`) |
| `content.published` | event | the content item that went live |
| `content.deleted` | event | `{id, type, slug, hard}` |
| `media.uploaded` / `media.deleted` | event | the media record |
| `taxonomy.term_saved` | action | the term |
| `user.login` | event | `{user_id, method}` |
| `site.form.submitted` | event | the contact form submission (name, email, message: personal data) |
| `redirects.saved`, `menus.saved` | event | `{action, id, …}` summaries |
| `commerce.order.paid`, `commerce.entitlement.activated` | event | the order / entitlement (when the commerce module is installed) |
| `migration.item.imported` | event | the imported item summary (wpmigrate) |
| `theme.render.context`, `menus.resolve_url`, `seo.sitemap_entry`, `system.health.checks` | filter | internal render/request structures: possible but rarely useful over HTTP and on the hot path |

`admin.nav` and `api.response` appear in the design spec but are not fired in v0.1, so subscribing to them
delivers nothing. Modules may add hooks; **System → Health** lists the hook names that currently have
subscribers (core modules and active extensions). Event payloads are what each module
passes to `hooks.Emit`; check the module's code for exact fields.

## Calling GoRun (inbound)

Extensions call the normal REST API (`/api/v1/*`, see `/api/docs`) with the
key shown at install:

```
Authorization: Bearer gp_…
```

The key carries exactly the manifest `permissions` as scopes and never
exceeds its owner's role (the admin who installed it). Nothing else is
needed: there is no extension-only API. An extension with no `permissions`
gets no key. Uninstalling revokes it.

## Admin and API

- **System → Extensions**: list (status, version, hooks, failures), zip
  upload, packages found in the extensions folder, per-extension page
  (manifest, hooks, permissions, types, links, settings, delivery log,
  activate/deactivate, uninstall with optional purge). Requires
  `extensions.manage`.
- REST (scope `extensions.manage`): `GET /v1/extensions`,
  `GET /v1/extensions/{slug}`, `POST /v1/extensions/{slug}/activate`,
  `POST /v1/extensions/{slug}/deactivate`,
  `DELETE /v1/extensions/{slug}?purge=true|false`. Installing is admin-only
  (the secret and key are revealed once, in the browser).
- System Health shows an `extensions` check that fails while any extension
  is in `error`.

## The hello example

`examples/extensions/hello-http` verifies signatures, logs actions and
events, and implements the `content.before_save` filter by appending
` — via hello` to the title.

1. Zip `examples/extensions/hello-http/manifest.json` (or copy the folder to
   `<DataDir>/extensions/hello/`) and install it in the admin; copy the
   secret. The manifest points at `http://127.0.0.1:8787/gorun`, so run
   GoRun with `GORUN_ALLOW_PRIVATE_NET=1` for this local test.
2. Run it: `HELLO_SECRET=<secret> go run ./examples/extensions/hello-http/cmd/hello-http`
3. Activate it, then save a post: its title ends with ` — via hello`, and the
   example logs `content.after_save` / `content.published`.

GoRun's own tests mount `hello.Handler(secret)` behind `httptest` to check
the whole round trip.

## Limits (v1)

- HTTP bridge only (gRPC later); one endpoint per extension; `hmac` auth only.
- `assets/` is stored but not served.
- `admin_nav` entries appear on the extension's admin page, not in the
  sidebar.
- Filters add their round-trip latency to the request that fires them; keep
  `timeout_ms` small and endpoints close.
- Updating an extension = uninstall (without purge) then install the new
  version: kept types and settings are reused, a new secret and key are
  issued.
