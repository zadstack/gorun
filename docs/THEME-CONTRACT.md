# Theme contract

> **ملخص بالعربية.** عقد قوالب الكتالوج في `pkg/themekit`: التحقق من `theme.json` والقوالب
> واللغات ولقطات الشاشة ورموز الهوية وغياب شبكات CDN للسكربت. يفرضه اختبارات الكتالوج
> والأمر `gorun theme doctor` (نفس `ValidateDir` / `CatalogOptions`). رفع مظهر القوالب
> المرفقة عمل منفصل عن إضافة واجهة الطبيب.

GoRun’s **catalog theme contract** lives in [`pkg/themekit`](../pkg/themekit). Ship-with
themes under `themes/` must pass `ValidateDir` with `CatalogOptions()`. The CLI
`gorun theme doctor` calls the same helpers (no kernel / database boot).

This document expands the short summary in [THEMES.md](THEMES.md#catalog-contract-pkgthemekit).

## Commands

```sh
gorun theme doctor                 # all themes under GORUN_THEMES (+ GORUN_THEMES_EXTRA)
gorun theme doctor magazine        # one slug
gorun theme doctor --json          # []Result (+ optional warnings)
gorun theme doctor --warnings      # soft {{t "key"}} presence hints (exit still follows contract only)
```

Exit code `1` when any contract `Result` has `OK == false`. Soft lang-key warnings never
fail the process by themselves.

## Catalog bar (`CatalogOptions`)

| Check | Rule |
|---|---|
| `theme.json` | Present, valid JSON, non-empty `name`. Optional `logos` object (`primary`, `dark`, `light`, `icon` → file path under `assets/`, no traversal) ships theme marks; site settings `site.logo*` / `site.icon` always take precedence. Templates read them with `{{logo "dark"}}` (media, settings precedence), `{{themeLogo "icon"}}` (manifest URL), `{{brandImg . "thumbnail" "class"}}` (the header mark: the dark logo on a dark page background, the primary logo otherwise; a `<picture>` that follows `prefers-color-scheme` only for a theme declaring `color-scheme:auto`, see [THEMES.md](THEMES.md#rules-that-keep-the-layers-readable)) and `{{favicon}}` (site icon, then manifest icon). Shipped headers, footers and heads use them. |
| `supports` | Must include `rtl` and `identity` |
| Locales | Own `lang/en.json` and `lang/ar.json` (flat string maps, ≥1 key each) — inheritance alone is not enough for ship-with |
| Templates | Resolve via own files or parents: `index`, `page`, `single`, `archive`, `taxonomy`, `search`, `404`, `500`, `page-builder`, `page-contact`, `form-result`, plus `front-page` **or** `home` |
| Screenshot | `screenshot.png` at theme root |
| Head | Own or inherited `parts/head.html` / `head-extra.html` call `sections` (links the [core site stylesheet](#core-site-stylesheet) on every page, the store stylesheet on commerce pages and `sections.css` only on pages whose content uses `gp-s-` blocks; a head without the call must link both `sections.css` and `/static/site/core.css` itself), `builderIdentity` (or `builderStyle`) and `identity` |
| Tokens | Primary stylesheet (`assets/site.css`, `theme.css`, or `style.css`) declares `:where(html[data-theme="<slug>"])` (default may use bare `html[data-theme="default"]`) |
| CSP | Theme HTML/CSS/JS must not reference known third-party script CDN hosts |

Custom (non-catalog) themes checked by doctor use a lighter bar: `index.html` must resolve
(`OptionsForSlug`). Use `ValidateDir(dir, roots, CatalogOptions())` in tests when you want
the full ship-with bar on a fixture.

## Core site stylesheet

Every theme gets `/static/site/core.css` from `{{sections .}}`, served from the binary with a
content hash. It gives new themes correct defaults without adding work to them:

- **Element defaults** for page content: labels, fields, selects, text areas, unclassed buttons,
  tables, code, quotes, alerts and status messages, and images and long words that never widen
  the page. Under `<main>` every rule is wrapped in `:where()`, so its specificity is zero and
  any rule a theme writes wins. Colours, radius and fonts come from the theme and identity tokens.
- **The shared functional templates** come from `default` for every theme: `account`, `cart`,
  `checkout`, `checkout-result`, `shop`, `product`, `private-intake` and `form-result` (children
  may override `form-result`). They wrap their content in `<div class="gp-core">`. The core
  stylesheet carries the utilities those templates use, scoped under `.gp-core` and without
  Tailwind's reset, so they look right under any theme and never restyle a theme's own markup.
  Inside `.gp-core` the element defaults weigh one class, enough to beat an element reset.
- **The store stylesheet** (`commerce.css`) is linked on every commerce page. Its buttons use the
  `--accent`, `--on-accent`, `--accent-ink` and `--panel` tokens, so they follow the theme and the
  site identity.

A theme keeps its character by styling its own classes and elements; it does not need to restyle
the shared pages. Rebuild the stylesheet with `tools/tailwind/build.sh` after changing a shared
template's classes (CI runs `--check`).

## Page composition helpers

Template funcs every theme can use to compose complete pages from real data. Each returns nothing
(or `Available` false) when the data does not exist, so a theme never shows an empty or invented
part.

| Func | Returns | Rules |
|---|---|---|
| `{{destination "contact"}}` | `{Available, Fallback, Purpose, URL, Title, Locale, Item}` | Purposes are `about`, `service`, `methodology` and `contact`. It takes a published public page with that purpose in the current locale, else one with that slug; else the current-locale translation (translation group) of such a page in another locale; else that other-locale page with `Fallback` true and its `Locale`, which a template must show as another language. Several matches: the oldest published, then the lowest id. It never builds a URL from a guessed slug. |
| `{{related . 3}}` | up to N items (max 12) | Same type and locale, sharing at least one term, published and public, excluding the item; newest first, then newest id. |
| `{{adjacent .}}` | `{Prev, Next}` | `Prev` is older and `Next` newer, same type and locale, published and public, by publication date then id; looks at the newest 1000 items. |
| `{{termsOf "category" "post"}}` | `[{Term, Count}]` | Terms with published public items of that content type in the page locale, with that count; at most 50 terms. Link them with `{{url .Term}}`. |

**Page purpose.** The page type has a `purpose` select field (content migration 0007, added only
when the type has none). The page template order is `page-<slug>`, `page-<template>`,
`page-<purpose>`, `page`, `single`, `index`, so a builder page keeps `page-builder.html`. Only the four
known purposes reach the hierarchy; any other stored value is ignored. Editors create purpose pages from builder presets ([SITE_BUILDER.md](SITE_BUILDER.md#english--purpose-pages)).

**Default components** (`themes/default/components`), each rendering nothing without data:

| Component | Use |
|---|---|
| `article-meta` | `{{component "article-meta" .Content}}`: publication date and reading time. No "updated" date: edits and autosaves change it without meaning. |
| `article-toc` | `{{$toc := toc .}}{{component "article-toc" $toc}}` and print `{{$toc.Body}}` as the body, never `.Body`: the list links to the ids `toc` adds to that body. Hidden below three h2/h3 headings. |
| `article-related` | `{{component "article-related" (related .Content 3)}}` |
| `article-adjacent` | `{{component "article-adjacent" (adjacent .Content)}}` |
| `archive-filter` | `{{component "archive-filter" (dict "Terms" (termsOf "category" "post") "Current" .Term)}}`, linking real term archives; hidden below two terms. |

The default `single`, `archive` and `taxonomy` templates use them; a child theme that overrides those
templates includes the components where it wants them. Their base styles are in the core site
stylesheet: one class per rule, so they beat an element reset, and a theme restyles them with its
own scope (`html[data-theme="x"] .gp-article-toc`).

## Package surface

| API | Role |
|---|---|
| `ValidateDir` / `ValidateCatalogRoot` / `CatalogOptions` | Authoritative contract (CI + doctor) |
| `DoctorTheme` / `DoctorRoots` / `OptionsForSlug` | CLI-oriented wrappers |
| `FormatResult` / `AnyFailed` | Human output / exit helpers |
| `ExtractTKeys` / `MergeLang` / `LangKeyWarnings` | Soft lang tooling (`--warnings`) |
| `AssetPath` / `DataThemeSelector` / `SettingKey` / `IdentityTokens` | Small authoring helpers |

Stdlib only — no new runtime dependencies. Do not import `internal/…` from themekit.

## CI

- `pkg/themekit.TestValidateCatalogRootShipWith`
- `internal/theme.TestShipWithCatalogMeetsThemekitContract`

Both call `ValidateCatalogRoot` / `ValidateDir` with `CatalogOptions`. Doctor must stay
aligned with those entry points.

## Scope boundaries

- **This contract + doctor** — validation and operator CLI.
- **Ship-with visual uplift** — CSS/markup/lang quality of `business` / `magazine` /
  `portfolio` / `docs` (parallel PR); themekit enables that work, it does not replace it.
