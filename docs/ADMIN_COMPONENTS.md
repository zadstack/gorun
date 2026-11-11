# Admin components

The admin shares one small component set, defined at the end of
`web/admin/static/admin.css`. New and reworked admin pages use these classes
instead of ad-hoc Tailwind utility strings, so buttons, titles, tables and
dates look and behave the same everywhere. Direction and colours follow
[DESIGN.md](../DESIGN.md): light surfaces, petrol (`--accent`) for actions and
selection, green only for success, amber and red for warning and error.

## Tokens

Defined on `:root` in `admin.css`. Older rules keep working through aliases
(`--radius`, `--radius-chip`, `--admin-space-*`).

| Token | Values |
|---|---|
| Type scale | `--fs-xs` 11px · `--fs-sm` 12px · `--fs-md` 13px · `--fs-base` 14px · `--fs-lg` 16px · `--fs-xl` 18px · `--fs-2xl` 22px (page titles) |
| Radius | `--radius-sm` 4px (badges, chips) · `--radius-md` 7px (controls, panels) |
| Spacing | `--space-1` … `--space-6` (0.25rem steps to 1.5rem) |
| Controls | `--control-h` 36px · `--control-h-sm` 30px · `--control-h-touch` 44px (every `.btn` on phones) |
| Colour | the existing `--ink`, `--muted`, `--line`, `--panel`, `--bg`, `--accent*`, `--green*`, `--amber*`, `--red*` |

## Components

| Class | Use |
|---|---|
| `.btn` + `.btn-primary` / `.btn-secondary` / `.btn-ghost` / `.btn-danger`, optional `.btn-sm` | Every button or button-like link. One `.btn-primary` per page area. |
| `.field` with `.field-label`, `.field-help`, `.field-error`; `.field-control` for a lone input | Form controls. `.field-row` lays fields out in a wrapping row. |
| `partials/page-header` (`.page-header`) | Page title, subtitle, back link, muted ID disclosure, primary and secondary action. |
| `.section-head` + `.section-title` | Headings inside a page. |
| `.data-table-wrap` + `.data-table` | Tables. Cells: `.num`, `.actions`, `.row-link`, `.cell-sub`; `.row-actions` keeps row buttons on one line. |
| `.badge` + `.badge-success` / `.badge-warning` / `.badge-danger` / `.badge-info` | Status labels. Neutral by default. |
| `.empty-state` | An empty list: say what is missing and, where useful, the next action. |
| `.kv` | Key/value lists (`<dl class="kv"><div><dt>…</dt><dd>…</dd></div></dl>`); one column on phones. |
| `.meta-row` + `.meta-item` | Inline "label value" metadata. Each item is a flex item, so Arabic and Latin values never reorder across items. |
| `.card` + `.card-head` / `.card-body` / `.card-foot` | Only for a grouped unit such as a form or a request, never for every row. |
| `.raw-detail` | A `<details>` for raw JSON or other secondary payloads. |
| `.id-detail` | A raw identifier (ULID) that stays out of the visible text. |
| `.avatar` (`.avatar-user`) | Initials from the `initials` template function. |

## Page header partial

```gotemplate
{{template "partials/page-header" (dict
  "Title" (t "admin.keys.title") "Subtitle" (t "admin.keys.subtitle")
  "Action" "#key-create" "ActionLabel" (t "admin.keys.new") "ActionIcon" "plus"
  "ActionHook" (dataAttr "key-create-open"))}}
```

Keys: `Title` (required), `TitleDir` (`auto` for user-entered titles), `Subtitle`,
`Back` + `BackLabel`, `ID`, `Class`, `Action` + `ActionLabel` (+ `ActionIcon`,
`ActionHook`), `Secondary` + `SecondaryLabel` (+ `SecondaryIcon`). `dict`
builds the argument map; `dataAttr` adds one `data-*` hook for scripts and tests.

## Dates and times

There is one output for timestamps: the short UTC form `2026-11-10 18:04`,
left to right, with the full value in the tooltip.

- Templates: `{{datetime .CreatedAt}}` renders
  `<time datetime="2026-11-10T18:04:58Z" dir="ltr" title="2026-11-10 18:04:58 UTC">2026-11-10 18:04</time>`
  (an optional second argument adds a class). Empty values render `—`.
  `{{date .X}}` returns the same short text without markup, for use inside
  translated strings.
- Scripts: `window.gpDateTime.element(value)` builds the same `<time>` element
  and `window.gpDateTime.format(value)` returns the text
  (`web/admin/static/datetime.js`, loaded on every admin page). Do not use
  `toLocaleString()` for admin timestamps.

## Mixed-direction values

Names, keys, brands and other user values inside translated text go in
`<bdi>` (or their own element in a `.meta-row`), so an English key name in an
Arabic sentence keeps its place. IDs, slugs and code use `dir="ltr"`.

## Migrated pages

Agent channels (list and thread), API keys, agent approvals, private reports
(list and detail), the forms inbox (form entries and form submissions) and the
dashboard page header use these components. Other admin pages still use their
earlier utility classes and move over page by page.
