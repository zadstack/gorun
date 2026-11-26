# GoRun MCP server

> **ملخص بالعربية.** يأتي GoRun بخادم MCP رسمي يتيح لوكلاء الذكاء الاصطناعي (Claude Code وClaude Desktop
> وأي عميل MCP) قراءة الموقع وتعديله عبر الخدمات والصلاحيات والتحقق نفسها التي تستخدمها لوحة التحكم وواجهة
> REST. الاتصال عبر HTTP على `POST /mcp` أو عبر stdio بالأمر `gorun mcp`. المصادقة بمفاتيح API فقط
> (`Authorization: Bearer gp_…`)، وأنشئ للوكيل مفتاحًا من نوع «وكيل ذكاء اصطناعي» من
> **وكلاء الذكاء الاصطناعي ← مفاتيح API** بأقل النطاقات اللازمة، فهذا النوع يعمل على MCP فقط وترفضه `/api`.
> الأدوات الحساسة (النشر وإلغاؤه والحذف وتعديل الإعدادات وتعديل المحتوى المنشور) لا تُنفَّذ مباشرة بل تنتظر
> موافقة بشرية من **وكلاء الذكاء الاصطناعي ← الموافقات**، ويستعلم الوكيل عن النتيجة بالأداة `check_approval`.
> الموافقة بمفتاح API تتطلب شخصًا آخر: لا يقرر المفتاح طلباته ولا طلبات مفتاح آخر لمالكه نفسه، إلا إذا فعّل موقع
> بمشغّل واحد ذلك صراحة بـ`GORUN_MCP_SAME_OWNER_APPROVAL=true`.
> كل استدعاء يُسجَّل في سجل التدقيق مع بصمة المعاملات فقط.
> ويقدّم الخادم أيضًا **قوالب أوامر** (prompts) ثنائية اللغة تشرح للوكيل الأدوات وترتيبها (`draft_post` و`translate_item`
> و`seo_review` و`site_overview`)، و**إكمالًا تلقائيًا** (completions) لقيم مثل أنواع المحتوى والتصنيفات والمعرّفات النصية،
> و**قوالب موارد** مثل `gorun://content/{type}/{slug}`؛ وكلها تخضع لنطاقات المفتاح نفسها.

## Operator overview / نظرة للمشغّل

| Step | Action |
| --- | --- |
| 1 | Create an **AI agent** key under **AI Agents → API keys** with the minimum scopes (never `*` on production keys). |
| 2 | Point the MCP client at `https://<your-site>/mcp` with `Authorization: Bearer gp_…`, or run `gorun mcp --key gp_…` (stdio). |
| 3 | Humans decide sensitive calls under **AI Agents → Approvals**; the agent polls `check_approval`. |
| 4 | Use a separate **REST API** key for `/api/v1` scripts; agent keys are refused there (`403 agent_key_not_allowed`). |
| 5 | Audit every tool call under **AI Agents → Audit**. Canvases that run agents: [CANVASES.md](CANVASES.md). |

| الخطوة | الإجراء |
| --- | --- |
| 1 | أنشئ مفتاح **وكيل ذكاء اصطناعي** من **وكلاء الذكاء الاصطناعي ← مفاتيح API** بأقل النطاقات. |
| 2 | وجّه العميل إلى `https://<موقعك>/mcp` أو شغّل `gorun mcp`. |
| 3 | راجع الطلبات الحساسة من **الموافقات**. |
| 4 | مفاتيح REST منفصلة لـ`/api/v1`. |
| 5 | راجع **التدقيق**. اللوحات: [CANVASES.md](CANVASES.md). |

Common failures (wrong key kind, stuck approvals, same-owner rules): [TROUBLESHOOTING.md](TROUBLESHOOTING.md). Docs index: [README.md](README.md).

GoRun ships an official [Model Context Protocol](https://modelcontextprotocol.io) server so AI agents
(Claude Code, Claude Desktop, any MCP client) can read and edit the site through the same services,
permissions and validation as the admin and the REST API.

- **Streamable HTTP:** `POST https://<your-site>/mcp` (stateless, JSON responses).
- **stdio:** `gorun mcp --key gp_…`, or set `GORUN_MCP_KEY` (one session per process).
- **Instructions:** a short routing guide sent at `initialize` (start with `get_site_structure`, drafts first,
  approvals and `check_approval`, paging, error codes, toolsets). The same text is the `gorun://guide` resource.
- **Resources:** `gorun://guide` (routing guide, any key), `gorun://types` (content types and field schemas),
  `gorun://site` (site structure), `gorun://canvases/{id}` (grant-scoped canvas board; same payload as `get_canvas`).
  Resource templates: `gorun://content/{type}/{slug}{?locale}`, `gorun://content/{type}/{slug}/{locale}`,
  `gorun://media/{id}`, `gorun://menu/{slug}` ([Resource templates](#resource-templates)).
- **Prompts:** `draft_post`, `translate_item`, `seo_review`, `site_overview`, bilingual step-by-step plans
  ([Prompts](#prompts)).
- **Completions:** `completion/complete` for prompt arguments and template variables ([Completions](#completions)).
- **Output schemas:** none yet.

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
API-key budgets are in memory, are not shared across serving processes and reset on restart
(or a new stdio session). HTTP limiters lazily discard fully refilled idle buckets. Login
lockouts are stored separately in SQLite and survive restarts.

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
| `comments.moderate` | `list_comments`, `moderate_comment` (approving needs approval), `delete_comment` (sensitive) |
| `themes.manage` | List and read templates, save template **drafts** |
| `audit.read` | `get_logs` (own audit rows only; IPs omitted) |
| `system.health` | Admin health screens and REST job/audit listings (not `get_logs`) |
| `settings.manage` | `update_settings` (sensitive; site identity and SEO keys only, see below); `list_services`, `get_service`, `set_service` (sensitive) |
| `system.capability_test` | `test_service` |
| `migrations.run` | Migration preview and status (when the WordPress migrator is installed) |
| `commerce.manage` | `list_products` (when commerce is installed) |
| `mcp.approve_self` | Sensitive tools run immediately, with no human approval. It must be listed explicitly in the key's scopes: `*` does **not** imply it. |
| `mcp.approve` | Decide approvals over REST. It must be listed explicitly in the key's scopes (`*` does not count). A key can never decide calls it requested itself, nor calls requested by another key of the same owner (see below). |
| `canvas.read` | `list_canvases`, `get_canvas`, `gorun://canvases/{id}`, and selected-board ops (`list_canvas_pending_runs`, `get_canvas_context`, `get_canvas_source`). Agent keys also need an explicit board grant. |
| `canvas.append` | `append_canvas_result` (append a private result note; never publishes) |
| `canvas.run` | `request_canvas_run` (creates a `requested` run for human review; does not execute) |
| `workagents.read` / `workagents.channels` | `list_channels`, `read_channel_messages` / `post_channel_message` ([AGENT-CHANNELS.md](AGENT-CHANNELS.md)) |

**Create MCP keys with only the scopes the agent needs, and never `*`.**

`tools/list` returns only the tools your key may call. Calling any other tool returns the error code
`forbidden`.

## Toolsets and read-only mode

Every tool belongs to one toolset. A client can ask for fewer tools (smaller `tools/list`, fewer wrong
picks) without a new key:

| Narrowing | HTTP (`/mcp`) | stdio (`gorun mcp`) |
|---|---|---|
| Only some toolsets | `?toolsets=content,media` or header `X-MCP-Toolsets: content,media` | `--toolsets content,media` |
| Only read-only tools | `?readonly=1` or header `X-MCP-Readonly: 1` | `--readonly` |

Without these parameters the list is exactly the scope-filtered list. The query parameter wins over the
header. `check_approval` and `get_site_structure` are always listed (if the key's scopes allow them).
Scopes still apply inside a view. A tool hidden by the view is refused on call with `forbidden`, like a
tool outside the key's scopes. An unknown toolset name or `readonly` value answers HTTP 400 (stdio: the
command exits with an error). Example: `claude mcp add --transport http gorun "https://your-site.example/mcp?toolsets=content,media&readonly=1" --header "Authorization: Bearer gp_…"`.

| Toolset | Tools |
|---|---|
| `content` | `list_content`, `get_content`, `create_content`, `update_content`, `publish_content`, `unpublish_content`, `delete_content` |
| `types` | `list_content_types`, `get_content_type`, `create_content_type`, `update_content_type_fields` |
| `translations` | `list_translations`, `create_translation`, `link_translation`, `unlink_translation` |
| `comments` | `list_comments`, `moderate_comment`, `delete_comment` |
| `media` | `get_media`, `list_media`, `upload_media`, `update_media` |
| `taxonomy` | `list_taxonomies`, `list_terms`, `create_term` |
| `templates` | `list_templates`, `get_template`, `create_template_draft` |
| `site` | `get_site_structure`, `get_logs` |
| `settings` | `update_settings` |
| `services` | `list_services`, `get_service`, `set_service`, `test_service` |
| `migration` | `preview_migration`, `get_migration_status` (WordPress migrator installed) |
| `commerce` | `list_products` (commerce installed) |
| `canvas` | `list_canvases`, `get_canvas`, `list_canvas_pending_runs`, `get_canvas_context`, `get_canvas_source`, `request_canvas_run`, `append_canvas_result` |
| `builder` | `get_section_schema`, `create_purpose_page`, `get_page_layout`, `plan_page_layout`, `save_page_layout`, `publish_page_layout`, `list_page_layout_revisions`, `get_page_layout_revision`, `restore_page_layout_revision` |
| `channels` | `list_channels`, `read_channel_messages`, `post_channel_message` (agent channels installed) |
| `approvals` | `check_approval` |
| `other` | tools that modules or extensions register without a toolset |

## Tool annotations

Every tool carries MCP annotations so clients can label it and decide how much confirmation a call needs:
`title`, `readOnlyHint` (never changes anything), `destructiveHint` (may delete or overwrite existing data;
only on write tools), `idempotentHint` (repeating the same call changes nothing more) and `openWorldHint`
(reaches outside GoRun: `upload_media` downloading a URL, `preview_migration` reading the WordPress site).
The lists after the tool table name the read-only and destructive tools. Annotations are hints for the
client; the server-side gates (scopes, approvals, grants) are what enforce access.

## Tools

Every tool returns the REST envelope as JSON text and as structured content:
`{"ok":true,"data":…}` on success, or `{"ok":false,"error":{"code","message","fields"}}` with
`isError: true` on failure (codes: [Error codes](#error-codes)). Input schemas (JSON Schema 2020-12) come from
the tool's Go input struct and use snake_case property names. At every level, properties without `omitempty`
are `required` (the same rule as the REST OpenAPI document), and fixed value sets are real `enum`s (content
and comment statuses, filter operators, order direction, field types, builder purposes and section options).

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
| `list_terms` | content.view | | Terms of a taxonomy; optional `locale` (one content language) and `translation_group` (a term and its translations). Each term returns `locale` and `translation_group` |
| `create_term` | taxonomy.manage | | New term; optional `locale` (default the site language) and `translation_of` (id of a term of the same taxonomy in another language: the new term becomes its translation, the parent maps to the parent's translation). See [term translations](CONTENT_STUDIO.md#term-translations) |
| `get_media` | content.view | | One media item by `id`, including dimensions and responsive image `variants` |
| `update_media` | media.upload | **yes** | Patch `alt`, `caption`, `title` by `id`, or set `focal` (`{x, y}`, each 0–1) or `clear_focal`. Missing/null fields stay unchanged; `""` clears. At least one change is required. |
| `list_templates` | themes.manage | | Theme templates and saved overrides |
| `get_template` | themes.manage | | Published `body`, `source`, and saved `draft` (null when absent); draft-only templates are readable |
| `create_template_draft` | themes.manage | **yes** | Approval saves an override **as a draft only**. A human previews and publishes it in Appearance → Templates. Use the authenticated admin preview to render Go template instructions before publication. |
| `get_logs` | audit.read | | Own audit tail only (`limit` ≤ 200, optional `action`; IPs stripped). Site-wide failed/dead jobs stay on `system.health` (admin / `GET /api/v1/jobs`). |
| `update_settings` | settings.manage | **yes** | `{"settings":{"site.title":"…"}}`. Allowed keys: `site.title`, `site.tagline`, `site.locale`, `site.timezone`, `site.logo`, `site.logo_dark`, `site.logo_light` and `site.icon` (each an existing media id or empty), `contact.email`, `site.posts_per_page` (1–100; a number or numeric string, stored as a number), `seo.title_template`, `seo.default_description`, `seo.default_og_image`, `seo.robots_txt`. String values, validated with the same rules as the admin pages. All keys are saved in one transaction, or none are. `site.content_locales`, `site.base_url`, `site.theme` and `site.front_page` stay admin-only. A `site.locale` update must select an already configured public content language and is checked inside the write transaction. |
| `list_services` | settings.manage | | The five services (mail, storage, chat, analytics, backups) with their status: `state` (`off`, `configured`, `incomplete`), `missing`, `fields`, `secret_set`. Never a secret's value ([SERVICES.md](SERVICES.md)) |
| `get_service` | settings.manage | | One service's status by `name` |
| `set_service` | settings.manage | **yes** | `{"name":"mail","values":{"host":"…","port":465}}`: changes only the given fields (strings, numbers or booleans), validated like the admin; nothing is saved if one is invalid. A key naming a secret (`password`, `api_key`, `keys`), or any key that is not one of the service's fields (`secret_key`, `resend_api_key`, …), is refused with `validation_failed` before the call is parked, so no secret is ever stored with an approval or shown in its preview. Answers `{"status":…}`, plus `needs_secret` (`link`, `expires_at`, `cli`) when the service is `incomplete` for want of its secret: the [one-time link](#one-time-secret-link) is made for the key's owner and is absolute on the site's base URL. Values that can only be saved with the secret (storage on a new bucket) are not saved: they are held with the link and applied with the secret (`pending: true`). An approved call's stored result (and `check_approval`) has the link redacted, with a `note` to call `set_service` again for a fresh one. The approvals page shows the fields and their new values. Audited `service.update` with `via: mcp` |
| `test_service` | system.capability_test | | The service's safe test by `name`: mail sends one test message to the key owner's own address; chat and storage say `no test` |
| `preview_migration` | migrations.run | | Dry-run report of a WordPress migration (only if wpmigrate is installed) |
| `get_migration_status` | migrations.run | | Migration progress (only if wpmigrate is installed) |
| `list_products` | commerce.manage | | Store products (only if commerce is installed) |
| `list_canvases` | canvas.read, content.view | | Boards the key may read: owner boards for REST keys, or grant-listed boards for agent keys. Returns `{id,title,updated_at,version}` only (`page` / `per_page` ≤ 100) |
| `get_canvas` | canvas.read, content.view | | Board by `id`: `{id,title,version,updated_at,cards[],edges[]}`. Agent keys receive only grant-allowed cards and edges between them. No inbound secrets or provider credentials |
| `list_canvas_pending_runs` | canvas.read, content.view | | Waiting runs for this agent key on a granted board (`board_id`) |
| `get_canvas_context` | canvas.read, content.view | | Bounded prompt/context for one delegated run (`board_id`, `run_id`). Each source starts with `## [ref:content/<id>] <title>` or `## [ref:note/<card id>] <title>`; a shortened source ends with `[… N more characters. Full text: ref:…]`. Also returns `refs` and `spilled` |
| `get_canvas_source` | canvas.read, content.view | | Full current text of one ref from that run's context (`board_id`, `run_id`, `ref`), at most 200,000 characters (`characters` returned, `total_characters` full length, `truncated` when cut). Other refs are refused |
| `append_canvas_result` | canvas.append, content.view | | Append a private result note for a waiting run (idempotent; never publishes) |
| `request_canvas_run` | canvas.run, content.view | | Request a run (`requested`) for human review (`board_id`, `node_id`, `request_id`) |
| `list_channels` | workagents.read | | This owner's open agent channels |
| `read_channel_messages` | workagents.read | | `{channel_id, after?, limit?}`: messages oldest first |
| `post_channel_message` | workagents.channels | | Post as the calling agent key (`{channel_id, body, target_member_id?}`) |
| `check_approval` | any key | | Status of a sensitive call (see below) |

Canvas operation errors: a missing, changed or revoked grant, consent, run, review or `context_token` returns
`forbidden` (one generic message, so other owners' boards and runs stay invisible); unknown arguments, a
non-agent card for `request_canvas_run` or a malformed request return `validation_failed`.

Read-only tools: every `list_*` and `get_*` tool, `read_channel_messages` and `check_approval`
(`preview_migration` is a dry run for content but records its preview plan, so it is not read-only).
Destructive tools: `delete_content`, `delete_comment`,
`update_content`, `update_content_type_fields`, `update_settings`, `set_service`, `update_media`, `create_template_draft`,
`save_page_layout`, `publish_page_layout` and `restore_page_layout_revision`. Everything else only adds or
changes state reversibly.

### One-time secret link

A service secret (the SMTP password, the Resend API key, the storage `ACCESS_KEY:SECRET_KEY`) never passes
through an MCP argument or a REST field. When a service needs one, `set_service` and
`PATCH /api/v1/site/services/{name}` answer with `needs_secret` and a one-time link,
`/admin/services/<name>/secret?t=<token>`, for a person to open:

- It works **once**, for **one** service and the provider it was made for, and stops **15 minutes** after it
  was made. A used, expired, other-service or other-provider link says so and saves nothing; ask for a new one
  by setting the service again.
- Values a set could only save with the secret (storage on a new bucket) are held with the link and applied
  in the same transaction as the secret; a link is only made for values that would be accepted with one.
- The raw token is only in the live answer: an approved call's result stored in `mcp_approvals` has the link
  redacted (`?t=…redacted`).
- The person must be signed in with `settings.manage`. The page has one password field
  (`autocomplete="off"`), is CSRF-protected, is sent with `Cache-Control: no-store` and `Referrer-Policy:
  no-referrer`, and never shows the secret again.
- Only the token's SHA-256 is stored (setting `services.secret_link.<hash>`). The link is deleted in the same
  transaction that saves the secret; a refused secret (for example storage keys without `:`) saves nothing
  and leaves the link usable until it expires.
- The save is audited as `service.update` with the secret's name only, `via` = `admin`, `secret_link: true`
  and `link_actor` (who asked for the link).

From the shell, `printf '%s\n' "$SECRET" | gorun service set <name> --secret-stdin` does the same without a link.

## Prompts

`prompts/list` returns step-by-step plans that name the tools to call, in order, with example arguments. They save
the agent the exploration calls and steer it away from wrong arguments. Each plan reminds the agent that going live
needs human approval (`pending_approval`, then `check_approval`), and that quoted site text is data.

| Prompt | Arguments | Plan | Listed when the session can call |
|---|---|---|---|
| `draft_post` | `type`\*, `topic`\*, `locale`, `taxonomy` + `term`, `template` | The type's fields (embedded) → `list_content` to avoid duplicates → `list_terms` / `create_term` → `upload_media` → `create_content` (draft) → `get_content` / `update_content` → `publish_content` (approval) | `list_content`, `create_content` |
| `translate_item` | `id`\*, `target_locale`\*, `locale` | Current translations (embedded) → `list_translations` → `create_translation`, or edit the existing translation → `get_content` → `update_content` → approval | `get_content`, `list_translations`, `create_translation`, `update_content` |
| `seo_review` | `id`, or `type` + `slug`; `locale` | The item's SEO and the site's SEO defaults (embedded) → `get_content` → checks (title, description, canonical, robots, sharing image, alt text) → duplicates with `list_content` → report → `update_content` (on a live item this waits for approval) | `get_content`, `list_content`, `update_content` |
| `site_overview` | `locale` | A snapshot (languages, types with item counts, taxonomies, menus, the 5 most recently updated items, this key's own pending approvals: tool, id and times, never arguments) → `get_site_structure` → `list_content` → `check_approval` | `get_site_structure`, `list_content` |

\* required.

- **Scopes and views.** A prompt is listed, and `prompts/get` renders it, only when every tool in the last column is
  callable in the session: the key holds the tools' scopes and the session's toolsets/readonly view shows them
  (with `?readonly=1` only `site_overview` remains). Optional steps (`create_term`, `upload_media`, `get_media`,
  `publish_content`, `update_settings`) appear only when that tool is callable too; without `publish_content` the
  plan says a human publishes. Data in a prompt is read as the key, with the same read rules as the tools.
- **Language.** `locale` picks the language of the text: Arabic for `ar` and `ar-*`, English otherwise. The default
  is the site language (`site.locale`). In `draft_post`, `locale` is also the new item's content language and must be
  one of the site's content languages; `target_locale` must be one too, and differ from the item's language.
- **Errors.** Unknown prompt, missing required argument, unknown argument, unknown type or item, invalid locale:
  JSON-RPC `-32602` with a message naming the problem. A prompt the session cannot use: `-32003` (forbidden). An
  invalid key: `-32001`.

Example:

```json
{"jsonrpc":"2.0","id":1,"method":"prompts/get",
 "params":{"name":"draft_post","arguments":{"type":"post","topic":"Ramadan opening hours","locale":"en","taxonomy":"category","term":"news"}}}
```

returns one `user` message whose text starts with
`Write a new post item about: "Ramadan opening hours"`, lists the type's fields and taxonomies, and continues with
numbered steps such as `create_content {"body":"<p>…</p>","excerpt":"…","locale":"en","status":"draft","terms":{"category":["news"]},"title":"…","type":"post"}`.

## Completions

`completion/complete` suggests values for prompt arguments and resource template variables: at most 20, sorted,
matched by prefix (ignoring case), with `hasMore` when more matched. Earlier choices go in `context.arguments`.

| Argument / variable | Values | Needs |
|---|---|---|
| `type` | Content type slugs | content.view |
| `taxonomy` | Taxonomy slugs (only those of `context.arguments.type`, when given) | content.view |
| `term` | Term slugs of `context.arguments.taxonomy` (and `locale`, when given); none without a taxonomy | content.view |
| `slug` (content) | Slugs of `context.arguments.type` (and `locale`), trash excluded, read through the content service so an author's key never sees another author's drafts | content.view |
| `slug` (`gorun://menu/{slug}`) | Menu slugs | content.view |
| `locale`, `target_locale` | The site's content languages | content.view |
| `template` | Theme template names | themes.manage |

The reference must be a prompt the session can use or a template the key can read. Other references answer
`-32003`; an unknown prompt, template or reference type answers `-32602`. Free-text arguments (`topic`, `id`) get
no values.

```json
{"jsonrpc":"2.0","id":2,"method":"completion/complete",
 "params":{"ref":{"type":"ref/resource","uri":"gorun://content/{type}/{slug}{?locale}"},
           "argument":{"name":"slug","value":"ram"},"context":{"arguments":{"type":"post"}}}}
```

```json
{"jsonrpc":"2.0","id":2,"result":{"completion":{"values":["ramadan-hours","ramadan-menu"]}}}
```

## Resource templates

`resources/templates/list` lists the templates the key can read. A read returns the matching tool's envelope as
`application/json` text: `{"ok":true,"data":…}`, with the same data as the tool.

| URI template | Same as | Needs |
|---|---|---|
| `gorun://content/{type}/{slug}{?locale}` | `get_content` with `type`, `slug` and optional `locale` | content.view |
| `gorun://content/{type}/{slug}/{locale}` | `get_content` with `type`, `slug`, `locale` | content.view |
| `gorun://media/{id}` | `get_media` (metadata and variants, never the file bytes) | content.view |
| `gorun://menu/{slug}` | One menu of `get_site_structure`, with its items | content.view |
| `gorun://canvases/{id}` | `get_canvas` | canvas.read, content.view |

Content reads follow the content service's read rules: with `content.view` a key reads drafts like `get_content`
does, except that an author's or contributor's key reads only its owner's drafts and published public items.
Percent-encode non-ASCII slugs (`gorun://content/post/%D9%85%D8%B1%D8%AD%D8%A8%D8%A7`). Errors: missing item or
unknown URI `-32602` with the message `Resource not found` and `data.uri` (MCP SEP-2164); missing scope or a forbidden item `-32003`; a malformed URI (empty
segment, unknown query parameter such as `?lang=`) `-32602`; an invalid key `-32001`.

`resources/list` is filtered the same way: `gorun://types` and `gorun://site` are listed only for keys with
`content.view`; `gorun://guide` is listed for every key. Resources follow the key's scopes; the toolsets/readonly
view narrows tools and prompts, not resources.

```json
{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"gorun://content/post/hello?locale=en"}}
```

```json
{"jsonrpc":"2.0","id":3,"result":{"contents":[{"uri":"gorun://content/post/hello?locale=en","mimeType":"application/json",
 "text":"{\"ok\":true,\"data\":{\"id\":\"01J…\",\"type\":\"post\",\"slug\":\"hello\",\"locale\":\"en\",\"status\":\"draft\",…}}"}]}}
```

## Deprecated tool names

These names were renamed to one `verb_noun` style. The old names are still accepted on `tools/call` (same
scopes, approvals, toolset filters; audited under the new name) but are no longer listed by `tools/list`.
Switch to the new names.

| Old name (deprecated) | New name |
|---|---|
| `canvases.pending` | `list_canvas_pending_runs` |
| `canvases.context` | `get_canvas_context` |
| `canvases.source` | `get_canvas_source` |
| `canvases.request_run` | `request_canvas_run` |
| `canvases.append_result` | `append_canvas_result` |
| `channels_list` | `list_channels` |
| `channels_read` | `read_channel_messages` |
| `channels_post_message` | `post_channel_message` |
| `run_migration_preview` | `preview_migration` |

## Error codes

Tool errors use these codes. They are stable: new codes may be added, existing ones are not renamed.

| Code | Meaning | What the agent should do |
|---|---|---|
| `validation_failed` | Arguments are invalid; `fields` names the problems | Fix the named fields and call again |
| `not_found` | The id, slug, approval or tool does not exist (or is not visible to this key) | Re-read with a list tool |
| `forbidden` | The key's scopes, a board grant, or the session's toolsets/readonly filter do not allow the call | Do not retry; ask the operator for access |
| `unauthorized` | The API key is no longer valid (stdio: revoked, expired, owner disabled) | Stop; a new key is needed |
| `conflict` | The item changed meanwhile (live edit raced an approval, stale `updated_at`, existing type slug) | Reload, review, then retry |
| `rate_limited` | Too many calls (stdio token bucket; agent channel posting limit) | Wait, then retry. Over HTTP the limit is HTTP 429 with `Retry-After` |
| `fetch_failed` | `upload_media` could not download the URL (unreachable, private address, refused redirect, non-200) | Check the URL, or upload `data_base64` |
| `channel_closed` | The agent channel is closed | Ask the owner for a new channel |
| `member_not_allowed` | The key, or the hand-off target, is not a member of the channel | Use a member id from the channel |
| `interrupted` | Stored in an approval result when an approved call stopped before saving its result | Check whether it took effect before repeating |
| `internal` | Unexpected server error; the message is generic and the details go to the server log | Report it; retry later |

`pending_approval` is **not** an error: a sensitive call returns `ok: true` with
`data.status: "pending_approval"` and an `approval_id`. Poll `check_approval`; the stored result appears once a
human approves (`approved`, or `failed` with the error), or the status becomes `denied` or `expired`.
Transport-level failures use JSON-RPC errors instead: HTTP 401 without a valid bearer key, HTTP 429 when the key's
HTTP rate limit is exceeded, HTTP 400 for an unknown toolset. Prompts, completions and resource reads answer with
JSON-RPC errors too: `-32602` invalid arguments, unknown name or resource not found (message `Resource not found`), `-32003` forbidden,
`-32001` invalid key, `-32603` internal error. (Go SDK clients report `-32003` as `ErrConnectionClosed` wrapping the
message, because that SDK uses the same code internally.)

Canvas boards are **not** public site pages. There is no anonymous HTML route for board bodies.
Simple CMS workspace mode may hide the canvases admin nav; these tools stay available when scopes and
grants allow. Operational contract: [CANVAS-OPERATIONS-CONTRACT.md](CANVAS-OPERATIONS-CONTRACT.md).

Modules and extensions can add their own tools (`Deps.MCP.AddTool`). These tools follow the same rules
for scopes, approval, audit and rate limits.

## Human approval for sensitive tools

1. When an agent calls a sensitive tool with a key that lacks `mcp.approve_self`, the tool does not run.
   GoRun checks the arguments against the tool's input schema (at most 256 KB), stores the call in
   `mcp_approvals` and returns:
   `{"status":"pending_approval","approval_id":"…","expires_at":"…"}`.
2. A user with the `mcp.approve` permission reviews the tool, its arguments (pretty-printed and escaped),
   the key and the requester in **AI Agents → Approvals** (`/admin/agents/approvals`), then approves or
   denies. Over REST, only keys that list `mcp.approve` explicitly can decide, never for calls
   they requested, and never for calls requested by another key of **the same owner**: approval is
   separation by person, not by key (HTTP 403). A signed-in user may still approve calls of their own
   agent keys in the admin; that is the human in the loop. A single-operator site that wants an
   approver key to decide its owner's own agent calls must opt in explicitly with
   `GORUN_MCP_SAME_OWNER_APPROVAL=true` (or `mcp_same_owner_approval = true` in `gorun.toml`); the
   requesting key itself is still refused. The REST API offers the same actions: `GET /api/v1/mcp/approvals`,
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

The admin fills these in for you. Creating a key under **AI Agents → API keys** shows a **Connect your
agent** panel with the three snippets below, ready to copy with this site's URL (the `site.base_url`
setting, else the address you opened the admin on) and the new key. The key is shown only on that
page; afterwards each key's row keeps the same panel with `gp_…` in place of the secret. **Test
connection** in the panel runs MCP `initialize` and `tools/list` as the key inside the site (no HTTP
round trip, no tool call) and reports the server version, the key's kind and scopes, and the toolsets
and tools it can see; on failure it names the cause (revoked, expired, owner disabled, wrong secret,
scopes that reach no tool) with the JSON-RPC code `/mcp` would answer.

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
gorun mcp --toolsets content,media --readonly   # narrow the tool list (see Toolsets)
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

- `get_section_schema`: `{locale?}`; read-only; returns every section kind with the fields, native options, entry fields, layouts and required values it accepts under each library, plus enums and limits; scope `content.view`. Read it before composing a document.
- `create_purpose_page`: `{purpose, title, locale?}`; creates a private draft page for `about`, `service`, `methodology` or `contact` with its preset section structure (the hero takes the title; other sections start empty and hidden) and returns `{id, edit_url, draft}`; scopes `content.create` and `content.edit`. Nothing is published.
- `get_page_layout`: `{id}`; returns the private draft, last-published flags and opaque version token; scope `content.edit`, with owner checks.
- `plan_page_layout`: `{id, document}`; read-only dry run of a save: validates the document, returns what it would change in the current draft as a readable list (`plan`, with the structured `diff`), whether the result could be published, whether the draft is in sync with the published layout, and the `base_updated_at` a following save must carry. Nothing is written. Call it before `save_page_layout` and show the plan to the person; scope `content.edit`.
- `save_page_layout`: `{id, document, base_updated_at}`; imports or updates a private draft without changing the public site; scope `content.edit`.
- `publish_page_layout`: `{id, updated_at}`; scopes `content.edit` and `content.publish`; **sensitive**, so it waits for human approval before replacing public HTML.
- `list_page_layout_revisions`: `{id}`; lists private layout history newest first; scope `content.edit`.
- `get_page_layout_revision`: `{id, revision_id}`; reads one revision document for preview/comparison; scope `content.edit`.
- `restore_page_layout_revision`: `{id, revision_id, updated_at}`; restores a revision into the private draft only; scope `content.edit`; **sensitive** (human approval). Public HTML stays unchanged until `publish_page_layout`.

`publish_page_layout` and `restore_page_layout_revision` also describe themselves: a pending approval of either shows, on `/admin/agents/approvals`, what the call would change right now (the published layout against the draft, or the draft against the revision, section by section), and warns when the parked token no longer matches or the draft cannot be published. The description is computed when the page is viewed, as the person viewing, so it is current and respects their permissions.

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
