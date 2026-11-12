# Content studio

The native content editor includes a studio below its form for every saved content type with body support. It reads the current unsaved title and text. Candidates belong to the signed-in editor and content item. Studio actions never save or publish the article. Text insertion/replacement changes only the unsaved editor; replacement requires explicit confirmation. Removing a candidate keeps its imported Media file.

## Providers and declarative skills

Open **Settings → AI → Content studio**, or the studio's Providers & skills link.

- Text reuses the configured encrypted OpenAI-compatible or Anthropic writing provider.
- Native images require an explicit `image_model` and an OpenAI-compatible endpoint that implements `POST /images/generations`. One 1024×1024 image is requested. The endpoint must return base64 image data or a downloadable HTTPS URL.
- Video and render use explicitly configured named MCP connections. There is no assumed generic video API and no deprecated OpenAI Videos adapter.
- Several MCP bindings may be configured for each task. The editor chooses one per generation. Each binding declares a capability, exact discovered tool, JSON argument template and output mapping.
- Presets are editable prompt data, never executable files. They can be added, changed or removed in the configuration. An editor can customize the prompt before each operation.

Save credentials only in encrypted Connections settings. Select an MCP connection and click **Connect & list tools only**. This executes initialize/notification/tools-list, not generation. Review its actual schema, then configure the exact tool name and JSON arguments. The setup screen includes a field reference, not a working provider example. Allow only tools intended to return draft text or media; tool invocation itself may consume provider credits. Never allow payment, publication or messaging tools here.

Template values support `{{prompt}}`, `{{title}}`, `{{text}}` and, for status requests, `{{job_id}}`. Replacement occurs on decoded JSON string values, so quotes cannot inject JSON fields. Constants remain owner-defined. Dot-separated output paths read strings in the tool result, such as `structuredContent.url`. Plain MCP text blocks are supported for text results. The provider validates its own argument schema; unsupported contracts fail explicitly.

Every generation first displays the exact resolved parameters, endpoint and model/tool for review. A review hash prevents unnoticed destination/configuration changes before submission. The candidate snapshots the selected connection endpoint/version and binding. Changing/revoking the binding invalidates queued work. A request ID makes resubmitting the same generation idempotent.

## Asynchronous MCP tools

Only configure asynchronous tools when their real contract is known. Set `job_path`, `status_tool`, `status_arguments`, `status_path`, `ready_value`, `failed_value`, and the final `url_path`. The status tool must also be discovered on the same connection. A pending result retains its job ID privately. **Check provider status** enqueues one explicit call to that exact status tool. No polling endpoint or automatic tool chain is invented. The browser automatically refreshes local queue status, not the external provider.

Creation runs once in the native queue. If a connection drops after a provider accepted the request, the operation is uncertain: check the provider before explicitly generating again. The worker never automatically repeats a potentially chargeable creation. Interrupted running records become failed after five minutes, with no repeat call. Regeneration uses a new reviewed request and creates a separate candidate.

## Media and limits

Imports accept PNG, JPEG, WebP and MP4. Render can return an image or video. HTTPS downloads use the DNS-aware public-network client, reject redirects, URL credentials and fragments, and never forward provider authorization. Signed HTTPS download URLs work; arbitrary extra download headers do not. Responses are bounded (MCP 1 MiB, image JSON 32 MiB, downloaded media 64 MiB), and native site upload limits still apply. Tool calls have a 90-second client limit inside a 100-second job limit. Longer tasks must use an asynchronous provider contract.

Imported files use native `media.Service.Upload` with `GeneratedContentID` and `GeneratedKind`. Storage keys are `sites/<site-id>/content/<content-id>/YYYY/MM/DD/<media-id>.<ext>`, using the selected local/S3 Media destination. Provider download URLs are not copied into media source metadata. Imported files remain in Media even if result persistence is interrupted.

Each content/editor pair retains at most 40 active candidates. All routes require a signed-in editor, native CSRF validation and content access; importing media additionally requires `media.upload`. Author/contributor content ownership is enforced again at worker execution. Configuration requires `settings.manage`. Ordinary responses omit encrypted secrets, provider job IDs and binding internals. Anonymous reports are separate from native content and cannot enter this studio.

## Validation

Local fake-provider tests cover unsaved source, draft preservation, same-request idempotency, text edits, owner isolation, CSRF, disabled capabilities, destination pinning, actual generated image import and retention, MCP discovery versus exact tool calls/status, sanitized errors and private-network rejection. Real provider generation remains unverified until an operator supplies a supported provider connection and explicitly invokes it.

## Bulk actions

Tick items on a content list (or **Select all**) and choose a bulk action. **Select all only selects the items shown on the current page, with the current filters**; other pages are never included. The list offers only the actions your role allows:

| Action | Needs | What happens per item |
|---|---|---|
| Publish now | `content.publish` | `Publish` (as the editor's Publish button). Already published items are skipped. |
| Move back to draft | `content.publish` | `Unpublish`. Items that are not published, scheduled or private are skipped. |
| Schedule… | `content.publish` and `content.edit` | Saved with status *scheduled* and the chosen time. The time (UTC, `datetime-local`) must be in the future, otherwise nothing changes. |
| Change author… | `content.edit`, not author/contributor | Saved with the new author. Only active team members whose role may create content are offered and accepted. |
| Add a term… / Remove a term… | `content.edit` | Saved with the term added to or removed from the item's terms. Only existing terms of the type's taxonomies are offered (up to 500). |
| Move to trash / Delete permanently | `content.delete` | Unchanged from before; the whole request needs `content.delete`. |

Each item goes through the same service method as the single-item path (`Publish`, `Unpublish`, `Update`), so ownership (authors and contributors only change their own items), publish permission, the save hooks of that path (`content.before_save`, `content.authorize_save` for saves), validation, revisions and audit entries apply item by item. One refused item never stops the others. Publish and move back to draft ask for confirmation with the number of selected items first.

After the request the list (with the same filters) shows a summary, for example **2 published · 1 skipped**, with the skipped items grouped by reason: no permission, not yours, changed meanwhile or held for approval, does not pass validation, in the trash, already in that state, not found. Titles are shown only for items you may open. The summary lives in a two-minute cookie for that list and is shown once. A request handles at most 200 ids.

Endpoint (admin session, CSRF field `_csrf`): `POST /admin/content/{type}/bulk` with `action` (`publish`, `unpublish`, `schedule`, `author`, `term_add`, `term_remove`, `trash`, `delete`), `ids`, and `publish_at`, `author_id` or `term` (`taxonomy/slug`) as the action needs; `return` is the list query to go back to.

Not included:

- **Change language** in bulk: an item's language ties it to a translation group (one item per language) and to its slug, so moving many items at once can collide with existing translations. Use **Translations** in the editor.
- **REST API**: `/api/v1` has no bulk operation pattern yet; scripts call the per-item endpoints (`POST /api/v1/content/{id}/publish` and the update endpoint) in a loop.

## Export

Take content out of GoRun as JSON (complete, for re-import elsewhere) or CSV (one row per item, for spreadsheets). Specification: [specs/data-freedom.md](specs/data-freedom.md).

- **Admin:** the content list's **Export** menu downloads JSON or CSV with the list's current filters (search, status, language, author, view). `GET /admin/content/{type}/export?format=json|csv` needs `content.view`.
- **REST:** `GET /api/v1/content/export` (scope `content.view`) answers with an attachment (`Content-Disposition: attachment`, `Cache-Control: private, no-store`). Parameters: `format` (`json` default, `csv`), `type`, `status` (comma list; default all but trash), `locale`, `author_id`, `search`, `date_field` (`created_at` default, `updated_at`, `published_at`) with `from`/`to` (inclusive; `2025-01-31` or RFC 3339), `columns` (CSV), `limit` (1–10 000, default 10 000) and `after`. Invalid values answer 422 before anything is sent.
- **CLI:** `gorun content export --format json|csv [--type T] [--status S] [--locale L] [--author ID] [--search Q] [--date-field F] [--from D] [--to D] [--columns c1,c2] [--out file]` writes everything that matches (no limit) to stdout or to a private (0600) file that appears only once complete.

**JSON** is one document: `{"format":"gorun.content-export","version":1,"exported_at","filters","items":[…],"count","next_after"}`. Each item has `id`, `type`, `slug`, `title`, `excerpt`, `body`, `body_format`, `status`, `locale`, `translation_group`, `parent_id`, `menu_order`, `template`, `fields`, `seo`, `terms` (taxonomy → term slugs), `author` (`id`, `name`, `email`), `featured_media_id`, `media` (every media id the featured image and image/file fields reference, with its absolute URL), `created_at`, `updated_at`, `published_at` and `publish_at`. Media files themselves are not included; backups carry them.

**CSV** starts with a UTF-8 BOM so spreadsheet apps read Arabic. Default columns: `id,type,slug,title,status,locale,author_name,created_at,updated_at,published_at,terms`. `columns` picks from `id`, `type`, `slug`, `title`, `excerpt`, `body`, `status`, `locale`, `translation_group`, `parent_id`, `author_id`, `author_name`, `author_email`, `created_at`, `updated_at`, `published_at`, `publish_at`, `featured_media_id`, `featured_media_url`, `seo_title`, `seo_description`, `terms` and `fields.<key>` (lists and objects as JSON). `terms` reads `category:news|world; tag:go`. Every cell that starts with `=`, `+`, `-`, `@`, a tab or a carriage return gets a leading `'`, so a spreadsheet never runs it as a formula.

**Who sees what.** An export holds exactly what the content list shows the caller: authors and contributors get their own items plus other people's published items of public types (never someone else's draft, pending or private item); the admin list's export of an author is limited to their own items, as the list is. Author emails appear only on the caller's own items, for callers with `users.manage`, and in the CLI. The CLI runs as the server operator and sees everything.

**Large exports** stream: items are read in pages of 100 in id order and written as they are read, so memory does not grow with the site. An HTTP export stops after `limit` items; the JSON document then ends with `next_after`, and the same request with `after=<next_after>` continues (for CSV, pass the last row's `id`).

**Audit.** Admin and REST exports record `content.export` with the actor, `via` (`admin` or `api`), format, filters, `count` and `next_after` — never item contents.

Not included: importing a JSON export (use the REST create operations or the WordPress importer), comments, revisions, users, settings and media binaries.

## Term translations

On a site with several content languages, categories, tags and custom taxonomy terms are translated like content: each term has a `locale` (one of the content languages; existing terms got the site's default language when the feature arrived) and terms that are the same category in different languages share a `translation_group`.

- **Admin:** open a term in **Taxonomies → (taxonomy)**. The form has a **Language** select, and the **Translations** panel lists every content language: add a translation inline (name, optional slug), link an existing term of another language, or remove the term from its translations. The term table shows each term's language. All of this needs `taxonomy.manage`.
- **Slugs** stay unique per taxonomy across languages, so `news` and `akhbar` (or `أخبار`) are separate slugs; the same text in two languages becomes `news` and `news-2`. Every slug reference (content `terms`, `taxonomy.{tax}` filters, menus, theme functions) keeps naming exactly one term.
- **A translation** copies the description and meta and takes the parent's translation in its language as parent (top level when the parent is not translated). A group holds one term per language; a group left with one term is dissolved.
- **Public site:** `/{taxonomy}/{slug}` under a language prefix redirects (301, query kept) to the term's translation in that language when there is one, so `/en/category/akhbar` goes to `/en/category/news`. A term archive lists the page language's items tagged with any term of its group (a translated post that kept the source's category still appears). Term pages list each translation as an `hreflang` alternate, the theme functions `url`, `terms` and `termsOf` use the page language's translation, the sitemap and menu items link each term under its own language prefix. Untranslated terms work in every language as before.
- **REST:** terms carry `locale` and `translation_group`; `GET /api/v1/taxonomies/{tax}/terms` filters by `locale` and `translation_group`; `POST …/terms` accepts `locale` and `translation_of` (a term id of the same taxonomy); `GET`/`POST`/`DELETE …/terms/{id}/translations` and `POST …/terms/{id}/translations/link` (`{"term_id": "…"}`) list, create, unlink and link. `translation_group` is read-only on create and update.
- **MCP:** `list_terms` takes optional `locale` and `translation_group`; `create_term` takes optional `locale` and `translation_of` ([MCP.md](MCP.md)).
- A content language used by a term cannot be removed from the site's languages.
