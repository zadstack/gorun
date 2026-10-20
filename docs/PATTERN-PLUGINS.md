# Pattern plugins (site layouts)

> **بالعربي.** الأنماط (Patterns) حزم تخطيط للموقع — مجلة، شركة، معرض، دليل — تُطبَّق فوق
> **هوية القالب** (خط وألوان ومسافات) دون استبدالها. الإضافات HTTP (Extensions) شيء آخر:
> برامج خارجية على خطافات. اختر النمط من **المظهر ← الأنماط**، والهوية من **المظهر ← القوالب**.

GoRun splits three concerns:

| Concern | What it is | Where |
|---|---|---|
| **Theme identity** | Type, color, spacing, chrome | `themes/`, Appearance → Themes |
| **Pattern plugin** | Opinionated page layouts | `patterns/`, Appearance → Patterns |
| **HTTP extension** | External hook subscribers | `internal/ext`, System → Extensions |

Pattern plugins are **declarative filesystem packages**, not Go plugins and not the HTTP bridge.

## Package layout

```
patterns/<slug>/
  pattern.json           required (kind=pattern)
  templates/*.html       same naming as themes (front-page.html / home.html required)
  parts/*.html           optional
  components/*.html      optional
  assets/**              served at /patterns/<slug>/assets/
  lang/{en,ar}.json      required pattern copy
  screenshot.png         optional admin preview
```

`pattern.json`:

```json
{
  "kind": "pattern",
  "slug": "editorial",
  "version": "1.0.0",
  "name": {"en": "Editorial", "ar": "تحريري"},
  "description": {"en": "…", "ar": "…"}
}
```

Unknown fields are rejected. Packages must not ship `:where(html[data-theme=…])` identity token selectors.

## Runtime layer

Template resolution (highest first):

1. Draft overrides (preview)
2. Published overrides
3. **Active pattern** (`site.pattern`)
4. Active theme
5. Parent themes

Activate a pattern under **Appearance → Patterns** (permission `themes.manage`). Clear returns to the theme’s own templates. Themes and patterns are independent: any identity theme can pair with any pattern.

## First-party starters

| Slug | Layout |
|---|---|
| `editorial` | Magazine masthead + lead + story grid |
| `company` | Business hero + offerings + updates |
| `showcase` | Portfolio intro + project mosaic |
| `guidebook` | Docs search hero + section cards |

Ship-with root: `./patterns` (override with `GORUN_PATTERNS`). Operators may also place packages under `<DataDir>/patterns/<slug>/`. Extra roots: `GORUN_PATTERNS_EXTRA` (PATH-separated).

## Validation

`pkg/patternkit.ValidateDir` checks the manifest, home template, locales, and forbids remote script CDNs / identity token selectors.

## Follow-ups

- Builder starters should pick **pattern + identity theme** instead of layout-heavy catalog themes.
- ZIP install UI for operator patterns (directory drop works today).
- Optional pattern settings (non-identity knobs only).

See also: [THEMES.md](THEMES.md), [EXTENSIONS.md](EXTENSIONS.md), [THEME-CONTRACT.md](THEME-CONTRACT.md).
