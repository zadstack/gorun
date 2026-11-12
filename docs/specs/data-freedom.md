# Spec: Data freedom (roadmap phase 8)

Status: in review. Roadmap: [PRO-ROADMAP.md](../PRO-ROADMAP.md) phase 8. Plan context:
[PLAN.md](../../PLAN.md) step 3 ("Add term translation relationships").

## Objective

Site owners can take their content out of GoRun in a documented, complete format, and
multilingual sites can translate category, tag and custom taxonomy terms the way they already
translate content. Two independent capabilities:

| Module | Responsibility | Depends on |
|---|---|---|
| `content-export` | Streamed JSON/CSV export of content items through admin, REST and CLI | content query + read permissions |
| `term-translations` | Term locale + translation group, admin linking, per-locale routing, REST/MCP | taxonomy, site router, seo sitemap |

Build order: `term-translations` first (the export writes term slugs and is unaffected by it),
then `content-export`. Either can ship alone.

## Assumptions

1. Exports are for people who may already read the items: the export never widens what the
   content list shows. Authors and contributors export their own items plus others' published
   items of public types, exactly like `restrictStaffRead`.
2. The CLI runs as the server operator (no user in context) and sees everything, like
   `gorun content list`.
3. Term slugs stay unique per taxonomy across all locales (unchanged constraint). A translation
   therefore has its own slug (`news` / `أخبار`, or `news-2` when the same text is reused). This
   keeps every existing slug reference (content `terms`, `taxonomy.{tax}` filters, menus, theme
   functions, URLs) unambiguous.
4. Existing terms get the site default locale (`i18n.ContentLocale`); monolingual sites see no
   behaviour change.

## Scope

### content-export

- Formats
  - **JSON**: one document `{"format":"gorun.content-export","version":1,"exported_at",
    "filters","items":[…],"count","next_after"}`. Each item: id, type, slug, title, excerpt,
    body, body_format, fields, status, locale, translation_group, parent_id, menu_order,
    template, seo, terms (taxonomy → slugs), author `{id,name,email}`, dates (created, updated,
    published, publish_at), featured media and every media ID referenced by image/file
    fields as `{id,url}`.
  - **CSV**: one row per item, UTF-8 with BOM. Default columns
    `id,type,slug,title,status,locale,author_name,created_at,updated_at,published_at,terms`;
    `columns=` selects core columns (`id,type,slug,title,excerpt,body,status,locale,
    translation_group,parent_id,author_id,author_name,author_email,created_at,updated_at,
    published_at,publish_at,featured_media_id,featured_media_url,seo_title,seo_description,
    terms`) and custom fields (`fields.<key>`; non-scalar values as JSON). Every cell starting
    with `=` `+` `-` `@`, tab or CR is prefixed with `'`.
- Filters: `type`, `status` (comma list, default all but trash), `locale`, `author`,
  `from`/`to` (date or RFC 3339, inclusive) on `date_field` = `created_at` (default),
  `updated_at` or `published_at`; `search` as in the list.
- Streaming: keyset pages of 100 ordered by id, written and flushed as they are read; memory
  does not grow with the export size. HTTP exports stop at `limit` (default and maximum
  10 000 items). The JSON document then ends with `next_after` (the last id); repeating the
  request with `after=<id>` continues. CSV callers use the last row's `id` the same way. The
  CLI pages through everything.
- Surfaces
  - Admin: **Export** menu on the content list (JSON, CSV) carrying the current filters;
    `GET /admin/content/{type}/export?format=…` (permission `content.view`).
  - REST: `GET /api/v1/content/export?format=json|csv&…`, scope `content.view`,
    `Content-Disposition: attachment`, `Cache-Control: no-store`.
  - CLI: `gorun content export --format json|csv [--type T] [--status S] [--locale L]
    [--from D] [--to D] [--columns c1,c2] [--out file]` (stdout when `--out` is absent).
- Audit: admin and REST exports record `content.export` with the actor, format, filters and
  row count, never contents.

### term-translations

- Schema: `terms.locale` and `terms.translation_group` (`''` = not translated), unique
  `(translation_group, locale)` where grouped. Migration adds the columns; boot backfills empty
  locales with the site default.
- Service: `TermTranslations`, `CreateTermTranslation` (copies description, maps the parent to
  its translation in the target locale), `LinkTermTranslation`, `UnlinkTermTranslation`;
  `TermQuery.Locale` and `TermQuery.TranslationGroup` filters; `SaveTerm` validates `locale`
  against the content languages and never changes the group.
- Admin: the term edit panel shows a language select and a **Translations** section listing
  each content language with the linked term, an inline form to add a translation (name,
  optional slug), a picker to link an existing term and an unlink action. A language column
  appears on the term table on multilingual sites.
- Public site: `/{tax}/{slug}` under a locale prefix redirects (301) to the term's translation
  in that locale when one exists; a term archive lists items assigned to any term of its
  group; `hreflang` alternates list each translation's URL; `url`, `terms` and `termsOf` in
  themes use the page-locale translation when there is one; the sitemap puts each term under
  its own locale prefix.
- REST (additive): `locale` and `translation_group` on `Term`; `locale`, `translation_group`
  filters on the term list; `translation_of` on create; `GET/POST/DELETE
  /v1/taxonomies/{tax}/terms/{id}/translations` and `POST …/translations/link`.
- MCP (tool names unchanged): `list_terms` gains optional `locale`, `translation_group`;
  `create_term` gains optional `locale`, `translation_of`. Both return the new fields.

## Non-goals

- JSON import (the WordPress importer and REST create cover imports; a GoRun→GoRun import is a
  separate change).
- Exporting media binaries, comments, revisions, users or settings (backups cover those).
- Per-locale duplicate term slugs, translated taxonomy names (already bilingual), menus per
  locale.
- An MCP export tool.

## Security

- Trust boundary: HTTP query parameters → validated (enums, dates, limit, columns allowlist).
- Information disclosure: exports reuse `restrictStaffRead`; `private`/draft items of others
  never leave for authors/contributors. Author email (PII) is included only for the caller's
  own items, for callers holding `users.manage`, and for the CLI; otherwise empty.
- CSV formula injection: neutralised cells (shared `httpx.CSVCell`).
- Repudiation: exports are audited (no contents). DoS: bounded pages and `limit`.
- Term translation writes need `taxonomy.manage`, are audited and run in one transaction.

## Commands

```sh
gofmt -l . && go vet ./... && go test ./...
python3 tools/docs-check.py
node tools/admin-icons/update.mjs --check
go run ./cmd/gorun api-docs docs/API.md   # regenerate the operations table
(cd e2e && npx playwright test data-freedom)
```

## Testing strategy

Go tests on real SQLite (`kernel.Boot`, `apitest`):

- export: author cannot export another user's private draft; CSV injection escaping; 250+
  rows stream in keyset pages; filters (type, status, locale, dates, author); `limit` and
  `next_after`; REST 401/403/200 with headers; CLI output to file.
- terms: migration on an existing database; translation create/link/unlink rules; per-locale
  routing redirect and archive; sitemap locale prefix; MCP tools with locale.
- Playwright: export from the content list (download), translating a category into Arabic.

## Success criteria

- Every surface above exists, documented in docs/API.md, docs/MCP.md and
  docs/CONTENT_STUDIO.md, with English and Arabic strings.
- No existing API field, route or MCP tool removed or renamed.
- `go vet`, `go test ./...`, docs check, icon check and the new Playwright specs pass.

## Open questions

- Should the HTTP export limit be configurable? (Not now: 10 000 with a cursor.)
