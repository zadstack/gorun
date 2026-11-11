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
banner removes it too. Deleting the item removes all of its autosaves.

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
