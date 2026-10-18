# Themes

> **ملخص بالعربية.** قالب GoRun مجلد من ملفات `html/template` في `themes/<slug>/`: قوالب التسلسل في
> `templates/`، والأجزاء في `parts/`، والمكوّنات في `components/`، والملفات الثابتة في `assets/`، والنصوص في
> `lang/<locale>.json`. يختار المحرك أول قالب موجود وفق تسلسل يشبه ووردبريس (`page-{slug}` ثم `page` ثم
> `single` ثم `index`…). القالب الفعّال هو الإعداد `site.theme`، ويُفعَّل من **المظهر ← القوالب**. يمكن
> تخصيص أي قالب من اللوحة أو عبر REST أو MCP كمسودة تُعاين ثم تُنشر، وتُحفظ التعديلات في
> `<DataDir>/overrides/<theme>/`. الكتالوج العام خمسة قوالب: `default` (مدونة)، و`business` (شركة وخدمات)،
> و`magazine` (مجلة ومحتوى)، و`portfolio` (أعمال شخصية)، و`docs` (توثيق). الأسماء القديمة (`minimal`
> و`studio-business` و`studio-editorial` و`studio-portfolio`) تعمل تلقائيًا كأسماء بديلة، والموقع الذي يستخدم
> أحدها ينتقل مرة واحدة مع إعداداته وتخصيصاته. القوالب المستقلة المبنية لموقع واحد تبقى
> خارج الكتالوج في مجلد منفصل وتُحمَّل على خوادمها فقط عبر `GORUN_THEMES_EXTRA`.

A GoRun theme is a directory of Go `html/template` files under `themes/<slug>/`, rendered by the theme engine (`internal/theme`). The active theme is the `site.theme` setting (default: `Config.Theme`, then `default`).

## The theme catalog

Five general themes ship in `themes/` and appear in **Appearance → Themes** (or `theme = "<slug>"` in `gorun.toml`). All are children of `default`, so shop, account, checkout and form templates come from it unless a theme overrides them.

**Switching themes in the admin:** open **Appearance → Themes**, activate a catalog slug (`site.theme`). That changes public rendering immediately for new requests. It does **not** create starter pages — use **Appearance → Build your site** for that ([SITE_BUILDER.md](SITE_BUILDER.md)). Initial install can also set `theme` in `gorun.toml` / `GORUN_THEME` before the first boot; afterwards prefer the admin so the change is audited.

| Slug | For | Character |
|---|---|---|
| `default` | personal blogs and general sites | calm reading layout, featured story, card grid with reading time, site search |
| `business` | companies and service businesses | full native shell, light surfaces, teal accent, 5 starter pages |
| `magazine` | magazines and editorial teams | paper tone, rust accent, serif, 4 starter pages |
| `portfolio` | independent professionals | light, red accent, 5 starter pages |
| `docs` | documentation | sticky search bar, page sidebar ordered by menu order |

`business`, `magazine` and `portfolio` are complete designs: they style navigation, footer, internal pages, article cards, search, empty results, contact and errors, load no public JavaScript or external fonts, and ship original local SVG artwork to replace before publication.

### Catalog contract (`pkg/themekit`)

Ship-with themes share one contract in `pkg/themekit` (`CatalogOptions` / `ValidateDir` / `ValidateCatalogRoot`):

- `theme.json` with a name; catalog children set `"parent": "default"`
- `supports` includes `rtl` and `identity`
- Own `lang/en.json` and `lang/ar.json` (not only inherited keys)
- Required templates resolve via inheritance (`index`, `page`, `single`, `archive`, `taxonomy`, `search`, `404`, `500`, `page-builder`, `page-contact`, `form-result`, plus `front-page` or `home`)
- `screenshot.png`; head (own or inherited) loads `sections.css`, `builderStyle`, and `identity`
- Primary stylesheet declares tokens under `:where(html[data-theme="<slug>"])`
- No third-party script CDN hosts in theme HTML/CSS/JS

CI covers this through `TestShipWithCatalogMeetsThemekitContract` and `pkg/themekit` tests. Prefer inheriting parts and components from `default` over copy-paste; theme-specific chrome stays in each theme’s CSS. When `gorun theme doctor` lands, it should call the same validators.

### Retired slugs

Installed themes keep their exact slug and implementation, including legacy parents used by custom themes. Only when the old theme is absent do earlier slugs resolve to catalog replacements for activation, parent chains, starters and provisioning tools:

| Retired | Now |
|---|---|
| `minimal` | `default` |
| `studio-business` | `business` |
| `studio-editorial` | `magazine` |
| `studio-portfolio` | `portfolio` |

A site whose active theme is a retired slug (including one selected by `GORUN_THEME` or `gorun.toml`) migrates at boot only when its original theme is no longer installed. Binary-only upgrades retain existing legacy themes, parent chains, settings and overrides. Published overrides and drafts are copied into the replacement theme, preserving nonconflicting files already there and retaining the original files. Differing files at the same path stop startup with an explicit conflict instead of silently dropping a customization. Symlinks and special files are rejected. After copying succeeds, `site.theme` and missing `theme.<new>.*` settings are committed in one database transaction; existing destination settings are preserved. Interrupted migrations can be retried, and original keys/files remain available for rollback. Successful migration is audited as `theme.migrate`.

### Custom themes outside the catalog

Themes built for one site can live outside `themes/`, in their own directory, and stay out of the catalog. General releases do not ship them. Their servers load them with `GORUN_THEMES_EXTRA` (a PATH-style list searched after `GORUN_THEMES`). `internal/theme/testdata/extra/custom-fixture` is a minimal example.

Activating a theme alone changes rendering but does not create its starter pages. Use **Appearance → Build your site** to prepare a complete private draft site. Existing starter runs preserve the theme recorded when they were created; preparing a new run selects the current design. The starter's preset remains editable, and later identity changes override its defaults rather than being fixed in theme CSS.

## Layout

```
themes/<slug>/theme.json          {"name","version","parent","locations","settings","supports"}
themes/<slug>/templates/*.html    hierarchy templates, named "single.html", "page-pricing.html", …
themes/<slug>/parts/*.html        called with {{part "header" .}}      (name "parts.header.html")
themes/<slug>/components/*.html   called with {{component "card" $x}}  (name "components.card.html")
themes/<slug>/assets/**           served at /themes/<slug>/assets/…; link with {{asset "style.css"}}
themes/<slug>/lang/<locale>.json  flat {"key": "text"} map read by {{t "key"}}
```

`settings` is a list of fields in the content field format (`key`, `type`, `label`, `help`, `options`, `min`, `max`, …).
**Appearance → Theme settings** (permission `themes.manage`) renders the active theme's list with the admin field
partials (`text`, `textarea`, `number`, `boolean`, `select`, `date`, and `image` through the media picker; other
types are edited as text), validates it with the content field rules, and stores each value as the setting
`theme.<slug>.<key>`, read in templates with `{{setting "theme.<slug>.<key>"}}`. Saves are audited
(`theme.settings.update`).

Each template file is parsed on its own (parts and components into a shared base, each hierarchy template into a clone of it), so a broken file only breaks the pages that use it. Templates are re-parsed on every request in dev (`GORUN_ENV=dev`) and cached in production.

## Hierarchy

The first existing candidate wins:

| Kind | Candidates |
|---|---|
| home | `front-page` → `home` → `index` |
| page | `page-{slug}` → `page-{template}` → `page` → `single` → `index` |
| single | `single-{type}-{slug}` → `single-{type}` → `single` → `index` |
| archive | `archive-{type}` → `archive` → `index` |
| taxonomy | `taxonomy-{tax}-{term}` → `taxonomy-{tax}` → `taxonomy` → `archive` → `index` |
| search | `search` → `archive` |
| 404 / 500 | `404` → `index` / `500` |
| module kinds | registered with `d.Theme.AddKind` (site: `form-result`; commerce: `shop`, `product`, `account`, `checkout-result`), then `index` |

`page-{template}` uses the item's template with any `page-` prefix and `.html` suffix removed. When `site.front_page` points at a page, the home URL renders that page through the page hierarchy, not `front-page.html`.

## Context and funcs

Every template receives a `*theme.Context`: `Site`, `Request`, `Kind`, `Content`, `Items`, `Term`, `Pagination`, `Query`, `User`, `Theme`, `Extra`, `Locale`, `Dir`, `Status`, `Alternates`. Modules add to `Extra` through the `theme.render.context` filter. The site sets `.Extra.title` on listings and `.Extra.form_flash` (`"ok"` or `"error"`) after a contact-form redirect.

| Func | Use |
|---|---|
| `t "key" args…` | theme `lang/<locale>.json`, then `en`, then the platform catalogs, then the key itself |
| `dir`, `locale`, `site` | text direction, locale, site info |
| `setting "key"` | public settings only (`site.`, `seo.`, `theme.`, `social.`, `contact.`); never secrets; `""` when unset |
| `url x` | absolute URL from a path, content item, term or menu item. An item links to its own locale; a term, `"/"` and `"/search"` stay in the page's locale; other paths are left as given |
| `asset "p"` | cache-busted theme asset URL |
| `menu "loc"` | the menu at a location, with URLs resolved, or nil |
| `query (dict …)` | content query: `type status search parent locale page per_page order taxonomy fields` |
| `content id`, `field . "k"`, `terms . "tax"` | single item, custom field, resolved terms |
| `media id`, `img m "variant" "class"` | media item; responsive `<img>` with srcset, alt and size |
| `comments .` | for an item whose type supports `comments`: `{Open, ContentID, Action, Back, Count, Items}` (approved comments, one reply level, never an email), or nil; the form posts `content_id`, `name`, `email`, `body`, `_back`, `website` (honeypot) and `_csrf` to `.Action`, and the page then shows `.Extra.comment_flash` (pending, approved, invalid, closed, limited). `default` and `business` ship a `comments` component |
| `identity .` | the site identity `<style>` (and font links) from Appearance → Theme settings, only for a theme whose own `theme.json` `supports` lists `identity`; print it last in `<head>` (see [Site identity](#site-identity)) |
| `seo .` | head meta (title, description, canonical, OG) and the `hreflang` alternates |
| `part "name" .`, `component "name" data` | include a part or component |
| `paginate`, `date v "layout"`, `money minor "CUR"`, `excerpt v n` | formatting |
| `safe`, `dict`, `add`, `csrf`, `nonce` | already-sanitized HTML, map literal, integer add, CSRF token, CSP nonce for inline scripts |
| `products`, `product "slug"` | registered by the commerce module (see below) |

**Locales.** The site locale (`site.locale`) is served unprefixed; another supported locale lives under `/<locale>/`
(`/en/post/x`), and the site locale's own prefix redirects there (`/ar/x` → `/x`). Pages in that locale get
`locale`, `dir` and theme strings of that locale, and lists (home, archives, terms, search, `query`) hold its items
plus items stored without a locale. `.Alternates` is the page in each locale it exists in (`Locale`, `Label` in
that language, absolute `URL`): published translations for an item, the same path for lists. A header language link:
`{{range .Alternates}}{{if ne .Locale locale}}<a href="{{.URL}}" hreflang="{{.Locale}}" lang="{{.Locale}}">{{.Label}}</a>{{end}}{{end}}`.
`menu "loc"` returns the location's menu for the page's locale, else its menu for every locale; menu links
point where they were saved.

Module funcs are resolved when templates are parsed. The engine registers empty fallbacks for the commerce funcs (`products` returns no groups, `product` returns nil), so any theme can call them and the CLI (`gorun theme save`, which boots no commerce) validates such templates; the commerce module replaces them when it is booted. Wrap their use in `{{with products}}…{{end}}` so the section disappears on sites without a store. Tests that need real products still register stand-ins with `d.Theme.AddFunc` (`internal/theme/commerce_stub_test.go`).

Public pages allow scripts from the site itself (`/static/...`, theme assets) and inline scripts only with `nonce="{{nonce}}"`. Third-party script hosts and `'unsafe-eval'` are not allowed, so vendor libraries under the theme's assets, and note that the standard Alpine build (which evaluates its directives with `new Function`) does not run. Inline event handler attributes (`onclick`) are blocked. Styles from Google Fonts are allowed. The bundled themes need nothing more; the default theme uses a precompiled Tailwind stylesheet (see [Tailwind in the default theme](#tailwind-in-the-default-theme)) and the local HTMX. A custom theme that still needs a CDN or Alpine can have the operator allow it with `GORUN_CSP_SCRIPT_SRC` (or `csp_script_src` in `gorun.toml`): https origins or URL prefixes, plus the keyword `'unsafe-eval'`, for example `GORUN_CSP_SCRIPT_SRC="https://cdn.jsdelivr.net 'unsafe-eval'"`. Broader sources (`https:`, `*`, `'unsafe-inline'`) are refused at startup. These apply to public pages only, never to the admin.

## Tailwind in the default theme

The default theme (and child themes that keep its `parts/head.html`, such as `docs`) styles its markup with
Tailwind utilities compiled ahead of time into `themes/default/assets/tailwind.css`, which is linked last in
the head. No compiler runs in the browser and the app needs no Node.

- **Rebuild:** `make css` (or `tools/tailwind/build.sh`) downloads the pinned Tailwind standalone CLI (v3.4.17)
  once, verifies its SHA-256 against the upstream release, and writes the minified CSS. Commit the result; CI
  runs `tools/tailwind/build.sh --check` and fails when the committed file is stale. Rebuild after adding or
  changing utility classes in the default or docs theme or in Go code that renders public markup.
- **What is scanned** (`tools/tailwind/tailwind.config.js`): default and docs theme templates, parts,
  components and scripts; the bundled starter content; and the Go files that render public markup
  (`internal/theme`, `internal/commerce`, the builder renderer and starters), where only `class="…"` attribute
  values count. Add a Go file to that list when it starts emitting utility classes on public pages.
- **Safelist** (`tools/tailwind/safelist.txt`): classes that must be present even when no scanned file spells
  them out, one per line. Add a class there only when it reaches public pages from data at runtime.
- **Content HTML** cannot add utilities: the sanitizer keeps only `ql-*`, `gp-*` and `language-*` classes, which
  the theme CSS styles, so post and page bodies never depend on the Tailwind build.
- **Runtime opt-in:** a custom child theme, an extension template or a template override that uses utilities
  missing from the build can load the in-browser compiler again (the local Play build, about 400 KB of
  JavaScript per page) by adding `{{part "tailwind-runtime" .}}` to its `parts/head-extra.html`. Prefer
  rebuilding the CSS when the classes are known.

## Site identity

A theme opts in by listing `"identity"` in its `theme.json` `supports` (the five catalog themes `default`, `business`, `magazine`, `portfolio` and `docs` do). Its **Appearance → Theme settings** page then ends with an identity group the engine adds (a theme that declares one of these keys keeps its own definition), and `{{identity .}}` prints the stored values. Values are stored per theme as `theme.<slug>.<key>`; empty values keep the theme's design.

The capability is **not inherited**: a child theme gets it only when its own `theme.json` declares it. Custom themes built on a catalog parent often use their own private tokens that the identity settings do not drive, so they do not declare it: the fields are hidden and `{{identity .}}` prints nothing for them even when values were stored earlier or the head they inherit calls it. Declare `"identity"` only after checking that your theme's colors and fonts come from the shared tokens below, in both schemes.

| Key | Values | Effect |
|---|---|---|
| `identity_accent`, `identity_background`, `identity_text` | hex `#rgb` / `#rrggbb` | Theme tokens (`--green`/`--accent`/`--gp-accent`, `--bg`, `--ink`, …) |
| (derived from `identity_accent`) | `#ffffff`, `#111111` or `#000000` | `--on-accent` / `--gp-on-accent`: the text color on accent fills, the first of those reaching 4.5:1 on the accent. Any accent is accepted (a light amber gets dark button text); theme CSS writes text on accent fills as `color: var(--on-accent, #fff)` (Tailwind: `text-on-accent`) |
| `identity_heading_font`, `identity_body_font` | `zad`, `ibm-plex-arabic`, `noto-naskh`, `noto-kufi`, `readex`, `tajawal`, `cairo`, `amiri` | `--font-heading` (h1–h4), `--font`/`--font-ui`. All support Arabic; Zad is bundled, the others load from Google Fonts. |
| `identity_scheme` | `light`, `dark` | `dark` applies a matching dark palette (explicit colors still win) |
| `identity_radius` | `square`, `soft`, `round` | `--radius` 0 / 8px / 16px |
| `custom_css` | ≤ 20000 chars, no HTML, `@import`, `expression()` or `javascript:` | Appended after the theme's styles and kept across theme updates |

Themes print them with `{{identity .}}` as the last thing before `</head>` (after their stylesheets and `head-extra`). The output is a `<style nonce data-gorun-identity>` whose tokens use `:root:root[data-theme]`, which outranks the catalog themes' `:where(html[data-theme="…"])` tokens without `!important`. Values are validated on save and again at render, so a hand-edited setting cannot inject markup. Use identity settings and custom CSS instead of overriding `parts.head*.html` to restyle a site.

### Builder identity and site identity together

Two settings can restyle the same tokens:

- `builder.identity` (accent, background, text, font, width) comes from the site builder and starters. `{{builderStyle}}` prints it as `:root{…}` (specificity 0,1,0) and sets the whole palette (`--green`, `--bg`, `--ink`, `--panel`, `--line`, `--font`, …).
- The site identity settings above are per theme. `{{identity .}}` prints them as `:root:root[data-theme]{…}` (0,3,0) after the builder block.

Precedence, per token: **site identity › builder identity › the theme's own tokens**. A token the site identity leaves empty keeps the builder value, and one neither sets keeps the theme default. For example, a builder accent of `#167252` with a site identity accent of `#c2410c` gives `#c2410c`, and the builder's background and text colors stay. Every catalog head prints `{{builderStyle}}` before `{{identity .}}`; `TestSiteIdentityOutranksBuilderIdentity` checks this. Custom CSS comes last in the identity block, so it can override both.

### Writing theme CSS that others can restyle

Declare tokens inside `:where(html[data-theme="<slug>"])` (zero specificity) and style components through those tokens. Keep component selectors short (one class, or `:where()` around structural parts) so a child theme, the identity settings or custom CSS can change a color or a font without out-ranking long `:is(...)` chains.

## Section library

The content sanitizer keeps only `class` values that start with `gp-` (plus editor classes), so page bodies written as HTML use the `gp-s-*` blocks from `themes/default/assets/sections.css`. Every catalog theme loads it. Its base selectors have zero specificity (`:where()`) and use the theme tokens, so the blocks follow the theme, the identity settings and custom CSS. Page bodies render inside `.prose-gp`, whose rules (link color and underline, list markers, `h2`/`h3` sizes, sibling margins) reach (0,2,2) specificity in the catalog themes; a short block at the end of `sections.css` re-applies exactly those properties under `:root .prose-gp` (0,3,0). To restyle a block inside a page body, use the same prefix (`:root .prose-gp .gp-s-btn{…}`) or custom CSS, which loads later. (The page builder owns `gp-section`, `gp-grid`, `gp-entry` and friends.)

| Class | Use |
|---|---|
| `gp-s-hero` with `gp-s-eyebrow`, `h1`, `gp-s-lead`, `gp-s-actions` | Page opening |
| `gp-s-btn`, `gp-s-btn gp-s-btn-ghost` | Primary / secondary links |
| `gp-s-band` | A titled section (`h2`, `p`, then any block) |
| `gp-s-grid` of `gp-s-card` (`h3`, `p`, trailing `a`), `gp-s-price` | Services, features, plans |
| `gp-s-steps` (`ol` of `li`) | Numbered process |
| `gp-s-checks` (`ul`) | Checklist |
| `gp-s-faq` with `details`/`summary` | Questions |
| `gp-s-cta` | Closing call to action |

```html
<section class="gp-s-hero"><span class="gp-s-eyebrow">Services</span><h1>What you do, for whom.</h1>
  <p class="gp-s-lead">One or two sentences on what a customer gets.</p>
  <p class="gp-s-actions"><a class="gp-s-btn" href="/contact">Contact us</a></p></section>
<section class="gp-s-band"><h2>What we offer</h2><div class="gp-s-grid">
  <article class="gp-s-card"><h3>Service</h3><p>What it includes.</p><p class="gp-s-price">49 AED</p><a href="/shop">Details</a></article>
</div></section>
```

The starter content's About page (`gorun starter`) is built from these blocks as an editable example.

## Reparenting a child theme

`gorun theme reparent <child> <parent>` reports, without changing anything, which inherited templates, parts and components would now come from another theme and which `part`/`component` references in the child would no longer resolve. `--apply` refuses while references are missing or the theme directory is not writable; otherwise it backs up `theme.json` to `theme.json.bak-<UTC time>`, rewrites only the top-level `"parent"` value (the rest of the file, nested `parent` keys included, stays byte for byte; the key must already exist, as a string or `null`) and records `theme.reparent` in the audit log. Restart the server afterwards. Use it to move custom themes off a retired parent (for example `studio-editorial` → `magazine`) and get the catalog's design updates.

## Overrides

- **Child themes:** set `"parent": "<slug>"` in `theme.json`. Any file the child lacks falls back to the parent, and lang files merge with the child winning.
- **Template editor / API / MCP:** overrides live in `<DataDir>/overrides/<theme>/` and win over the theme files. They can be saved as drafts and previewed with `?gp_preview_override=1` by users with `themes.manage`, then published. Draft previews are sandboxed static HTML: JavaScript, forms and external assets are blocked; review interactive behavior on a separate staging site. MCP draft creation requires approval. Override names use the dotted form (`parts.header.html`).
- A template that fails to render produces the theme's `500.html` (or a built-in page) and logs the error. It never shows a blank page.

---

## Visual site building

`/admin/site-builder` prepares company, portfolio and magazine page sets and an unassigned navigation menu. Creation never changes the live front page, theme, site identity or publication status. Review the pages through the section editor and activate explicitly; activation publishes the reviewed drafts and applies the menu, locale, selected Studio theme, front page and identity in one transaction. A stale content or layout version rejects activation rather than overwriting newer work. Run tokens are owner-bound, and retries resume the same drafts. Company and portfolio prepare five pages each; magazine prepares four. The run records its theme selection so revisiting an earlier run does not silently change its design.

`/admin/builder/<page-id>` edits 12 section kinds: hero, text, features, services, projects, testimonials, pricing, FAQ, statistics, logos, contact and call to action. Layouts support default, split and centered variants; the preview reflects page language, not admin language. For starter pages it renders the proposed theme, title and menu; for existing pages it renders the active installed theme. Both paths preserve the theme shell and stylesheet order without changing live content or identity. This works for custom themes as well as the Studio designs. Fields are plain text and safe URLs; the builder does not execute arbitrary user HTML. Up/down buttons complement drag ordering, and undo/redo operate within the open editor session.

Autosave persists a separate structured draft in `builder_documents`, leaving public HTML untouched. Publishing joins rendered HTML, indexes, revisions and the published JSON snapshot in one transaction. Reset restores the last published JSON snapshot if present; a legacy draft without that snapshot resets to an empty editor. Converting an existing HTML body requires acknowledgement. Content revisions can restore HTML, but there is not a full historical timeline of JSON layouts; an external HTML change causes a conflict and explicit reconciliation.

The default theme and all three Studio designs use `{{builderBody .Content}}` in `page-builder.html`, `{{builderStyle}}` for validated identity tokens, and `/static/builder/builder.css`. Custom themes can integrate those helpers and provide `components/contact-form.html` and `components/card.html`; merely selecting a custom theme does not guarantee builder styling. `builderBody` inserts real contact forms and public-only post feeds inside their section, preserving ordering. Sandbox previews show those components but block script execution and form submission. Stylesheets from the selected installed theme and its parents are embedded at their original cascade positions; bounded theme-local fonts and CSS images are embedded too, so they work inside the isolated preview. Asset reads reject traversal and escaping symlinks, with a 2 MiB per-file and 8 MiB total limit. External stylesheets and fonts are not loaded. JavaScript-only controls and enhancements must be tested on a staging site; the editor intentionally remains an inert preview.

`/admin/appearance/identity` changes the shared logo, title, tagline, accent/background/text colors, system/serif/mono font and narrow/standard/wide width. Saving applies the design to compatible live templates. Text/background contrast and white button-label contrast must be at least 4.5:1. Public links use a readable text color independently of the button fill.

The starter sample copy is editable and does not claim real customers or results. The builder does not generate a whole site from an AI prompt. Layout documents can be imported/exported from the editor or read/saved through authenticated REST and MCP operations documented separately. Import validates the complete file before replacing the working draft and remains undoable; images are URLs and their assets must be transferred separately.

## Guided profiles and readiness

The starter wizard captures optional project description, about copy, 1–6 offering/topic names, contact email/phone/address, logo media and hero media before creating pages. It reuses the native media picker and validates existing image records. Description populates home excerpt/SEO metadata; selected hero populates featured media. All three designs receive the same supplied profile through their own layouts.

Captured profiles remain stable on retry. Draft previews receive a request-scoped logo override; no public settings change during preparation or preview. Activation stores only nonempty supplied contact/logo settings in the existing batch transaction. Missing selected media blocks activation with an actionable review.

`/admin/appearance/readiness` is a read-only local report for administrators with `settings.manage`. It inspects public content, raw menu references, local image files, metadata and configuration presence; remote requests, rendering, mail delivery and restore remain unverified. Partial scans cannot produce blanket success. See [the user guide](SITE_BUILDER.md).
