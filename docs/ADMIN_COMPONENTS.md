# Admin components

The admin shares one small component set, defined at the end of
`web/admin/static/admin.css`. New and reworked admin pages use these classes
instead of ad-hoc Tailwind utility strings, so buttons, titles, tables and
dates look and behave the same everywhere. Direction and colours follow
[DESIGN.md](../DESIGN.md): light surfaces, petrol (`--accent`) for actions and
selection, green only for success, amber and red for warning and error.

## Basecoat (shadcn/ui for plain HTML)

The admin uses [Basecoat](https://basecoatui.com) 1.3.0, the shadcn/ui
component library for any stack: CSS classes plus a small vanilla JavaScript
runtime, no React. The CSS is compiled into the admin stylesheet:
`tools/tailwind/admin.css` imports Tailwind v4 and Basecoat's source bundle
(`web/admin/static/vendor/basecoat-1.3.0.bundle.css`, from the npm tarball),
and `tools/tailwind/build.sh` (`make css`) builds the committed
`web/admin/static/admin-tailwind.css` with the pinned Tailwind standalone CLI,
which `base.html` loads before `admin.css`. The JavaScript runtime is
`web/admin/static/vendor/basecoat-1.3.0.js`; both files sit beside their
licence and are recorded in `web/admin/static/cdn-provenance.json`. Every
admin and auth page can use Basecoat markup directly:

- `class="btn"` with `data-variant` (`secondary`, `outline`, `ghost`, `link`,
  `destructive`) and `data-size` (`xs`, `sm`, `lg`, `icon`, `icon-sm`).
- `class="badge"` with `data-variant`; `.card` with `<header>`, `<section>`,
  `<footer>`; `.alert` with `data-variant="destructive"`; `.field` wrapping a
  `<label>` and its control (inputs, selects, textareas, checkboxes, radios and
  `role="switch"` checkboxes are styled when they are direct children).
- Interactive components (dropdown-menu, tabs, popover, select, combobox,
  dialog/drawer, toast, sidebar, accordion) follow the Basecoat markup and are
  initialised automatically by `basecoat-1.3.0.js`; HTMX swaps are picked up
  by its mutation observer. Pilot: `pages/auth/login.html` and
  `partials/flash`.

Basecoat reads the shadcn tokens (`--background`, `--foreground`, `--card`,
`--primary`, `--secondary`, `--muted`, `--accent`, `--destructive`,
`--border`, `--input`, `--ring`, `--radius`, `--sidebar-*`), which `admin.css`
sets to the GoRun palette. Two names changed meaning in that move: shadcn's
`--accent` is a soft hover surface and `--muted` a soft background, so the old
petrol action colour is now `--primary` (hover `--primary-hover`) and the old
secondary text colour `--muted-foreground`. The GoRun colour utilities
(`bg-blue`, `border-line`, `text-ink`, …) are unchanged; secondary text is
`text-muted-foreground` (shadcn's name, which Basecoat defines; `text-muted`
would be the soft surface colour).

Basecoat's rules live in Tailwind's `components` cascade layer: utilities and
anything `admin.css` declares outside a layer win over them, and they win over
Tailwind's base reset. The admin has no in-browser Tailwind runtime: the
compiled stylesheet carries only the utilities found in the admin templates,
the admin scripts and the Go code that renders admin markup (see the
`@source` list in `tools/tailwind/admin.css`), and `make css-check` fails in
CI when it is stale. When a template needs a utility that is missing, run
`make css` and commit the result.

## Tokens

Defined on `:root` in `admin.css`. Older rules keep working through aliases
(`--radius`, `--radius-chip`, `--admin-space-*`).

| Token | Values |
|---|---|
| Type scale | `--fs-xs` 11px · `--fs-sm` 12px · `--fs-md` 13px · `--fs-base` 14px · `--fs-lg` 16px · `--fs-xl` 18px · `--fs-2xl` 22px (page titles) |
| Radius | `--radius` 10px (Basecoat base) → `--radius-sm` 6px (badges, chips), `--radius-md` 8px (controls), `--radius-lg` 10px (cards, panels), `--radius-xl` 14px |
| Spacing | `--space-1` … `--space-6` (0.25rem steps to 1.5rem) |
| Controls | `--control-h` 36px · `--control-h-sm` 32px · `--control-h-touch` 44px (every `.btn` on phones) |
| Colour | shadcn tokens `--background`, `--foreground`, `--card`, `--primary` (+ `--primary-hover`), `--secondary`, `--muted`, `--muted-foreground`, `--accent`, `--destructive`, `--border`, `--input`, `--ring`; GoRun aliases `--ink`, `--line`, `--panel`, `--bg`, `--blue*`; semantic `--green*`, `--amber*`, `--red*`. Public themes declare the same role names as aliases of their identity tokens ([role names](THEME-CONTRACT.md#role-tokens)), so a component written against them renders in the admin and on the site; the admin palette itself stays its own |

## Components

| Class | Use |
|---|---|
| `.btn` (Basecoat) + `data-variant` / `data-size`; legacy aliases `.btn-primary` (= plain `.btn`), `.btn-secondary` (= `data-variant="outline"`), `.btn-ghost`, `.btn-danger` (outlined destructive), `.btn-sm` | Every button or button-like link. One primary button per page area. |
| `.field` (Basecoat) wrapping `<label>` + control, with `.field-help`, `.field-error`; `.field-control` for a lone input; `.field-label` still accepted | Form controls. `.field-row` lays fields out in a wrapping row. |
| `partials/page-header` (`.page-header`) | Page title, subtitle, back link, muted ID disclosure, primary and secondary action. |
| `.section-head` + `.section-title` | Headings inside a page. |
| `.data-table-wrap` + `.data-table` | Tables. Cells: `.num`, `.actions`, `.row-link`, `.cell-sub`; `.row-actions` keeps row buttons on one line. |
| `.badge` (Basecoat pill) + `.badge-success` / `.badge-warning` / `.badge-danger` / `.badge-info`, or `data-variant` | Status labels. Neutral by default. |
| `.empty-state` | An empty list: say what is missing and, where useful, the next action. |
| `.kv` | Key/value lists (`<dl class="kv"><div><dt>…</dt><dd>…</dd></div></dl>`); one column on phones. |
| `.meta-row` + `.meta-item` | Inline "label value" metadata. Each item is a flex item, so Arabic and Latin values never reorder across items. |
| `.card` (Basecoat) + `.card-head` / `.card-body` / `.card-foot`, or `<header>` / `<section>` / `<footer>` | Only for a grouped unit such as a form or a request, never for every row. |
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
