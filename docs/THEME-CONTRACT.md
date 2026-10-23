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
| `theme.json` | Present, valid JSON, non-empty `name`. Optional `logos` object (`primary`, `dark`, `light`, `icon` → file path under `assets/`, no traversal) ships theme marks; site settings `site.logo*` / `site.icon` always take precedence. Templates read them with `{{logo "dark"}}` (media, settings precedence), `{{themeLogo "icon"}}` (manifest URL), `{{brandImg . "thumbnail" "class"}}` (the header mark: a plain `<img>` unless a distinct dark logo exists, then a `<picture>` with a `prefers-color-scheme` source; the forced dark identity scheme swaps them) and `{{favicon}}` (site icon, then manifest icon). Shipped headers, footers and heads use them. |
| `supports` | Must include `rtl` and `identity` |
| Locales | Own `lang/en.json` and `lang/ar.json` (flat string maps, ≥1 key each) — inheritance alone is not enough for ship-with |
| Templates | Resolve via own files or parents: `index`, `page`, `single`, `archive`, `taxonomy`, `search`, `404`, `500`, `page-builder`, `page-contact`, `form-result`, plus `front-page` **or** `home` |
| Screenshot | `screenshot.png` at theme root |
| Head | Own or inherited `parts/head.html` / `head-extra.html` call `sections` (links `sections.css` only on pages whose content uses `gp-s-` blocks; a plain `sections.css` link is still accepted), `builderIdentity` (or `builderStyle`) and `identity` |
| Tokens | Primary stylesheet (`assets/site.css`, `theme.css`, or `style.css`) declares `:where(html[data-theme="<slug>"])` (default may use bare `html[data-theme="default"]`) |
| CSP | Theme HTML/CSS/JS must not reference known third-party script CDN hosts |

Custom (non-catalog) themes checked by doctor use a lighter bar: `index.html` must resolve
(`OptionsForSlug`). Use `ValidateDir(dir, roots, CatalogOptions())` in tests when you want
the full ship-with bar on a fixture.

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
