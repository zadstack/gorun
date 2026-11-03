# Site forms: any fields, a table per form

Status: design approved in conversation (sections 1–3), awaiting written-spec review.

## Goal

Every form on a GoRun site stores its submissions in the admin from its first submission, in a table of its own
whose columns are that form's fields. A form comes into being in one of two ways:

1. **Builder:** a "Form" section in the page editor declares the fields. Publishing the page creates the form.
2. **Capture:** any GoRun-rendered `<form method="post" action="/forms/{slug}">` that carries the CSRF token is
   accepted. Its first submission creates the form, and unknown fields are recorded as *discovered*.

Each submission is stored first and then emits the existing `site.form.submitted` webhook event.

## What the user decided

- Forms may have any fields (not only the contact form's name, email and message).
- Both creation paths exist: the builder is the primary one, and capture means no submission is lost.
- After storing, the only automatic action is the webhook (integrations). The table and CSV export are the core
  of the feature. No new notification emails and no canvas events in this version.
- Storage is approach A: one `forms` table with a JSON field schema, and one `form_entries` table with JSON data.
  There are no per-form SQL tables.

## Today (0.18.7)

- There is one form, `POST /forms/contact`, with fixed fields (`internal/site/forms.go`). It uses `contactGuard`
  (5/min per-IP rate limit, a 64 KiB body cap, multipart parsing that passes `_csrf` on), the CSRF middleware, the
  `website` honeypot, validation, an optional attachment, a receipt email, and the `site.form.submitted` event
  (exactly once, via `form_event_deliveries`).
- Rows live in `form_submissions` (fixed columns plus inbox state: status, attachment, delivery). The inbox is at
  `/admin/system/form-submissions` (`internal/site/submissions_admin.go`), with CSV export, under
  `PermSubmissions`.
- Builder section kinds are listed in `internal/builder/model.go` (`kinds`), and `contact` renders the real
  contact form.
- There is no visitor analytics. That is a separate project (see Out of scope).

## Design

### 1. Data

New migration in `internal/site/migrations/`:

```
forms(
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,          -- [a-z0-9-]{1,40}
  title TEXT NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('builder','captured','contact')),
  fields_json TEXT NOT NULL,          -- ordered [{name,label,type,required,help,choices,state,hidden}]
  open INTEGER NOT NULL DEFAULT 1,    -- 0 after its builder section is removed
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
)
form_entries(
  id TEXT PRIMARY KEY,
  form_id TEXT NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
  data_json TEXT NOT NULL,            -- {field name: string value}
  page TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new','read','spam')),
  attachment_name TEXT NOT NULL DEFAULT '', attachment_type TEXT NOT NULL DEFAULT '', attachment_size INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
)
CREATE INDEX form_entries_form_created ON form_entries(form_id, created_at);
CREATE INDEX form_entries_form_status ON form_entries(form_id, status, created_at);
```

- **Field types:** `text`, `textarea`, `email`, `number`, `date`, `choice`, `checkbox`, `file` (builder only).
- **Field state:** `pinned` (declared, so it is validated) or `discovered` (seen in a captured submission).
- **The contact form becomes a `forms` row** with slug `contact`, source `contact`, and its three pinned fields.
  - `form_submissions` stays as is. It keeps carrying delivery state for the receipt email and the attachment
    path, and the existing inbox and its CSV keep working unchanged.
  - The contact form's table in the new admin reads `form_submissions` through an adapter. It needs no data
    migration, so nothing can be lost in a copy.
  - The new storage serves every other form.
  - *Ruling to confirm in plan review:* keeping `form_submissions` instead of migrating it avoids a risky
    one-shot copy of rows that have attachments on disk and delivery state.

### 2. Receive path: `POST /forms/{slug}`

1. **`formGuard`** generalizes `contactGuard` (rate limit, body cap, multipart, CSRF hand-off). `/forms/contact`
   keeps its existing handler.
2. **Honeypot:** a filled `website` field gets a silent success, as today.
3. **Known form, builder source:** the server validates the pinned fields (type, required, choices, length) and
   drops unknown fields. Errors return to the page with values kept, as `formResult` does for contact. A closed
   form (`open=0`) returns 410 with a short message.
4. **Unknown slug or captured form:** capture rules apply, and a breach rejects the whole submission with 422:
   - field names match `[a-z0-9_]{1,40}`;
   - at most 30 fields, and at most 4,000 characters per value;
   - names starting with `_`, plus `website` and `_csrf`, are ignored;
   - at most 50 captured forms per site;
   - no files.

   The first submission creates the form with source `captured`. New field names are appended as `discovered`.
5. **Storage order:** the entry is stored first. Then `site.form.submitted` is emitted with the existing
   payload plus `form` (the slug) and `fields` (a map), exactly once per entry through a delivery table like
   `form_event_deliveries`. An audit entry records the submission without its values.
6. **Rate limiting:** applies per IP across all `/forms/*`, sharing the existing limiter.

### 3. Admin

- **`/admin/forms`** sits in the sidebar's "inbox" group. It lists each form with its title, source, entry
  count, new count and last entry time. The sidebar badge shows the total count of new entries.
- **`/admin/forms/{slug}`** shows the form's table:
  - **columns:** date, status, then up to six visible fields in schema order; the rest show on the detail page;
  - **order and paging:** newest first, 50 per page;
  - **filters:** status, date range, and a text search over `data_json`;
  - **CSV export:** all fields, respecting the active filter, with the existing CSV-injection guard;
  - **detail page:** opening an entry marks it read; entries can be marked spam or deleted.
- **Schema actions:**
  - pin a discovered field (choose its type and label);
  - hide or unhide a column, rename a label, reorder;
  - these change presentation only and never rewrite stored entries.
- **Deleting a form:**
  - a captured form can be deleted, with a confirmation showing its entry count;
  - a builder form cannot be deleted while its section is published.
- **Permissions:** `PermSubmissions` throughout. Deletions and exports are audited.

### 4. Builder section and themes

- **New section kind `form`** in `internal/builder/model.go`:
  - settings: title, slug (derived from the title, editable), submit label, success message;
  - field list: up to 30 fields, each with label, type, required flag, help text and choices;
  - `file` uses the existing attachment settings.
- **Preview** renders the form disabled, as the contact section does in drafts.
- **Publishing a page** upserts each form section's `forms` row with source `builder` and all fields pinned.
  - A slug taken by another builder form rejects the publish with a clear message.
  - A slug held by a captured form is adopted: its fields become pinned and its entries are kept.
- **Removing the section** and republishing sets `open=0`. The table and entries stay.
- **Module boundary:** the builder must not write `site` tables directly. Publishing calls an interface that
  the site module provides through `kernel.Deps`. The plan reads `docs/BOUNDARIES.md` and names the exact hook.
- **Themes:**
  - `{{form "slug"}}` renders a builder form anywhere, with the same validation and error rendering;
  - plain HTML forms posting to `/forms/{slug}` with `{{csrf}}` are captured;
  - `docs/THEME-DEVELOPMENT.md` gets an example of each.
- **Accessibility:**
  - the same CSS and markup contract as the contact form;
  - a `label` on every field, errors tied to fields with `aria-describedby`, and focus moved to the first
    invalid field;
  - 44px controls on coarse pointers, and RTL/LTR from the page locale.

## Out of scope

- Visitor analytics (Umami-like). That is the next project, with its own spec.
- New notification emails. The contact form's receipt email is unchanged.
- Canvas inbound events from forms.
- Automatic retention or deletion, and per-field permissions.
- Editing submitted values.
- Charts.

## Files (expected)

| area | files |
|---|---|
| storage, receive path | `internal/site/migrations/0006_forms.sql`, `internal/site/forms.go` (guard generalization), new `internal/site/forms_any.go` |
| admin | new `internal/site/forms_admin.go`, templates under `web/admin/templates/pages/site/` |
| builder | `internal/builder/model.go` (`form` kind), `native_render.go`, the page editor's section form, publish hook |
| themes | template func `form` in the site renderer, `docs/THEME-DEVELOPMENT.md` |
| labels | site and builder catalogs, `ar` and `en` |
| docs | `CHANGELOG.md`, a new `docs/FORMS.md` |

## Testing

- **Go unit tests:**
  - capture limits (names, count, length, the 50-form cap) and field discovery;
  - builder validation per type, and a closed form returning 410;
  - honeypot, rate limit and CSRF on `/forms/{slug}`;
  - webhook emitted exactly once with `form` and `fields`;
  - slug conflicts on publish and adoption of a captured form;
  - CSV escaping.
- **Contact form regression:** the existing contact tests stay green, including the inbox, CSV, receipt and
  event tests.
- **E2E (`e2e/tests/site-forms.spec.ts`):**
  - build a page with a Form section, publish it, submit as a visitor in `ar` and `en`, and see the entry in
    `/admin/forms/{slug}` at once;
  - a captured theme form appears with discovered fields, and pinning a field works;
  - CSV export contains the fields;
  - filters and search work;
  - validation errors focus the first invalid field.

## Review focus

1. Two quick submits from the same page (double click): the replay key from the contact form must cover any form.
2. A builder form edited to drop a field: old entries keep the value, the column is hidden, CSV still includes it.
3. A captured form flooded with random field names: the 30-field and 50-form caps hold, and the rejection does
   not create the form.
4. Arabic field labels with Latin slugs: the derived slug is ASCII, and an empty derivation asks for one.
5. A form section on an unpublished draft: no `forms` row until publish, and preview never accepts a submission.
