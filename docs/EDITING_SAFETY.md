# Editing safety

> **ملخص بالعربية.** محرر المحتوى يحفظ النص تلقائيًا في سجل خاص بكل مستخدم ولا يغيّر العنصر نفسه،
> فلا تتغير النسخة المنشورة ولا يُملأ سجل المراجعات. إن حفظ شخص آخر نسخة أحدث يرفض الحفظ التلقائي
> بالرمز 409 دون أن يمحو شيئًا، ويبقى النص في المحرر. عند فتح العنصر لاحقًا يظهر شريط "استعادة
> التغييرات المحفوظة تلقائيًا". الحفظ العادي يزيل السجل.

## Autosave

The content editor saves the text you type in the background, for existing items you may edit
(`content.edit`, and for authors and contributors only their own items). New items start
autosaving after their first save.

**What is saved.** Title, slug, excerpt, body, SEO fields and custom fields (`fields[...]`).
Status, publish date, author, terms and translations are decisions, not drafts: they are never
autosaved and only change with **Save**.

**Where it goes.** Every autosave is written to `content_autosaves`, one row per item and user
(`content_id`, `user_id`, `payload_json`, `base_edit_token`, `base_updated_at`, `saved_at`),
never into the item, whatever its status:

- a published or scheduled item keeps its live content until you save;
- a draft keeps its revision history: each real save adds a revision and history is pruned to
  `content.revisions_keep` (25 by default), so a save every 30 seconds would push real
  revisions out;
- no `content.before_save`/`after_save` hooks, search indexing, webhooks or audit entries run
  for a background save.

Only you see your autosaves. A normal **Save** removes your row; **Discard** in the restore
banner removes it too. Deleting the item removes all of its autosaves, and deleting a user
removes theirs (`user_id` references `users` with `ON DELETE CASCADE`). The daily
`content.cleanup` job deletes autosaves not touched for 30 days, and autosaves older than
7 days whose item was saved since (the restore banner offers those as older than the item
until then).

**Conflicts.** The editor is opened on a version identified by its edit token (the same
optimistic-concurrency token the normal save checks, `content.EditToken`). An autosave is only
stored while that token still matches the item, checked inside the write transaction. When
someone else saved the item after you opened it, the server answers `409 conflict`, stores
nothing and the editor shows: *Not autosaved: someone saved a newer version of this item. Your
text is still in the editor; copy what you need, then reload to see their changes.* Autosave then
pauses for that page; nothing is overwritten on either side. A normal save from the same page
gets the existing conflict screen with your input preserved.

**Restore.** When you open an item with an autosave, a banner reads *You have autosaved changes
from 2026-11-11 12:04* with **Restore autosaved changes** and **Discard**. Restore fills the
editor (rich text included); nothing is saved until you press **Save**. If the item was saved
again after your autosave, the banner says so: restoring then puts the older text over the newer
version in the editor and you should compare before saving. Restore needs JavaScript; Discard
also works as a plain form post.

**Timing.** A change is saved 3 s after you stop typing, at most 30 s after the first unsaved
change while you keep typing, and when focus leaves a field; never more than one request every
3 s. A periodic check every 30 s catches changes that fired no event.

**Status line.** Under the save state in the publish panel: *Autosave is on*, *Changes not
autosaved yet*, *Saving…*, *Autosaved 2026-11-11 12:04* (the shared `gpDateTime` output, full
value in the tooltip), *Not saved* with a **Retry** button on a network or server error, and the
conflict or permission message.

**Leaving the page.** The browser asks before leaving when there are changes that are neither
saved nor autosaved (including status, terms and other fields autosave does not cover).

**Endpoints** (admin session, CSRF field `_csrf`):

| Method | Path | Permission | Answer |
|---|---|---|---|
| POST | `/admin/content/{type}/{id}/autosave` | `content.edit` + item owner rules | `200 {"ok":true,"data":{"saved_at":…}}`, `409 conflict`, `403 forbidden`, `404 not_found`, `413 too_large` (4 MiB) |
| POST | `/admin/content/{type}/{id}/autosave/discard` | `content.edit` | JSON with `Accept: application/json`, else `303` back to the editor |

The form posts `edit_token` (from the editor) and the autosaved fields as the editor names them.

## Revision compare

Each entry in the editor's **Revisions** panel has a **Compare** link before its **Restore**
button. It opens `/admin/content/{type}/{id}/revisions/{rev}/compare`
(permission `content.view`; authors and contributors only for their own items):

- **This revision → current version** (default) compares the revision with the saved item;
- **Previous revision → this revision** (`?against=previous`) shows what that save changed.
  The oldest revision has no previous one (404).

The page lists the changed fields (title, slug, status, excerpt, template, parent, order,
featured image, every SEO key, every custom field and every taxonomy) and a body diff, then a
**Restore this revision** button (`content.edit`) for any revision but the newest.

The body diff runs on text extracted from the stored, already sanitized HTML: one line per
block element (paragraphs, headings, list items, quotes, table rows…), `<br>` breaks a line,
images appear as `[image: alt]`, scripts and styles are dropped and whitespace is collapsed
except in `<pre>`. Lines are compared with a longest-common-subsequence diff
(`internal/content/diff.go`, no dependency); a removed line followed by an added one becomes a
*Changed* line with a word-level diff. Very large bodies (over about a million line pairs
after trimming the common start and end) fall back to "all removed, all added".

Removed text is in `<del>` (struck through), added text in `<ins>` (underlined), each starting
with a screen-reader label (*Removed:* / *Added:*); every changed line has a visible text label
(*Added*, *Removed*, *Changed*), so color is never the only signal. Text is rendered escaped,
with `dir="auto"` and `unicode-bidi: plaintext` per line so Arabic and English lines each keep
their direction.

## Shareable preview links

A preview link lets someone without an admin account see a draft, pending, scheduled or
private item through the public theme. In the editor's **Preview links** panel, choose how
long it lives (**1 hour**, **24 hours** — the default — or **7 days**) and press **Create preview
link**. The URL is shown once, with a **Copy** button; active links are listed with their expiry
and a **Revoke** button (with confirmation). Without JavaScript, create shows the link on its
own page and revoke returns to the editor.

- **Who.** Creating, listing and revoking need `content.edit` on the item (authors and
  contributors only for their own items). Trashed items get no links and are never shown.
  At most 20 active links per item.
- **Token.** 32 random bytes from `crypto/rand`, base64url (43 characters), in
  `/preview/{token}`. Only its SHA-256 is stored in `content_preview_links`
  (`id`, `content_id`, `token_hash`, `created_by`, `created_at`, `expires_at`, `revoked_at`);
  the daily `content.cleanup` job deletes rows that expired or were revoked more than 30 days ago;
  the lookup goes by hash and is confirmed with a constant-time comparison. The token never
  appears in the database, the audit log or the list.
- **What the visitor sees.** The saved version (not unsaved editor text, not autosaves)
  rendered by the active theme as if it were published, in the item's language, with a small
  *Preview — not published* ribbon at the top. Page access rules (password or blocked pages)
  do not apply: the link itself is the permission.
- **Headers.** `X-Robots-Tag: noindex, nofollow`, `Cache-Control: private, no-store` and
  `Referrer-Policy: no-referrer` on every answer, including the 404.
- **No oracle.** Unknown, malformed, expired and revoked tokens, links to trashed items and
  items of non-public types all get the same theme 404 page. Paths under `/preview/` that are
  not 43-character tokens fall through to normal page routing.
- **Rate limit.** 60 requests per minute per client IP on `/preview/…` (then `429` with
  `Retry-After`).
- **Audit.** `content.preview_link_create` (link id, expiry, lifetime) and
  `content.preview_link_revoke` (link id), targeting the item.

Admin routes: `POST /admin/content/{type}/{id}/preview-links` (`preview_ttl` = `1h`/`24h`/`7d`;
JSON `201` with `Accept: application/json`, else the result page) and
`POST /admin/content/{type}/{id}/preview-links/{link}/revoke`.

REST ([API.md](API.md#content)): `GET /api/v1/content/{id}/preview-links` lists active links
(never tokens), `POST /api/v1/content/{id}/preview-links` with `{"ttl":"24h"}` returns `201`
with the link and its `url` (the only time the token is returned), and
`DELETE /api/v1/content/{id}/preview-links/{link}` revokes. All need `content.edit`.

```sh
curl -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"ttl":"7d"}' https://example.org/api/v1/content/01J…/preview-links
# {"ok":true,"data":{"id":"01J…","content_id":"01J…","created_by":"…","created_at":"…",
#  "expires_at":"…","url":"https://example.org/preview/…"}}
```
