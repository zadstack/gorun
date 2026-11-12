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
| `.row-menu` (`row-menu.js`) | One actions button per table row for secondary and destructive actions. See [Row actions](#row-actions). |
| `.danger-zone` | The last section of a settings or detail page, holding its destructive actions. See [Danger zone](#danger-zone). |

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

## Row actions

Keep at most one obvious action visible in a row (often the row link itself)
and put the rest behind one menu button. `row-menu.js` (loaded on every admin
page) enhances the markup; without JavaScript the button stays hidden and the
items render inline as small buttons, so every action still works.

```gotemplate
<div class="row-menu" data-row-menu>
  <button type="button" id="key-actions-{{.ID}}" class="btn btn-secondary btn-sm row-menu-button"
          data-row-menu-button aria-haspopup="menu" aria-controls="key-menu-{{.ID}}" hidden>
    {{icon "ellipsis"}}<span class="sr-only">{{t "core.action.row_actions" .Name}}</span></button>
  <div class="row-menu-list" id="key-menu-{{.ID}}" data-row-menu-list>
    <form method="post" action="…/rotate" data-confirm="…">…
      <button type="submit" name="grace" value="3600" class="row-menu-item" data-row-menu-item>…</button></form>
    <form method="post" action="…/revoke" data-confirm="…">…
      <button type="submit" class="row-menu-item row-menu-item-danger" data-row-menu-item>…</button></form>
  </div>
</div>
```

- Items are the forms' own submit buttons (or links), so routes and posted
  fields do not change; a choice that used to be a `<select>` becomes one item
  per value with the same `name`/`value`. `data-confirm` still asks first and
  submits the chosen button's value.
- Keyboard: Enter, Space or ↓ opens on the first item, ↑ on the last;
  ↑ ↓ Home End move; Esc closes and returns focus to the button; Tab closes.
  A click outside, scrolling or resizing closes it.
- The list is fixed-positioned below the button (above it near the bottom of
  the screen), aligned to the button's inline end and kept inside the
  viewport, so a scrolling table never clips it.
- `.row-menu-item-danger` goes last and is separated by a rule.

Used by API keys and saved connections.

## Danger zone

Destructive actions on a settings or detail page (delete, uninstall) go in one
section at the very bottom of the page, after the form and its side panels:

```gotemplate
<section class="danger-zone" aria-labelledby="user-danger-title">
  <h2 id="user-danger-title" class="danger-zone-title">{{t "core.danger.title"}}</h2>
  <div class="danger-zone-item">
    <div class="danger-zone-text"><p class="danger-zone-label">{{t "admin.users.danger"}}</p>
      <p class="danger-zone-help">{{t "admin.users.delete_help"}}</p></div>
    <form method="post" action="/admin/users/{{.ID}}/delete" data-confirm="…">…
      <button type="submit" class="btn btn-danger btn-sm">{{icon "trash-2"}}<span>…</span></button></form>
  </div>
</section>
```

Each item says what it removes and has one `.btn-danger`; keep the existing
`data-confirm`. Several items stack with a divider. On phones the button spans
the width. Used by users, roles, forms, extensions, products and billing
servers.

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
