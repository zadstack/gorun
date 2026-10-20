# GoRun design direction

Product context: PRODUCT.md.

## Administrative workspace
Light neutral work surface, readable ink and restrained green selection/action states. Compact desktop density with 44px mobile controls. Familiar sidebar, search topbar, content area; detail disclosure reduces metadata noise. Self-hosted Zad typography, Arabic RTL with normal letter spacing, logical CSS dimensions and explicit keyboard focus.

## Public themes
Ship-with themes are **identity systems** (type, color, spacing, chrome), not opinionated site-pattern layouts. Stable slugs: `default` Classic, `business` Modern, `portfolio` Minimal, `magazine` Serif, `docs` Docs. Looks: editorial (Classic/Serif), clean-technical (Modern), warm-local (Minimal). Site brand and content own the voice; saved identities override defaults. Keep CSS scoped by data-theme. Full site patterns (magazine/business/portfolio packs) belong in plugins, not the theme catalog.

## Constraints
Selected homepage heroes may use a self-hosted Three.js enhancement, with three preset shapes, an off switch, reduced-motion support and a static fallback. Load it only on pages that use it. No fabricated content or performance claims. Draft creation, preview and activation remain separate. Screenshot previews must come from real rendered templates. Responsive and keyboard verification accompany changes.
