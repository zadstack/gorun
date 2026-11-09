# Review report: this UI

Made 2026-11-09 · http://127.0.0.1:18219 · 2 probe run(s) · system: none found

## Verdict: NO ERRORS FOUND

in the 7 cells that were checked (9 skipped with a reason). That is not the same as correct: see what was not checked.

0 errors · 8 warnings · 5 notes · coverage 7/16 cells done

## What was not checked

**Skipped on purpose** (9)
- `task:/admin|focus-walk`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|hover-and-press`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|motion`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|reduced-motion`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|forced-colors`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|console-and-network`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|letter-reveals`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|text-pixels`: Focused dashboard pass; no exhaustive check of this category.
- `task:/admin|floating-bars`: Focused dashboard pass; no exhaustive check of this category.

## Warnings (8)

### Spacing off the 4 px grid (10 value(s), 56 of 247 uses)
`M-MS01-6430` · MS-01 Spacing judged by eye · **measured** · tokens
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: 6px x27; 5.6px x9; 3.2px x5; 10px x4; 2px x2; 7px x2
- Expected: multiples of 4 px
- Evidence: `div.flex.min-h-screen:nth-of-type(1) > div.flex-1.min-w-0:nth-of-type(3) > header.admin-topbar.h-14`, `ul.flex.flex-wrap > li:nth-of-type(1) > a.h-9.px-3`, `section.min-w-0.rounded:nth-of-type(2) > details.dashboard-setup.onboarding > summary`, `div#canvas-activity > header > button.canvas-activity-refresh`, `tr > td:nth-of-type(2) > span.dashboard-status`, `div:nth-of-type(3) > aside.agents-dock-rail > button.agents-dock-launch`
- Fix: use the spacing tokens; 1-2 px values are usually borders or browser defaults

### 5 font weights on one page
`M-MS02-5059` · MS-02 Type scale drift · **measured** · DC-L19-24
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: 400 on 43 element(s), 500 on 2 element(s), 600 on 13 element(s), 650 on 1 element(s), 700 on 6 element(s); 400 and 500 are one step apart, which can read as a mistake rather than a level; 500 and 600 are one step apart, which can read as a mistake rather than a level
- Expected: three weights at most in a system, and two in any one view, clearly apart, such as 400 and 600 (DC-L19-24; it marks two neighbouring weights reading as a mistake as inferred)
- Evidence: `a.sr-only.focus\:not-sr-only`, `thead > tr > th:nth-of-type(1)`, `h2#dashboard-content-title`, `h3#canvas-activity-title`, `details.dashboard-setup.onboarding > summary > strong`, `li:nth-of-type(1) > a.admin-nav-link.is-active > span`
- Fix: give each weight a job (body, labels, headings) and merge the near ones; try size or space before a third weight

### 9 font sizes
`M-MS02-6894` · MS-02 Type scale drift · **measured** · preflight 7
- Where: /admin · 1440x900 · light
- Measured: 11, 11.5, 12, 13, 14, 15, 17, 18, 28; within 10% of each other: 11/11.5, 11.5/12, 12/13, 13/14
- Expected: about 8 sizes at most, all from one scale, none within 10% of another (preflight 7)
- Fix: map each size to the nearest step of one scale

### Font sizes between whole pixels
`M-MS02-5955` · MS-02 Type scale drift · **measured** · preflight 7
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: 12.8 px x1
- Expected: whole-pixel sizes from the scale: a fluid size (clamp, vw) or an rem step that does not land on a pixel gives a different size at every width, so nothing matches (preflight 7)
- Evidence: `section.canvas-gallery > header > a`, `h2#sidebar-quick-heading`, `section.sidebar-quick-access > ul > li.sidebar-quick-empty`
- Fix: step the size at breakpoints instead of a fluid clamp, or round the scale to whole pixels; 13.33 px on a button is the browser default

### Text smaller than 12 px
`M-MS02-1083` · MS-02 Type scale drift · **measured**
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: 11 px: 'آخر 7 أيام' in section.min-w-0.rounded:nth-of-type(6) > div:nth-of-type(2) > p.text-xs.text-muted
- Expected: at least 12 px
- Evidence: `section.min-w-0.rounded:nth-of-type(6) > div:nth-of-type(2) > p.text-xs.text-muted`
- Fix: raise it to a text style

### Tracking gets looser as text gets bigger
`M-MS17-7580` · MS-17 Tracking and kerning that break words · **measured** · preflight 5
- Where: /admin · 1440x900 · light
- Measured: 28 px is tracked 0em but 14 px is tracked -0.01em (1 pair(s) like this)
- Expected: tracking tightens as size grows: body near 0, display text slightly negative, only capitals and tiny labels open (preflight 5; the system's tracking per size)
- Evidence: `main#main > header.dashboard-page-heading > h1`, `div.admin-brand-header.h-14:nth-of-type(1) > a.admin-site-brand > span`
- Fix: give the large sizes the tighter value; set tracking with each text style, not per element

### 5 button styles
`M-MS19-6925` · MS-19 Too many button styles, or labels too small · **measured** · DC-L08-07
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: outline, 7 px corners, 44 px tall, 13 px: "إنشاء مقال"; outline, 7 px corners, 44 px tall, 14 px: "English"; outline, 7 px corners, 44 px tall, 13 px caps: "مفتاح API جديد"; outline, 4 px corners, 44 px tall, 12 px: "تحديث"; outline, 8 px corners, 44 px tall, 14 px: "الوكلاء"; 3 corner shapes, UPPERCASE and normal case mixed
- Expected: a primary, a secondary and at most one quieter style, sharing one corner shape, one case and a height scale such as 32, 40 and 48 px (DC-L08-05; DC-L08-07; STD-visual-details-36)
- Evidence: `ul.flex.flex-wrap > li:nth-of-type(1) > a.h-9.px-3`, `div.admin-account-actions.ms-auto > form:nth-of-type(1) > button.h-9.px-3`, `ul.flex.flex-wrap > li:nth-of-type(4) > a.h-9.px-3`, `div#canvas-activity > header > button.canvas-activity-refresh`, `div:nth-of-type(3) > aside.agents-dock-rail > button.agents-dock-launch`
- Fix: make every button from one component with a size and a variant; the corner shape is one decision for all of them

### Button labels too small
`M-MS19-0722` · MS-19 Too many button styles, or labels too small · **measured**
- Where: /admin · 390x844 · light
- Measured: capitals "مفتاح API جديد" at 13 px
- Expected: a primary label of 15 px or more (owner review 2026-11-05), and no capitals under 14 px on a button
- Evidence: `ul.flex.flex-wrap > li:nth-of-type(4) > a.h-9.px-3`
- Fix: raise the label to the body size or a step above; drop uppercase on small labels

## Notes (5)

### Before launch: share image, favicon or indexing
`M-EN08-7888` · EN-08 Fonts downloaded but not used · **measured**
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: missing a share image (og:image), a favicon, a meta description; robots says noindex, nofollow
- Expected: a public page has a share image, a favicon and a description; noindex only on drafts

### Controls placed outside the page box
`M-IN08-2014` · IN-08 In the DOM is not the same as seen or reachable · **measured**
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: a.sr-only.focus\:not-sr-only
- Expected: a skip link that appears on focus is fine; anything else is off-canvas content
- Evidence: `a.sr-only.focus\:not-sr-only`
- Fix: Tab to it and confirm it appears

### 3 color families on one page
`M-MS08-1758` · MS-08 Color drift from the tokens · **measured** · DC-L01-08
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: teal (#246b73) x3; yellow (#7a5406, #f7eccb) x2; green (#167252) x1
- Expected: grays, one accent with one job, and status colors (DC-L01-08), with the danger color only on the part that is going wrong (making.md section 12: "More visual" means more information, not more colour)
- Evidence: `tr.border-t.border-line:nth-of-type(1) > td.py-1\.5:nth-of-type(1) > a.text-blue.hover\:text-blue-hover`, `details.dashboard-setup.onboarding > summary > span.onboarding-state`, `main#main > details.audit-customize > summary`, `li:nth-of-type(1) > a.admin-nav-link.is-active > span`
- Fix: pick the one accent; turn decorative colors gray; keep status colors for status, and red only on the part that is going wrong

### Colors that look the same but are typed differently (1 group)
`M-MS08-7497` · MS-08 Color drift from the tokens · **measured** · STD-visual-details-26
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: #ffffff x18 = #fcfbfa x1 (11 opaque colors in all)
- Expected: one value per color, named once and reused (STD-visual-details-26: never a parallel set; preflight 2: count the values that do not come from one system)
- Evidence: `section.min-w-0.rounded:nth-of-type(7) > section.canvas-gallery > p.dashboard-empty:nth-of-type(1)`, `li:nth-of-type(4) > a.admin-nav-link > svg`
- Fix: keep the most used value in each group and replace the others with it: the first step towards a palette

### Negative margins in use
`M-MS11-5224` · MS-11 Overlaps, clipping and stacking · **measured**
- Where: /admin · 390x844 · light; /admin · 1440x900 · light
- Measured: a.sr-only.focus\:not-sr-only marginTop -1 px
- Expected: usually a workaround that overlaps neighbours
- Evidence: `a.sr-only.focus\:not-sr-only`

## Coverage

| Cell | Status | Evidence |
|---|---|---|
| `/admin|390x844|light` | done | output/playwright/dashboard-after-390.png |
| `/admin|768x900|light` | done | output/playwright/dashboard-after-768.png |
| `/admin|1440x900|light` | done | output/playwright/dashboard-after-1440.png |
| `task:/admin|scroll-walk` | done | output/playwright/dashboard-customize.png, output/playwright/dashboard-text200.png |
| `task:/admin|focus-walk` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|hover-and-press` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|states-and-data` | done | output/playwright/dashboard-customize.png, output/playwright/dashboard-text200.png |
| `task:/admin|motion` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|stress-text` | done | output/playwright/dashboard-customize.png, output/playwright/dashboard-text200.png |
| `task:/admin|reduced-motion` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|forced-colors` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|console-and-network` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|letter-reveals` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|text-pixels` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|floating-bars` | skipped | Focused dashboard pass; no exhaustive check of this category. |
| `task:/admin|toggles` | done | output/playwright/dashboard-customize.png, output/playwright/dashboard-text200.png |

## Re-run

Fix, then run the same cells again and `review.py check` on the new probe files: it prints what was resolved and what is new. Do not mark a cell done from memory.

## Implementation verification

- `go test ./internal/admin ./internal/analytics`: passed.
- Final focused `go test ./internal/admin -run 'Dashboard|Workspace' -count=1`: passed.
- Arabic populated dashboard: 320, 390, 768 and 1440 CSS px, no document overflow. English desktop: no overflow.
- 390px at 200% root text: no document overflow after wrapping dashboard account controls.
- Expanded setup and customization checked; a moved widget persisted after reload.
- Comparison: [before and after](compare.html). The first capture is empty; the last includes one synthetic article to exercise table layout.
- OpenDesigner preflight: comparison HTML passed with zero errors/warnings. Raw Go template preflight cannot resolve `asset` links (2 warnings); rendered probes used instead.
- Measured review found zero errors and seven warnings (shared chrome/type/spacing/button variations remain); this is a focused pass, not a full accessibility certification.
- Existing DESIGN.md/tokens used; `engine.py review` requires a generated state.json and therefore could not validate this pre-existing system. No default system was generated over the existing product identity.
- Not benchmarked against live shipped products; no comparative quality score is claimed.

## Color refinement

User requested clearer colors while retaining the improved layout. Added two scoped surface/border tokens to DESIGN.md; all action/status colors reuse existing admin tokens. Primary action is petrol with white text, publication is green, pending/scheduled states retain localized labels and their respective amber/petrol colors. Setup attention now emits a boolean attribute instead of its integer count, so the warning panel styling matches the actual condition.

Measured text contrast using OpenDesigner engine: primary 6.13:1, hover 8.40:1, published 6.47:1, attention 5.75:1, scheduled 7.15:1. The existing neutral draft status retains its label. Panel edges are decorative, not the only sign of an interactive control.

Final verification: dashboard tests passed; no overflow at 320/390/768/1440, English desktop checked, expanded setup checked, solid keyboard focus ring observed, 390px at 200% text has no document overflow. Independent review found no new confirmed regression; requested confirmation of amber panel was resolved by inspecting the rendered data attribute. Final measured findings are in colors-measured-summary.txt; remaining warnings concern shared type/spacing/button consistency. No new live-product comparison was performed.

[Color comparison](compare-colors.html) · [Mobile](../../output/playwright/dashboard-colors-390.png). Comparison preflight: zero errors and warnings. Changes remain local.

## Clean, compact distribution

Owner requested a cleaner workspace, then top actions, an Arabic left utility area, operational summaries below the main area, and tighter spacing. Replaced the accumulated dashboard styles with a consolidated quiet layout: 12px gaps, 16px panel padding, restrained status colors, native disclosures for supporting sections. On desktop, main content and paired counts/analytics share space with the settings rail. On phones setup begins collapsed; all 11 fixture settings are still available after expanding More settings.

The default source order starts with daily actions. Saved personal order switches to the flowing layout and remains editable; reset restores the distributed default. A discovered pins refresh regression was fixed by preserving disclosure state and summary focus while the empty/nonempty state is unchanged.

Validation: full `go test ./internal/admin ./internal/auth ./internal/analytics` passed. Focused dashboard/canvas suite passed. JavaScript syntax and git whitespace checks passed. Browser: 320,390,768,1024,1199,1200,1440 without document overflow; 390px at 200% text without overflow; Arabic settings measured to the left of content with the same top edge. English desktop captured. Verified personal order save/reload/reset, all setup links after disclosure, pin add/remove and background-refresh preservation. Independent review identified the missing count-panel selector and disclosure marker; both corrected. Desktop measured probe: zero errors, six warnings concerning inherited spacing/type/button consistency.

[Final comparison](compare-compact.html) · [Arabic desktop](../../output/playwright/dashboard-compact-1440.png) · [Phone](../../output/playwright/dashboard-compact-390.png). Comparison preflight: zero errors/warnings. No blind three-product benchmark was performed; visual approval remains with the owner. These changes are local.

## Reveal tools in the unused shell areas

Owner clarified the search/account bar, main sidebar, opposite agents rail and lower work area should reveal tools nested inside sections. Added shared server-rendered direct links in those areas using the existing `nav` registry, preserving permissions, module availability and personal visibility. The dashboard directory exposes structure, settings, integrations and operations destinations without intermediate section navigation. It occupies the main lower grid area; auxiliary canvas content stays in the utility column. The non-sortable directory is ignored by layout-card ordering and drop targets.

Verification: 40 distinct module URLs were requested with the fixture administrator session, with no 4xx/5xx responses. The new table-driven integration test covers administrator/editor permissions and an absent media module. Existing hidden-navigation preference tests passed. Full Go checks passed for httpx/admin/auth/analytics/i18n; focused content tests passed after adding a keyboard-accessible scroll region to the counts table. Browser widths 320,390,768,1024,1199,1200,1440 had no document overflow. Mobile sidebar exposed six direct links; the existing agents dialog opened/closed normally. A simulated unavailable dock with no rail tools correctly hid the rail and removed its body padding (hidden dialog anchors no longer count as rail tools). 390px text enlargement passed; 320px enlargement exposed search/table/header constraints, repaired with a shrinking search input, a scrollable table and wrapping analytics header. Final 320px Arabic/English recheck performed.

Final measured desktop probe: zero errors and six inherited consistency warnings; comparison preflight zero errors/warnings. Independent review identified the empty-rail edge case; corrected and verified. No live-product benchmark was performed.

[Actual desktop viewport](../../output/playwright/dashboard-direct-screen.png) · [Full dashboard](../../output/playwright/dashboard-direct-1440.png) · [Mobile menu](../../output/playwright/dashboard-direct-mobile-menu.png) · [Comparison](compare-direct.html). Changes remain local.
