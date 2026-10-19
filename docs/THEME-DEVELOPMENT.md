# Build a GoRun theme / إنشاء قالب GoRun

> **بالعربي:** الدليل ده للمطور اللي عايز يعمل Theme جديد، من أول المجلد والملفات لحد الاختبار والتفعيل. استخدم **Child Theme** وارث من `default` عشان تعيد استخدام القوالب الأساسية بدل ما تنسخها كلها. التفاصيل الكاملة في [THEMES.md](THEMES.md)، ومعايير الفحص في [THEME-CONTRACT.md](THEME-CONTRACT.md).

GoRun themes are server-rendered Go `html/template` files plus local CSS/assets. No Node, React or PHP is needed at runtime. This guide describes **GoRun Full**; Cloudflare Edge theme compatibility is not implemented yet.

## 1. Start with a child theme

Create `themes/mytheme/` (or a separate theme root loaded using `GORUN_THEMES_EXTRA` for a site-specific theme).

```text
themes/mytheme/
  theme.json
  templates/
    index.html
  assets/
    style.css
  lang/
    en.json
    ar.json
  screenshot.png
```

Only `theme.json` and the files you actually override need to be added; inherited templates, parts and components resolve from the parent. A screenshot and localized labels make the theme easier to distribute and review.

## 2. Declare theme metadata

**`theme.json`** (starter example; the complete schema is documented in THEMES.md):

```json
{
  "name": "My Theme",
  "version": "1.0.0",
  "parent": "default",
  "supports": ["rtl"]
}
```

Set `supports` to include `identity` **only** after the theme's CSS uses GoRun's shared identity tokens correctly; support is not inherited automatically. For a complete working metadata example, inspect `themes/business/theme.json` in the repository.

## 3. Customize a page safely

To start, copy a single template from `themes/default/templates/` into your child theme and modify it. For example, `templates/index.html` overrides the fallback index template. Use the actual template name/definitions from the parent; do not assume a generic HTML file can be rendered independently.

GoRun resolves templates by hierarchy:
- Home: `front-page` → `home` → `index`
- Page: `page-{slug}` → `page-{template}` → `page` → `single` → `index`
- Post: `single-{type}-{slug}` → `single-{type}` → `single` → `index`
- Archive: `archive-{type}` → `archive` → `index`

Use `{{part "header" .}}` and `{{component "card" .}}` to reuse existing UI; see [THEMES.md](THEMES.md) for template context, functions and the full hierarchy.

## 4. Add CSS and assets

Put your CSS in `assets/style.css` and link it from an overridden head part (or the inherited extension hook when available) using:

```html
<link rel="stylesheet" href="{{asset "style.css"}}">
```

GoRun serves assets under `/themes/<slug>/assets/` and the `asset` helper produces a cache-busted URL.

Use modern CSS variables and zero-specificity selectors rather than fighting the parent with `!important`:

```css
:where(html[data-theme="mytheme"]) {
  --accent: #167252;
  --radius: 12px;
}
```

If you inherit the default theme head, check how its existing styles load before overriding anything. Avoid third-party JavaScript CDNs and inline event handlers; GoRun enforces CSP. Prefer local assets and scripts with a valid nonce.

## 5. Localize and support RTL

Add `lang/en.json` and `lang/ar.json` as flat string dictionaries:

```json
{"hero.title": "Welcome"}
```

In templates, use `{{t "hero.title"}}` rather than hard-coded text. Use logical CSS properties (`margin-inline-start`, `padding-inline`, `inset-inline-end`) and test Arabic `dir=rtl` alongside English LTR. Keep navigation keyboard accessible and honor `prefers-reduced-motion`.

## 6. Preview, validate, activate

```sh
gorun theme doctor mytheme
gorun theme doctor --json
```

These commands check the theme contract without needing a database. For ship-with catalog themes, additional `pkg/themekit` checks require language files, `screenshot.png`, the supported templates through inheritance and other catalog rules. A site-specific custom theme may have different distribution requirements.

Start GoRun in development mode, open **Appearance → Themes**, activate `mytheme`, and inspect the homepage, single post, page, archive, search, 404 and contact page. Activation changes rendering; it **does not create starter pages**. Use **Appearance → Build your site** if you want starter content.

For visual QA, check desktop/mobile, Arabic/English, dark/light where supported, contrast, focus rings, empty states, SEO metadata and loading without external assets. Run the project's existing Go and Playwright suites before submitting a bundled theme.

## 7. Distribute and maintain

- Package the theme directory (including `theme.json`, templates, assets and languages). This is a **filesystem theme**, not an HTTP extension plugin.
- For a site-specific theme, install it in a configured theme root using `GORUN_THEMES_EXTRA`; for general bundled themes, contribute to the repository and satisfy its catalog contract.
- Prefer parent inheritance and explicit version updates; avoid copying all parent files.
- Site-level draft/preview/publish overrides are stored under `<DataDir>/overrides/<theme>/` and may take precedence over your distributed files. Review overrides when diagnosing unexpected output.

**Further reading:** [Full themes reference](THEMES.md) · [Theme contract](THEME-CONTRACT.md) · [Theme catalog](DEFAULT-THEMES.md) · [Site builder](SITE_BUILDER.md).
