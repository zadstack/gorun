# Site quality in the core: requirements and status

> **ملخص بالعربية.** جودة المواقع ما زالت تعتمد على تعديلات خاصة بكل قالب. هذا السجل يحوّل
> قائمة الإضافات المطلوبة للنواة إلى حالة فعلية مأخوذة من الكود، بالترتيب نفسه: ما الموجود، وما
> الناقص، وما التصميم المقترح. أول بند (العقد الموحّد للقوالب) بدأ بورقة الأنماط الأساسية.

The goal is that the core prevents the problems we fixed by hand on existing sites, so new
sites do not repeat them. This record follows the requested order. Each item says what the code
does today, with file references, what is missing, and the smallest design that closes the gap.
It is based on reading the code at v0.17.0, not on a full audit of every site function.

Items that change a live site (stale guide content, the two cached images on GoCloud, visitor
journeys on a real domain) are operator work and are listed at the end; nothing here changes a
real site.

## Status

| # | Item | Status |
|---|---|---|
| 1 | Unified theme contract | **Started**: core site stylesheet (below) |
| 2 | Unified identity application | **Started**: readable-layer rules (below) |
| 3 | Site readiness check before publishing | Partial (two checkers, no rendered-page check) |
| 4 | Automatic asset versioning | Mostly in place; two gaps |
| 5 | Whole-site preview | Partial (one page or one template at a time) |
| 6 | Reviewable theme updates | Open |
| 7 | Module readiness report | Partial (commerce only) |
| 8 | Compatibility data in the release | Migrations: done in main, first published by the next release; template hashes open |
| 9 | Page compositions (purpose presets, single and archive anatomy, localized destinations) | **Done for the core**: template contract ([THEME-CONTRACT.md](THEME-CONTRACT.md#page-composition-helpers)) and builder purpose presets ([SITE_BUILDER.md](SITE_BUILDER.md#english--purpose-pages)) |

## 1. Unified theme contract

**Done.** `/static/site/core.css` is served from the binary and linked by `{{sections .}}` in
every theme ([THEME-CONTRACT.md](THEME-CONTRACT.md#core-site-stylesheet)). It gives
zero-specificity defaults for forms, tables, code, quotes and long content, and the utilities of
the shared store, cart, checkout, account, product and intake templates, scoped under `.gp-core`.
Before it, those templates came from `default` under every theme but relied on stylesheets only
the default head linked, so they were unstyled under business, portfolio and magazine. The theme
contract now requires a head that skips `{{sections .}}` to link the core stylesheet itself.

**Still open.**
- No shared component library: each theme rebuilds header, footer, contact form, card, list and
  pagination (`themes/*/components`, `themes/*/parts`).
- `/membership` and `/saas` render inline templates with their own stylesheet
  (`internal/membership/module.go:179`, `internal/saas/module.go:154`,
  `internal/httpx/account_page.go:12`, fixed colours) instead of the theme. The membership page is
  hard-coded Arabic.
- `/start` (launch) has its own templates and stylesheet (`internal/launch/module.go:20`).

**Next.** Render membership and saas pages through the theme (as commerce does), or at least
replace their fixed colours with the theme tokens and the core stylesheet.

## 2. Unified identity application

Three layers write the same CSS variables: theme tokens at zero specificity
(`:where(html[data-theme])`), builder identity at `:root` (`internal/brand/brand.go:76`) and site
identity at `:root:root[data-theme]` (`internal/theme/site_identity.go:264`). Confirmed conflicts:

1. The dark palette resets only some tokens (`site_identity.go:252`); an accent stays as the link
   colour on the dark surface, and hard-coded light colours in themes stay light.
2. Accent contrast is checked only against white button text (`brand.go:47`), never as text on
   the page background.
3. The Apply contrast check falls back to the theme's declared colours
   (`identity_admin.go:150`), so dark scheme plus dark text passes, then renders dark on dark.
4. Builder identity overrides surface, muted text and radius on every theme, including themes
   that do not declare `identity`.
5. `brandImg` swaps to the dark logo on the visitor's OS dark mode (`funcs.go:752`) while the
   page itself stays light.
6. The effective-appearance report skips the builder layer (`appearance.go:207`).

**Done.** Conflicts 1, 3 and 5 are closed, and conflict 2 is closed for text in the accent colour
([THEMES.md](THEMES.md#rules-that-keep-the-layers-readable)): the dark scheme redefines every
surface and text token; accent text is computed to reach 4.5:1 on the page background; the save
check and the contrast shown use the background that renders; the logo follows the page.

**Still open.** Conflict 4 (builder identity still sets surfaces, muted text and a square radius on
every theme) and conflict 6 (the appearance report skips the builder layer). Narrowing the builder
layer changes how existing builder sites look, so it needs a reviewed migration.

**Original design.** Builder identity sets brand tokens only (accent, font, width, and background or text
only when set) under `:where(:root)`; one scheme resolver defines light and dark values for every
semantic token; Apply checks contrast against the effective background; `brandImg` follows the
resolved scheme; the appearance report includes the builder layer.

## 3. Site readiness check before publishing

**Today.** Builder readiness (`internal/builder/readiness.go:53`, admin page only) checks the
front page, identity, logo, placeholder text, image alt and files, and primary menu targets from
stored HTML. The SEO audit (`internal/seo/audit.go:28`) finds duplicate stored titles and
descriptions per locale. Neither renders a page. Translations are `Locale` plus
`TranslationGroup` on items (`internal/content/contract.go:71`).

Mail readiness uses the selected native SMTP/Resend configuration and the shared native
recipient parser (also used by form settings) to validate explicit
`forms.recipients` before falling back to `contact.email`. Reports never expose recipient
addresses or provider secrets, decrypt credentials, or send a message. A configuration pass
means local setting presence only. Provider acceptance and actual inbox receipt remain
separate, unknown checks until a controlled send and recipient confirmation. Legacy check
IDs `smtp_config` and `smtp_delivery` are retained for existing consumers. Notification
enablement (`forms.notify`) and the administrator fallback are not verified by these checks.

**Design.** A background observation (`observe.Spec`, as `internal/admin/updates.go:75` uses)
renders each published URL in each locale through the public router in-process, then reports one
row per page and reason: non-200, missing or duplicate rendered `<title>`, `h1` count, broken
internal links and images, missing viewport, missing translation (an item whose group lacks a
published member for a supported locale), and overflow risks found statically (fixed widths,
wide tables, long unbroken strings), labelled as heuristics because no browser runs. Results
show on the existing readiness page, through `GET /api/v1/site/readiness`, and as an MCP tool.

## 4. Automatic asset versioning

**Today.** Media files never change under a URL: every upload and every logo change gets a new
storage key (`internal/media/store.go:147`), served as immutable (`handlers_serve.go:38`). Theme
and pattern assets carry `?v=<mtime>` (`internal/theme/funcs.go:299`); identity stylesheets and
the core stylesheet carry content hashes.

**Gaps.** `themeLogo` and the theme-icon fallback in `favicon` return unversioned URLs
(`funcs.go:735`, one-hour cache). The mtime version can repeat or go backwards after an image
rebuild. With S3 `PublicURL`, caching is set by the bucket or CDN, not by GoRun.

**Design.** Hash file content for `asset` (cached per path, size and mtime), and route
`themeLogo` through `asset`. No body rewrite is needed for `/media/` URLs.

## 5. Whole-site preview

**Today.** Builder preview (one page), template drafts (`?gp_preview_override=1`, any URL), the
identity draft preview (home and newest post, Arabic or English, desktop or mobile width). No
preview of an inactive theme or pattern.

**Design.** One request-scoped preview flag that loads the identity draft, template drafts and an
optional draft theme or pattern for that request only, kept `no-store` and admin-only, plus a page
that frames a URL list (home, one item per type, search, contact, store and account pages) with
the existing language and width toggles.

## 6. Reviewable theme updates

**Today.** Overrides are files under `<data>/overrides/<slug>/` (`internal/theme/overrides.go:15`)
and record no base: no hash, no theme version, no copy of the original. `theme.json` has a
version nothing compares. Themes update with the GoRun release.

**Design.** When saving an override, keep the original file and its theme version and sha256
beside it. The template editor then shows base → override and base → current theme. A conflict is
a current file whose hash differs from the recorded base; show it in the editor and System Health.
Older overrides show "base unknown". Detecting conflicts before an update needs per-template
hashes in the release index (item 8).

## 7. Module readiness report

**Today.** Modules publish requirements through the `system.capabilities.modules` filter
(`internal/admin/capabilities.go:13`); only commerce does (`internal/commerce/health.go:206`).
`GET /site/capabilities` reports the others as publishing nothing, while System → Modules labels
the same modules `not_required` (`internal/admin/modules.go:302`), which contradicts it.

| Module | Missing prerequisite | Fix step |
|---|---|---|
| saas | Commerce with Stripe for subscription sync | Enable commerce and finish its Stripe setup |
| membership | Commerce for `/membership` sign-in | Enable commerce |
| launch | An https base URL for the `Secure` session cookie | Set the site's https base URL |
| workagents, identityai | An AI text connection | Add an AI connection under Connections |
| identitylogo | An AI connection plus an `images-png-v1` logo binding | Configure the logo binding |
| mcp | A scoped API key; an https base URL for remote clients | Create a key; set the base URL |
| ext | A base URL for callbacks; no extension in error | Set the base URL; fix the extension |
| wpmigrate | Nothing until a migration runs | Publish "configured" |

**Design.** One requirements function per module, modelled on commerce, with two shared helpers
(AI connection ready, https base URL), and the `not_required` label replaced by the real state.

## 8. Compatibility data in the release

Starting with the next release, each release publishes `release-index.json` with every migration
it ships, so the update preflight checks migrations instead of reporting `unknown`
([UPGRADE.md](UPGRADE.md#7-staged-updates-in-app)). Theme slugs are left out on purpose: the
preflight would refuse a site that runs its own theme. Per-template hashes, which item 6 needs,
are not published yet.

## Operator work on existing sites

These need access to the real sites and are not done from this repository:

- **Stale content**: review guides, versions and images; the GoRun guide on the website still
  names an old version.
- **Functional pages**: confirm the store, account and service-start pages after updating to a
  release with the core stylesheet; launch and membership pages need item 1's next step.
- **Two languages**: review translated content and the matching links between languages, not
  only `lang` and RTL.
- **Visitor journey**: from the home page to a service, then contact or order, on a safe test
  copy of the site.
- **Images and cache**: purge the two stale images from GoCloud's cache, then check whether they
  come from an unversioned theme logo or icon (item 4 gap) or from a CDN rule.
