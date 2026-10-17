# GoRun MCP server

> **ملخص بالعربية.** يأتي GoRun بخادم MCP رسمي يتيح لوكلاء الذكاء الاصطناعي (Claude Code وClaude Desktop
> وأي عميل MCP) قراءة الموقع وتعديله عبر الخدمات والصلاحيات والتحقق نفسها التي تستخدمها لوحة التحكم وواجهة
> REST. الاتصال عبر HTTP على `POST /mcp` أو عبر stdio بالأمر `gorun mcp`. المصادقة بمفاتيح API فقط
> (`Authorization: Bearer gp_…`)، وأنشئ للوكيل مفتاحًا من نوع «وكيل ذكاء اصطناعي» من
> **وكلاء الذكاء الاصطناعي ← مفاتيح API** بأقل النطاقات اللازمة، فهذا النوع يعمل على MCP فقط وترفضه `/api`.
> الأدوات الحساسة (النشر وإلغاؤه والحذف وتعديل الإعدادات وتعديل المحتوى المنشور) لا تُنفَّذ مباشرة بل تنتظر
> موافقة بشرية من **وكلاء الذكاء الاصطناعي ← الموافقات**، ويستعلم الوكيل عن النتيجة بالأداة `check_approval`.
> كل استدعاء يُسجَّل في سجل التدقيق مع بصمة المعاملات فقط.

GoRun ships an official [Model Context Protocol](https://modelcontextprotocol.io) server so AI agents
(Claude Code, Claude Desktop, any MCP client) can read and edit the site through the same services,
permissions and validation as the admin and the REST API.

- **Streamable HTTP:** `POST https://<your-site>/mcp` (stateless, JSON responses).
- **stdio:** `gorun mcp --key gp_…`, or set `GORUN_MCP_KEY` (one session per process).
- **Resources:** `gorun://types` (content types and field schemas), `gorun://site` (site structure).
- **Prompts:** none in v1.

## Authentication

MCP uses **API keys only**: `Authorization: Bearer gp_…`. Session cookies are not accepted. A request
without a bearer key gets HTTP 401 with a JSON-RPC error body and `WWW-Authenticate: Bearer`.
An invalid, revoked or expired key gets HTTP 401.

Create keys in **AI Agents → API keys** (`/admin/agents/keys`, permission `api_keys.manage`). A key acts
as its owner: it can never do more than the owner's role allows, and only what its **scopes** list
(`*` = all of the owner's permissions).

Keys have a **kind**. That screen creates **AI agent** keys by default. They work on `/mcp` (HTTP and
stdio) and nowhere else: on `/api` they get `403 agent_key_not_allowed`, so an agent cannot call the
REST API, for example to approve its own sensitive calls. **REST API** keys also work on `/mcp`.

**Upgrading:** keys created before key kinds existed are **REST API** keys, and keep working as
before. To lock an existing agent out of `/api`, create a new key of type **AI agent**, switch the
agent's configuration to it, and revoke the old key.

Over stdio the key is checked again before every call (revocation, expiry, owner status), so revoking a
key or disabling its owner ends a running session. The next call returns `unauthorized`.

Rate limits are per key (`rate_per_min`, default 60). Over HTTP, exceeding the limit returns HTTP 429
with `Retry-After`. Over stdio, a per-process token bucket at the key's rate makes the tool call return
a `rate_limited` error. The `/mcp` and `/api` endpoints keep separate budgets for the same key.

DNS-rebinding protection in the SDK is turned off for `/mcp`. Every request needs a bearer key (there
are no ambient credentials), and reverse proxies on the same host connect from 127.0.0.1 using the
public `Host` header.

## Scopes

| Scope | Grants |
|---|---|
| `content.view` | Read types, content, media, taxonomies, site structure, resources |
| `content.create` / `content.edit` | Create drafts / update content (editing a live item needs approval) |
| `content.publish` | `publish_content`, `unpublish_content` (sensitive) |
| `content.delete` | `delete_content` (sensitive) |
| `types.manage` | Create content types, edit their fields |
| `media.upload` | `upload_media`, `update_media` (metadata edits need approval) |
| `taxonomy.manage` | `create_term` |
| `themes.manage` | List and read templates, save template **drafts** |
| `system.health` | `get_logs` |
| `settings.manage` | `update_settings` (sensitive; site identity and SEO keys only, see below) |
| `migrations.run` | Migration preview and status (when the WordPress migrator is installed) |
| `commerce.manage` | `list_products` (when commerce is installed) |
| `mcp.approve_self` | Sensitive tools run immediately, with no human approval. It must be listed explicitly in the key's scopes: `*` does **not** imply it. |
| `mcp.approve` | Decide approvals over REST. It must be listed explicitly in the key's scopes (`*` does not count), and a key can never decide calls it requested itself. |

**Create MCP keys with only the scopes the agent needs, and never `*`.**

`tools/list` returns only the tools your key may call. Calling any other tool returns the error code
`forbidden`.

## Tools

Every tool returns the REST envelope as JSON text and as structured content:
`{"ok":true,"data":…}` on success, or `{"ok":false,"error":{"code","message","fields"}}` with
`isError: true` on failure. Error codes are `forbidden`, `unauthorized`, `not_found`, `validation_failed`,
`conflict`, `rate_limited`, `fetch_failed` (upload from a URL) and `internal`. `internal` errors carry a
generic message, and the details go to the server log. Input schemas (JSON Schema 2020-12) come from the tool's Go
input struct and use snake_case property names.

| Tool | Scope | Sensitive | What it does |
|---|---|---|---|
| `get_site_structure` | content.view | | Identity, types + fields, taxonomies, menus, theme, template names. Call first. |
| `list_content_types` | content.view | | All content types with field schemas |
| `get_content_type` | content.view | | One type (`slug`) |
| `create_content_type` | types.manage | | New type with custom fields (`conflict` if the slug exists) |
| `update_content_type_fields` | types.manage | | Replace a type's field list |
| `list_content` | content.view | | Query DSL: `type`, `status`, `search`, `taxonomy`, `fields` filters (`eq ne gt lt gte lte in like`, indexed fields only), `order`, `page`, `per_page` (≤ 100). `search` is a full-text substring match ranked by relevance unless `order` is given |
| `get_content` | content.view | | By `id`, or `type` + `slug` (+ `locale`) |
| `create_content` | content.create | | Creates a **draft** (or `pending`). Setting a live status is refused. |
| `update_content` | content.edit | **on live items** | Changes only the keys you send. `fields` and `terms` merge key by key. Status may only move between draft and pending; an empty status keeps the current one. Editing a published, private or scheduled item is parked for approval; editing drafts and pending items is not. |
| `publish_content` | content.publish | **yes** | Publish an item |
| `unpublish_content` | content.publish | **yes** | Back to draft |
| `delete_content` | content.delete | **yes** | Trash, or delete permanently with `hard: true` |
| `list_comments` | comments.moderate | | Comments by `status` / `content_id`, without commenter emails |
| `moderate_comment` | comments.moderate | **when approving** | Set `status`: approving publishes visitor text |
| `delete_comment` | comments.moderate | **yes** | Delete a comment permanently |
| `list_translations` | content.view | | The item and its translations, by locale |
| `create_translation` | content.create | | Copy an item into another `locale` as a linked draft |
| `link_translation` | content.edit | **when a linked item is live** | Link `translation_id` (and its translations) to `id`; same type, each locale once |
| `unlink_translation` | content.edit | **when a linked item is live** | Take an item out of its translations |
| `upload_media` | media.upload | | `data_base64` + `filename` (≤ 10 MB) or a public `url` (SSRF-safe: private addresses refused) |
| `list_media` | content.view | | Media library |
| `list_taxonomies` | content.view | | Taxonomies |
| `list_terms` | content.view | | Terms of a taxonomy |
| `create_term` | taxonomy.manage | | New term |
| `get_media` | content.view | | One media item by `id`, including dimensions and responsive image `variants` |
| `update_media` | media.upload | **yes** | Patch `alt`, `caption`, `title` by `id`. Missing/null fields stay unchanged; `""` clears. At least one non-null field is required. |
| `list_templates` | themes.manage | | Theme templates and saved overrides |
| `get_template` | themes.manage | | Published `body`, `source`, and saved `draft` (null when absent); draft-only templates are readable |
| `create_template_draft` | themes.manage | **yes** | Approval saves an override **as a draft only**. A human previews and publishes it in Appearance → Templates. Use the authenticated admin preview to render Go template instructions before publication. |
| `get_logs` | system.health | | Audit tail (`limit` ≤ 200, optional `action`) and failed/dead jobs (no payloads) |
| `update_settings` | settings.manage | **yes** | `{"settings":{"site.title":"…"}}`. Allowed keys: `site.title`, `site.tagline`, `site.locale`, `site.timezone`, `site.logo` (an existing media id), `contact.email`, `site.posts_per_page` (1–100; a number or numeric string, stored as a number), `seo.title_template`, `seo.default_description`, `seo.default_og_image`, `seo.robots_txt`. String values, validated with the same rules as the admin pages. All keys are saved in one transaction, or none are. `site.base_url`, `site.theme` and `site.front_page` stay admin-only. |
| `run_migration_preview` | migrations.run | | Dry-run report of a WordPress migration (only if wpmigrate is installed) |
| `get_migration_status` | migrations.run | | Migration progress (only if wpmigrate is installed) |
| `list_products` | commerce.manage | | Store products (only if commerce is installed) |
| `check_approval` | any key | | Status of a sensitive call (see below) |

Modules and extensions can add their own tools (`Deps.MCP.AddTool`). These tools follow the same rules
for scopes, approval, audit and rate limits.

## Human approval for sensitive tools

1. When an agent calls a sensitive tool with a key that lacks `mcp.approve_self`, the tool does not run.
   GoRun checks the arguments against the tool's input schema (at most 256 KB), stores the call in
   `mcp_approvals` and returns:
   `{"status":"pending_approval","approval_id":"…","expires_at":"…"}`.
2. A user with the `mcp.approve` permission reviews the tool, its arguments (pretty-printed and escaped),
   the key and the requester in **AI Agents → Approvals** (`/admin/agents/approvals`), then approves or
   denies. Over REST, only keys that list `mcp.approve` explicitly can decide, and never for calls
   they requested (HTTP 403). The REST API offers the same actions: `GET /api/v1/mcp/approvals`,
   `POST /api/v1/mcp/approvals/{id}/approve`, `POST /api/v1/mcp/approvals/{id}/deny`.
3. **Approve** runs the call **once** with the stored arguments, as the requesting key
   (`auth.WithAPIKeyActor`: the key's scopes and its owner's role apply, and the `mcp.call` audit row
   names the key). At that moment the key must still be active and its scopes must still cover the tool. The result is stored.
   If the call errors, or the key was revoked in the meantime, the status becomes `failed` and the error
   is stored. Approving again, or approving after a denial or expiry, does nothing; the admin page says
   the call was already decided.
4. The agent polls `check_approval` with `{"approval_id":"…"}`:
   - `pending_approval` while waiting
   - `approved`, with `result`, which is the stored envelope
   - `failed`, with `result` holding the error
   - `denied`
   - `expired`, for calls left undecided for 24 hours. A pending call past its expiry reads as expired
     at once; an hourly job also updates the stored status.

   A key can only check its own approvals. Any other ID returns `not_found`.

**Scope of the gate.** Approval gates MCP tools only. A **REST API** key that also calls `/api/v1/...`
directly is not gated there: its scopes alone decide. That is why agents should get **AI agent** keys, which
the REST API refuses with `403 agent_key_not_allowed`, and only the scopes they need.

System Health shows the number of pending approvals. It warns when an approval is past its expiry,
which means the expiry job is not running.

## Audit

Every tool call writes one `audit_log` row with action `mcp.call`, target `tool:<name>`, and the
calling API key as actor. Its meta is `{args_hash, ok, duration_ms, error_code?, approval_id?, pending?}`.
Arguments themselves are never logged, only their SHA-256, so `upload_media` file data never reaches the
log. An approved execution is logged again with `executed: true`, `approval_id`, `api_key_id` and
`approved_by`. Decisions are audited as `mcp.approval.approve` and `mcp.approval.deny`.

## Client configuration

### Claude Code (HTTP)

```sh
claude mcp add --transport http gorun https://your-site.example/mcp \
  --header "Authorization: Bearer gp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

### Claude Desktop (stdio)

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "gorun": {
      "command": "/usr/local/bin/gorun",
      "args": ["mcp", "--key", "gp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"],
      "env": { "GORUN_DATA": "/var/lib/gorun", "GORUN_THEMES": "/opt/gorun/themes" }
    }
  }
}
```

### stdio command

```sh
gorun mcp --key gp_…
GORUN_MCP_KEY=gp_… gorun mcp   # keeps the key out of the process list
```

This boots GoRun against the configured data directory (same database as `gorun serve`), validates
the key and serves one MCP session over stdin/stdout. Logs go to stderr.

Like every command that boots GoRun (`serve`, `mcp`, `import-wxr`, `reindex`), it needs the themes
directory: set `GORUN_THEMES` (as in the Claude Desktop config above), or start it from the repository
root or the container's `/app`. Without it the process exits at once with "themes directory not found".

The stdio process starts **no job workers or cron** of its own: it is meant to run beside `gorun
serve`, whose workers execute the jobs the session enqueues. Pass `--jobs` only when no server runs on
that data directory: a process with workers takes the exclusive `<data>/gorun.lock`, so `gorun mcp --jobs`
beside a running server exits with "data directory is in use by another GoRun process". The old `--no-jobs` flag is still accepted and does nothing.

### Any streamable-HTTP client

Send JSON-RPC to `POST /mcp` with `Content-Type: application/json`,
`Accept: application/json, text/event-stream` and the bearer header. The server is stateless and
ignores `Mcp-Session-Id`. `GET /mcp` and `DELETE /mcp` return 405.

## Structured page builder tools

- `get_page_layout`: `{id}`; returns the private draft, last-published flags and opaque version token; scope `content.edit`, with owner checks.
- `save_page_layout`: `{id, document, base_updated_at}`; imports or updates a private draft without changing the public site; scope `content.edit`.
- `publish_page_layout`: `{id, updated_at}`; scopes `content.edit` and `content.publish`; **sensitive**, so it waits for human approval before replacing public HTML.

Pass the latest draft `updated_at` from get/save to the next operation. A conflict requires reloading the layout and reviewing the newer work. A portable document uses `version: 1` and ordered sections; copied media URLs still need their corresponding assets.

## Editor integration update (MCP implementation 1.1.0)

`get_template` keeps the existing `name` and live `body` fields and adds `source` and
`draft`, matching REST `GET /api/v1/templates/{name}`. An absent draft is `null`;
an intentionally empty draft is `""`. Reading a draft never publishes it.

`create_template_draft` requires a non-null string `body`. Missing/null bodies
fail without replacing the saved draft; an explicit empty string is valid.
Arguments containing unknown fields or trailing JSON are rejected. Template
writes still require approval unless the key explicitly has `mcp.approve_self`.
Publishing remains a human admin/REST operation, not an automatic MCP tool.

Example tool arguments:

```json
{"name":"parts.footer.html","body":"<footer>{{.Site.Title}}</footer>"}
```

Read back with `get_template` and `{"name":"parts.footer.html"}` before asking the
operator to preview and publish. For media, call `get_media` with `{"id":"MEDIA_ID"}`;
`update_media` with `{"id":"MEDIA_ID","alt":"وصف الصورة"}` changes only alt text.
A pending approval is not evidence that metadata was changed: poll `check_approval`.
