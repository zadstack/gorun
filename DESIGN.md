# GoRun design direction

Product context: PRODUCT.md.

## Administrative workspace
Light neutral work surface, readable ink and restrained green selection/action states. Compact desktop density with 44px mobile controls. Familiar sidebar, search topbar, content area; detail disclosure reduces metadata noise. Self-hosted Zad typography, Arabic RTL with normal letter spacing, logical CSS dimensions and explicit keyboard focus.

## Public themes
Default is a quiet reading layout; company is a light service studio; portfolio emphasizes project imagery; editorial uses warm paper and typographic hierarchy. Saved site identities override defaults. Keep CSS scoped by data-theme and preserve inherited-template behavior.

## Constraints
Selected homepage heroes may use a self-hosted Three.js enhancement, with three preset shapes, an off switch, reduced-motion support and a static fallback. Load it only on pages that use it. No fabricated content or performance claims. Draft creation, preview and activation remain separate. Screenshot previews must come from real rendered templates. Responsive and keyboard verification accompany changes.
