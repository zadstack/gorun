# Site Forms Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Any form on a GoRun site, from a builder section or plain theme HTML, stores its submissions in its own admin table from the first submission, and each submission fires `site.form.submitted`.

**Architecture:**
- The `site` module owns two new tables (`forms`, `form_entries`), the `POST /forms/{slug}` receive path, the admin tables, and a theme func `form` that renders a form's markup.
- The `builder` module adds a `form` section kind. Builder boots before site, so it never imports site: on publish it emits the hook `builder.forms.published`, and site subscribes and upserts the definitions.
- The theme's builder body injector renders each `gp-form` section through the `form` func, which supplies the CSRF token at request time.

**Tech Stack:** Go, SQLite, html/template, plain JS (builder.js), Playwright.

**Spec:** `docs/superpowers/specs/2026-11-03-site-forms-design.md`

## Global Constraints

- Commits: `git -c user.name=ahmedvnabil -c user.email=vacrom414@gmail.com commit -s`, with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Module boundaries (`docs/BOUNDARIES.md`): `internal/builder` must not import `internal/site`, and `internal/theme` must not import either. Communicate through hooks (string names) and `d.Theme.AddFunc`.
- Permission: `PermSubmissions` (`settings.manage`) for every admin route. Add no new permission.
- Slug: `^[a-z0-9-]{1,40}$`. Field name: `^[a-z0-9_]{1,40}$`.
- Field types: `text`, `textarea`, `email`, `number`, `date`, `choice`, `checkbox`, `file` (`file` is builder only).
- Capture limits: at most 30 fields, at most 4,000 characters per value, at most 50 captured forms per site, no files. Names starting with `_`, plus `website`, are ignored.
- Builder forms: at most 30 fields. A known builder form drops unknown fields.
- Reuse `contactGuard`'s limits: a 5/min per-IP limiter shared by all `/forms/*`, a 64 KiB body, and CSRF.
- Store first, then emit `site.form.submitted` once. `/forms/contact` behavior is unchanged (all existing contact tests stay green).
- `ar` and `en` labels for every new string, in the site and builder catalogs.
- Tests: `go test ./internal/site/... ./internal/builder/... ./internal/theme/...`. E2E from `e2e/`: `npx playwright test tests/global.setup.ts tests/<spec>.ts --project=setup --project=chromium`.

## Rulings (made while planning, against the spec)

- R1. **The contact form is left alone.** It keeps `form_submissions` and its inbox. `/admin/forms` lists a "Contact" row whose link opens the existing inbox, `/admin/system/form-submissions`. That inbox is the contact form's table, so no adapter code is needed. Cost if wrong: an extra adapter later.
- R2. **`forms.page_id`** (`TEXT NOT NULL DEFAULT ''`) is added to the spec's schema. Republishing a page without its form section then closes (`open=0`) the builder forms that belonged to that page. Cost if wrong: one column.
- R3. **The theme func is `form(slug, csrf, back string) template.HTML`, implemented in site.**
  - Theme authors write `{{form "booking" csrf .Request.URL.Path}}`.
  - The builder injector calls the same func. That gives one markup implementation for all themes, with no per-theme component.
  - Cost if wrong: a component per theme later.
- R4. **Server-side validation errors render through the existing `form-result` path** (`formResult` with `FormState.Errors` keyed by field name). The form's markup uses native `required` and `type` attributes, so browsers block and focus the first invalid field before submit. Cost if wrong: an inline error re-render later.
- R5. **Builder slug uniqueness is checked inside the builder,** across its own published documents. A captured form with the same slug is adopted by site on upsert. Cost if wrong: a conflict reported later than publish.

## Review Focus

1. Double submit (double click) of any form: one entry, not two. Reuse the contact form's request-key replay window (Task 1 test `TestFormsReplayCreatesOneEntry`).
2. A builder form republished without a field: old entries keep the value; CSV keeps the column (Task 2 test `TestFormsCSVKeepsRetiredFields`).
3. A flood of random field names on a captured form: a rejected submission creates no form and no entry (Task 1 test `TestCaptureRejectionCreatesNothing`).
4. A form titled in Arabic: the derived slug is ASCII. An empty derivation keeps the field empty and asks for one (Task 3 test `TestFormSlugFromArabicTitle`).
5. A draft page with a form section: no `forms` row until publish, and the preview form is disabled (Task 3 test `TestDraftFormNotRegistered`, Task 4 test for preview).

---

### Task 1: Storage and the receive path

**Files:**
- Create: `internal/site/migrations/0006_forms.sql`, `internal/site/forms_any.go`, `internal/site/forms_any_test.go`
- Modify: `internal/site/module.go` (route), `internal/site/forms.go` (`FormSubmission.Fields`), `internal/site/forms_mail.go` (`deliverForm` skips non-contact)

**Interfaces:**
- Produces:
  - `type FormField struct { Name, Label, Type string; Required bool; Help string; Choices []string; State string /* pinned|discovered */; Hidden bool }`, with JSON tags snake_case.
  - `type FormDefinition struct { ID, Slug, Title, Source, PageID string; Fields []FormField; Open bool; CreatedAt, UpdatedAt string }`.
  - `func (h *handlers) formBySlug(ctx context.Context, slug string) (FormDefinition, error)` returns `errNoForm` when there is none.
  - `func (h *handlers) upsertBuilderForms(ctx context.Context, pageID string, defs []FormDefinition) error` is used by Task 3.
  - `FormSubmission.Fields map[string]string` (`json:"fields,omitempty"`) on the hook payload.
  - Entry storage: `form_entries` (spec §1, plus R2's `page_id` on `forms`).

- [ ] **Step 1: Write failing tests** in `forms_any_test.go`, using the package's existing test-app helpers (see `forms_test.go`):
  - `TestCaptureCreatesFormOnFirstSubmit`: POST `/forms/quote` with a valid CSRF token, `name=A&budget=500`. Expect 200 or 303. A `forms` row exists with slug `quote`, source `captured` and two `discovered` fields, and one `form_entries` row has `data_json` `{"budget":"500","name":"A"}`.
  - `TestCaptureAddsNewFieldsAsDiscovered`: a second submit adds `phone`, so the fields are `name`, `budget`, `phone`, in first-seen order.
  - `TestCaptureRejectionCreatesNothing`: 31 fields, or a 4,001-character value, or a name `Bad-Name` returns 422, with zero `forms` and zero entries for that slug.
  - `TestCaptureFormCap`: with 50 captured forms present, a new slug returns 422.
  - `TestCaptureIgnoresReserved`: `_csrf`, `_back` and `website` are not stored, and a filled `website` gives a silent OK with no entry.
  - `TestBuilderFormValidates`: a builder form (inserted through `upsertBuilderForms`) with `email` required. A missing email returns 422 with `Errors["email"]`, and an unknown extra field is dropped from `data_json`.
  - `TestClosedFormGone`: `open=0` returns 410.
  - `TestFormsReplayCreatesOneEntry`: the same token and payload posted twice within 10 minutes gives one entry.
  - `TestFormEventCarriesFields`: the `site.form.submitted` payload has `Form="quote"` and `Fields["budget"]="500"`, and `deliverForm` sends no mail for it.
  - `TestContactUnchanged`: the existing `/forms/contact` tests still pass. Run the whole package.
- [ ] **Step 2:** `go test ./internal/site/ -run 'Capture|BuilderForm|ClosedForm|FormsReplay|FormEvent'`. Expected: FAIL (route missing).
- [ ] **Step 3:** Write migration `0006_forms.sql` (spec §1 plus `page_id`). Add `formGuard` (generalize `contactGuard`'s limiter, body cap and multipart; no attachments for captured forms). Register `r.With(h.formGuard, csrf).Post("/forms/{slug}", h.formSubmit)` after the contact and comment routes so they keep priority. Implement `formSubmit`:
  - honeypot;
  - slug validation;
  - `formBySlug`, then builder validation or capture;
  - a request key of `sha256(json([token, slug, sorted data, page]))`, stored through `form_request_keys` (reuse the 10-minute window; it may need `form_entries` in the cleanup query);
  - insert;
  - `Emit(HookFormSubmitted, FormSubmission{ID, Form: slug, Fields, Page, IP, SubmittedAt})`;
  - audit `site.form.submit` with `form=slug`, and no values;
  - `formResult`.
  
  In `deliverForm`, return early when `sub.Form != "contact"`.
- [ ] **Step 4:** `go test ./internal/site/...`. Expected: PASS, including every contact test.
- [ ] **Step 5:** Commit: `feat(site): store any form's submissions in forms/form_entries`.

### Task 2: Admin tables

**Files:**
- Create: `internal/site/forms_admin.go`, `internal/site/forms_admin_test.go`, `web/admin/templates/pages/site/forms.html`, `web/admin/templates/pages/site/form_entries.html`, `web/admin/templates/pages/site/form_entry.html`
- Modify: the site catalogs (`internal/site/catalog/*.json`, keys under `site.forms.*`)

**Interfaces:**
- Consumes: Task 1 tables and types.
- Produces: routes under `/admin/forms` (nav ID `site.forms`, group `inbox`, position 190, icon `inbox`).

- [ ] **Step 1: Write failing tests** (`forms_admin_test.go`, with the existing admin test client patterns from `submissions_admin` tests):
  - `TestFormsListShowsCountsAndContactRow`: lists `quote` with its entry and new counts, plus a Contact row linking to `/admin/system/form-submissions` (R1).
  - `TestFormEntriesTable`: `/admin/forms/quote` shows the date, status and field columns, at most six visible. Hidden fields are not columns. Newest first, 50 per page.
  - `TestFormEntriesFilters`: `status=new`, `from`/`to` dates and `q` (text search over `data_json` with `LIKE`, escaped) narrow the rows.
  - `TestFormEntryMarksRead`: GET of one entry moves `new` to `read`. POST status `spam` and POST delete work, and delete is audited.
  - `TestFormsCSVKeepsRetiredFields`: CSV has a header of id, created_at, status, page, then all fields, including ones no longer in the schema but present in entries (union in schema order, then extras). Values go through `csvSafe`. The export is audited.
  - `TestPinDiscoveredField`: POST pin with `type=email&label=Email` makes the field `pinned`, and later submits validate it.
  - `TestFieldPresentationOnly`: hide, rename and reorder change `fields_json` only, and `data_json` of old entries is unchanged.
  - `TestDeleteCapturedForm`: the confirmation shows the entry count, and the delete cascades entries. A builder form with `open=1` cannot be deleted (409 plus a flash).
  - `TestFormsAdminForbidden`: a user without `settings.manage` gets 403.
  - `TestNavBadgeCountsNew`: the nav item's badge equals the number of new entries across forms (use the existing nav badge mechanism if there is one; if not, show the count on the list page only and note it in the report).
- [ ] **Step 2:** Run the tests. Expected: FAIL.
- [ ] **Step 3:** Implement `registerFormsAdmin(d, h)` (called from `Module.Register`), handlers and templates, following `submissions_admin.go` and `pages/site/form_submissions.html` markup and flash patterns. Columns render with `bdi` for values, and tables are responsive (they scroll horizontally inside their own container on mobile).
- [ ] **Step 4:** `go test ./internal/site/...`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat(site): admin tables, filters and CSV for any form`.

### Task 3: Builder `form` section and the publish hook

**Files:**
- Modify: `internal/builder/model.go` (kind `form`, `Section.Form *FormSpec`, validation), `internal/builder/render.go` and the mixed/native renderer if it lists kinds, `internal/builder/store.go` (`Publish` emits after commit), the builder catalog `Catalog(locale)` entry
- Create: `internal/builder/forms.go`, `internal/builder/forms_test.go`
- Modify: `internal/site/module.go` (subscribe), `internal/site/forms_any.go` (`decodeBuilderForms`)

**Interfaces:**
- Produces:
  - `type FormSpec struct { Slug, SubmitLabel, Success string; Fields []FormFieldSpec }`.
  - `type FormFieldSpec struct { Name, Label, Type string; Required bool; Help string; Choices []string }`.
  - Hook name constant `HookFormsPublished = "builder.forms.published"`, payload `{"page_id": string, "forms": [{"slug","title","fields":[...]}]}` (JSON-friendly; site decodes it from a struct or `json.RawMessage`, as `decodeSubmission` does).
  - `func SlugFromTitle(title string) string` returns ASCII or `""`.
  - Rendered markup: `<section id="gp-{id}" class="gp-section gp-form gp-default" data-form="{slug}"><div class="gp-inner"><div class="gp-heading">…title/text…</div></div></section>` (no form element; Task 4 injects it).
- Consumes: Task 1 `upsertBuilderForms`.

- [ ] **Step 1: Write failing tests:**
  - `TestFormSectionValidates`: slug pattern, 1 to 30 fields, unique field names, types from the list, `choice` needs 1 to 20 choices.
  - `TestFormSlugFromArabicTitle`: `SlugFromTitle("احجز موعد")==""`, and `SlugFromTitle("Book a call")=="book-a-call"`.
  - `TestPublishEmitsFormsHook`: publishing a page with one form section emits `builder.forms.published` with that page ID and spec.
  - `TestPublishRejectsSlugTakenByAnotherPage`: another published builder page already has slug `booking`, so publish returns a validation error naming the slug.
  - `TestDraftFormNotRegistered`: saving a draft emits nothing.
  - In site (`forms_any_test.go`):
    - `TestUpsertBuilderFormsAdoptsCaptured`: a captured `booking` becomes source `builder` with fields pinned, and its entries are kept.
    - `TestUpsertClosesRemoved`: republishing page P without form `x` sets `x.open=0`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement the model, validation, render and catalog. In `Publish`, emit after `UpdateAtomic` succeeds (never inside the tx; see the `hooks.Emit` doc). Site subscribes with `d.Hooks.Action(builderFormsPublished, 10, h.onBuilderForms)`, using a string literal and no builder import.
- [ ] **Step 4:** `go test ./internal/builder/... ./internal/site/...`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat(builder): form section kind published to site forms`.

### Task 4: Rendering: the `form` theme func and builder injection

**Files:**
- Modify: `internal/site/forms_any.go` (register `d.Theme.AddFunc("form", …)`), `internal/theme/builder_body.go` (inject for `gp-form`), `internal/builder/static/builder.css` (`.gp-form form` styles, mirroring `.gp-contact form`), `docs/THEME-DEVELOPMENT.md`
- Test: `internal/site/forms_render_test.go`, `internal/theme/builder_body_test.go`

**Interfaces:**
- Produces: `form(slug, csrf, back string) template.HTML`.
  - Markup: `<form class="sb-form gp-form-fields" method="post" action="/forms/{slug}" novalidate=false>` containing hidden `_csrf` and `_back`, the visually hidden honeypot `website`, and per field `<label for=…>`, an input by type (`required`, `maxlength=4000`, `dir="ltr"` for email and number), help as `<p id=…-help>` referenced by `aria-describedby`, `choice` as `<select>`, `checkbox` as a single checkbox, and `file` with `enctype` multipart. It ends with a submit button carrying `SubmitLabel` (default `t "site.forms.submit"`).
  - An unknown or closed slug renders nothing.
  - IDs are prefixed `sf-{slug}-`, so two forms on a page do not collide.

- [ ] **Step 1: Write failing tests:**
  - `TestFormFuncRendersFields`: each type renders its control, labels are tied to controls, required gets `required`, and help gets `aria-describedby`.
  - `TestFormFuncClosedRendersNothing`.
  - `TestBuilderBodyInjectsForm`: a body with a `gp-form` section for `booking` gets a form with `_csrf` equal to the request token, appended to `.gp-inner`. Without the `form` func registered, the section stays without a form and nothing panics.
  - `TestPreviewFormDisabled`: in builder preview (see `builder_preview.go`), injected forms have their controls and button `disabled`.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement. In `builder_body.go`, read `data-form` and call the func from the request's func map with `(slug, csrf, path)`.
- [ ] **Step 4:** `go test ./internal/site/... ./internal/theme/... ./internal/builder/...`. Expected: PASS.
- [ ] **Step 5:** Commit: `feat(site): render forms in builder sections and themes`.

### Task 5: Builder editor UI for form sections

**Files:**
- Modify: `web/admin/static/builder.js`, `web/admin/static/builder-admin.css`, `web/admin/templates/pages/builder/editor.html` (labels), the builder catalogs

**Interfaces:**
- Consumes: Task 3 `Section.Form` JSON shape and the `form` kind in `Catalog`.

- [ ] **Step 1:** Write the failing e2e `e2e/tests/builder-form-section.spec.ts`:
  - add a Form section;
  - set the title "Book a call", then check the slug auto-fills to `book-a-call` and stays editable;
  - add fields name (text, required), email (email, required) and plan (choice: basic, pro);
  - reorder with up and down buttons;
  - remove one field;
  - save, reload, and check the field list persists;
  - an Arabic title leaves the slug empty with an inline hint, and saving is blocked until a slug is set;
  - in `ar` and `en`, every field control has a label and buttons are 44px on a coarse pointer.
- [ ] **Step 2:** Run. Expected: FAIL.
- [ ] **Step 3:** Implement the form section editor in `builder.js`, following how repeated `items` are edited there (same patterns for add, remove, reorder and focus after remove).
- [ ] **Step 4:** Run the spec plus the existing builder specs (`ls e2e/tests | grep -i builder`). Expected: PASS.
- [ ] **Step 5:** Commit: `feat(builder): edit form section fields`.

### Task 6: End to end, docs and changelog

**Files:**
- Create: `e2e/tests/site-forms.spec.ts`, `docs/FORMS.md`
- Modify: `CHANGELOG.md` (`## [Unreleased]` → `### Added`), `docs/BOUNDARIES.md` (route table row for site: `/forms/{slug}` and `/admin/forms*`; hooks row: `builder.forms.published`)

- [ ] **Step 1:** Write `site-forms.spec.ts`:
  - **builder path:** create a page with a Form section, publish, visit as a visitor in `ar` and `en`, and submit. `/admin/forms/{slug}` shows the entry within a reload, and the CSV contains the field headers and values.
  - **capture path:** a page whose content contains a plain `<form method="post" action="/forms/quote">` with the CSRF token (inject it through a theme override, or use the `form` func on a captured slug after the first API submit). The form appears with discovered fields, and pinning works.
  - **checks:** filters and search work. Native validation blocks an empty required field, with focus on the first invalid field.
- [ ] **Step 2:** Run it together with every existing forms, contact, inbox and builder spec (`ls e2e/tests | grep -Ei 'form|contact|inbox|builder|submission'`). Expected: PASS.
- [ ] **Step 3:** Write `docs/FORMS.md` (EN and an AR section, like `CANVASES.md`) and add the CHANGELOG lines.
- [ ] **Step 4:** Run `go vet ./... && go test ./...` (in the background; it takes minutes). Expected: PASS.
- [ ] **Step 5:** Commit: `docs(forms): forms guide, changelog and boundaries`.
