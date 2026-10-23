# Section-component pilot — validation record (2026-10-22)

Scope: reusable page sections for HTML page bodies (hero variants, feature cards,
steps, flow diagram, comparison table, FAQ, product preview, closing panel) and
section backgrounds driven by the identity tokens. Implemented in
`themes/default/assets/sections.css` (shared by every catalog theme),
`internal/theme/site_identity.go` (three new identity keys) and documented in
`docs/THEMES.md` → "Section library" and "Site identity".

Demo page: `internal/theme/testdata/sections-pilot.{en,ar}.html`, rendered by the
Go tests (`internal/theme/sections_pilot_test.go`) and imported by
`e2e/tests/sections-pilot.spec.ts` as `/en/sections-pilot-en` and
`/sections-pilot-ar`. Screenshots in this folder come from that Playwright run
(default and business themes; 1280 and 375 px; English LTR and Arabic RTL). Both
fixtures carry both hero variants (the second under an `h2`), so the split hero
with the fading dots and the centered hero on circles are rendered in both
directions.

## What the suites verified

| Check | Where | Result |
|---|---|---|
| Sanitizer keeps every section class and element the pilot uses; strips `<svg>`, `style`, `aria-*` | Go `TestSanitizerKeepsPilotSections` | pass |
| Stylesheet covers every class, embeds only `data:` SVG, no `@import`, ≤ 32 KiB raw / ≤ 8 KiB gzip | Go `TestSectionsStylesheetCoversPilotAndStaysStatic` | pass (31012 B raw, 6745 B gzip) |
| Identity pattern keys: tokens only, unknown values ignored, hex validated, design tab, reset | Go `TestIdentityPattern*` | pass |
| Pilot page renders 200 in default and business, en and ar | Go `TestSectionsPilotRendersInDefaultAndBusiness` | pass |
| No 500 marker, no horizontal scroll, no page errors, `dir` per locale | Playwright, 2 themes × 2 widths × 2 locales | pass |
| Section buttons text vs fill ≥ 4.5:1 | same | default 7.36:1, business 5.47:1 |
| Comparison table highlights column 2; FAQ opens/closes without script; steps have no list markers | same | pass |
| Hero background is a masked colour layer from the tokens | same | pass |
| Reduced motion: card transition ≈ 0 s, no hover transform | Playwright `reduced motion removes the card lift` | pass |
| `identity_pattern=waves`, `density=dense`, `color=#c2410c` reach `/site/identity.css` and the page; a section's own modifier still wins; a bad colour is rejected | Playwright `identity pattern settings reach the pages` | pass |
| `sections.css` absent on home, archive, search and 404 in default and business; present on both pilot pages | Playwright `sections.css is a used asset` | pass |
| Existing suites: `catalog-themes` (5 themes, starter About page sections) and `theme-acceptance` (§6, 5 themes × 1440/390) | Playwright `catalog` project | 33 passed + pilot spec 10 passed |

## Performance (same fixture before and after)

Limits stated before measuring: `sections.css` ≤ 32 KiB raw / ≤ 8 KiB gzip; no new
request on any page; render time within 1.5× of a home page render; pages whose content
has no `gp-s-` block must not load the stylesheet at all (used-assets-only).

| Measure | Before (main 00f123b) | After | Note |
|---|---|---|---|
| `sections.css` raw | 5 426 B | 31 012 B | one file, linked only on pages that use it |
| `sections.css` gzip | 1 671 B | 6 745 B | +5.0 KiB on pages with sections, cached afterwards |
| Requests on the demo page | 14 (default) / 12 (business) | 14 / 12 | no new request; icons and patterns are inline CSS |
| `sections.css` on pages without sections (home, archive, search, 404) | loaded on every page (1 671 B gzip) | not linked (`{{sections .}}` prints nothing) | one request and 1.7 KiB gzip less than main on those pages; `TestSectionsStylesheetLoadsOnlyWhenContentUsesIt` (5 themes) and the spec's used-asset test. A listing links it whenever a listed body uses a block (the default index prints full bodies), so the home-page numbers hold while no listed post uses the library. Commerce product pages opt in through `Extra["sections"]`. |
| CSS transferred, home page (encoded) | ≈ 18.7 KiB default / 16.8 KiB business | 17.0 KiB default / 15.1 KiB business, 13 requests | measured by the spec; before = after + the 1.7 KiB the old file cost |
| CSS transferred, demo page (encoded) | ≈ 19.3 KiB / 15.9 KiB | 23.9 KiB (default) / 20.4 KiB (business) | before = after − stylesheet delta; everything else unchanged |
| Document transferred (encoded) | — | 2.9–3.4 KiB | HTML body unchanged by the CSS |
| `domContentLoaded` (local, Chromium) | — | 14–27 ms | from `performance.getEntriesByType('navigation')` |
| Go render, pilot page (`BenchmarkSectionsPilotRender`) | identical fixture, CSS is linked not read | 5.6–6.4 ms/op, 6.3–6.8 MB/op | the sibling benchmark `BenchmarkIdentityPreviewHome` measured 5.4–6.8 ms/op for home pages on the same machine |

## Basecoat (evaluated in isolation; not added)

`basecoat-css@1.0.2/dist/basecoat.cdn.min.css` was fetched into a scratch
directory only and inspected with grep; nothing was added to the repository.

| Question | Finding |
|---|---|
| Tailwind version | Targets Tailwind **v4** (`@import "tailwindcss"`, `@layer theme/base/components/utilities`, `@property`, 58 `oklch()` and 233 `color-mix()` uses). GoRun pins the standalone CLI **v3.4.17**; the source route needs a Tailwind upgrade first. |
| Standalone file | 218 225 B raw, 22 105 B gzip. Bundles the Tailwind v4 preflight inside `@layer base` (resets `h1–h6`, `a`, `button`, `img`, `table`, `summary`, …), so loading it beside a catalog theme re-resets the theme's typography and chrome. |
| Tokens | shadcn-style `--background`, `--foreground`, `--primary`, `--border`, `--radius:.625rem`, all `oklch()`. None matches GoRun's `--bg`/`--ink`/`--accent`/`--radius` tokens; a bridge layer would be needed and dark mode uses `html.dark`, not `data-theme`/`color-scheme`. |
| Class names | Unprefixed (`btn`, `card`, `table`, `input`, `label`, `select`, `tabs`, `field`, `item`, `empty`). The content sanitizer keeps only `gp-*`, so none would survive in a page body; one existing admin template already uses `btn`. |
| JavaScript | Accordion (single-open), Combobox, Command, Drawer, Dropdown, Popover, Select, Sidebar, Slider, Tabs, Toast need its script; button, card, badge, table do not. |
| Licence / version | MIT, 1.0.2 (2026-07-06). |

Assessment: not clean for GoRun as it stands. It would either require Tailwind v4
(a toolchain change outside this pilot) or ship a second preflight and token
system next to the theme's. The pilot's own primitives (`gp-s-card`,
`gp-s-compare`, `gp-s-faq`, `gp-s-btn`) cover the components the brief asked for
with token-driven CSS. Recommendation: do not add Basecoat; revisit only if the
Tailwind build moves to v4.

## What remains before generalising

1. **Page templates for section pages.** `page.html` prints the page title as an
   `h1`, so a body starting with `gp-s-hero` has two `h1`s (the starter About page
   has the same today). A `page-sections.html` template per catalog theme, or a
   page option to hide the title, would fix it.
2. **Builder blocks.** The page builder (`internal/builder/render.go`,
   `builder.css`) still draws its own `gp-section` blocks. Mapping its kinds onto
   the library (`hero` → `gp-s-hero-split`, `features` → `gp-s-grid`,
   `faq` → `gp-s-faq`, `stats` → `gp-s-facts`, `pricing` → `gp-s-compare`) and
   exposing `identity_pattern` to builder sections is the next step; it needs
   the builder's own tests and screenshots.
3. **Magazine, portfolio and docs.** Their heads call `{{sections .}}` too and they pass the
   existing catalog checks with the starter About page, but the pilot page was
   screenshotted only in default and business. Add them to
   `sections-pilot.spec.ts` once their page widths are reviewed.
4. **Full-bleed sections.** `gp-s-bleed` uses `100vw` plus `overflow-x: clip`
   on `html`, the same technique as `gr-hero`. Container-query units would
   remove the dependency on the viewport width once every catalog page wraps
   the body in a container.
5. **Editor support.** The HTML editor has no snippets for these blocks; a
   "insert section" menu would make them discoverable (the sanitizer already
   accepts the markup).
6. **Hero scenes.** The Three.js hero scenes stay builder-only. A static
   `gp-s-media` slot takes an image or the drawn preview; a scene would need a
   script loaded only on pages that use it.
7. **Shadows in the dark scheme.** `--gp-s-shadow*` are black-based and all but
   vanish on a dark background; a dark-palette shadow token in the identity
   stylesheet would restore depth there.
