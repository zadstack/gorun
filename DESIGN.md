# GoRun design direction

Product context: PRODUCT.md.

## Administrative workspace
Warm neutral work surface (#faf9f8), charcoal ink (#2b2b2b) and restrained petrol (#246b73) action/selection states. Green remains the success/status color. Distinct workspace category colors identify navigation areas. Flat white sidebar and topbar keep the working area calm. Compact desktop density with 44px mobile controls. Familiar sidebar, search topbar, content area; detail disclosure reduces metadata noise. Self-hosted Zad typography, Arabic RTL with normal letter spacing, logical CSS dimensions and explicit keyboard focus.

## Public themes
Ship-with themes are **identity systems** (type, color, spacing, chrome), not opinionated site-pattern layouts. Stable slugs: `default` Classic, `business` Modern, `portfolio` Minimal, `magazine` Serif, `docs` Docs. Looks: editorial (Classic/Serif), clean-technical (Modern), warm-local (Minimal). Classic pairs cream paper (#fffef2) with charcoal actions (#333333). Serif uses neutral white paper and charcoal text, with deep blue (#001489) for actions and links so images carry the color. References: [Kobbe](https://ui.gradientdeck.com/styles/kobbe), [Aesop](https://ui.gradientdeck.com/styles/aesop), [MUBI Notebook](https://ui.gradientdeck.com/styles/mubi-notebook). Site brand and content own the voice; saved identities override defaults. Keep CSS scoped by data-theme. Full site patterns (magazine/business/portfolio packs) belong in plugins, not the theme catalog.

## Constraints
Selected homepage heroes may use a self-hosted Three.js enhancement, with three preset shapes, an off switch, reduced-motion support and a static fallback. Load it only on pages that use it. No fabricated content or performance claims. Draft creation, preview and activation remain separate. Screenshot previews must come from real rendered templates. Responsive and keyboard verification accompany changes.

## Shared customer surfaces

Shared customer surfaces default to a light neutral background, dark slate text and a restrained blue action color. Explicit saved site identities continue to take precedence.

Core-owned launch, Membership and SaaS surfaces use the safe published identity subset described in [Shared site UI](docs/SHARED_SITE_UI.md). Their presentation consumes standalone semantic tokens and retains module/session boundaries. Default commerce account styling is scoped to its own component, supports dark identity pairs, and uses native progressive disclosure for alternative sign-in methods. Theme overrides retain precedence.
