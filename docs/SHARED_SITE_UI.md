# Shared public account and onboarding UI

GoRun's core-owned customer pages can reuse the published site identity without executing a custom theme's CSS inside authentication forms. This change affects the embedded launch journey (`/start`, `/start/login`, `/start/verify`) and the standalone Membership and SaaS pages. The administrative sign-in renderer remains separate.

## Identity contract

`internal/theme.StandaloneIdentityFor(ctx, deps)` returns validated generated CSS and font stylesheet URLs. It reads the active theme's published identity where supported, falls back to a valid saved builder identity, and finally uses `brand.Default()`. It works when the theme module or dependencies are absent. Explicit published tokens remain usable when arbitrary custom CSS makes the normal appearance report inconclusive, because standalone pages never import that custom CSS.

The fallback identity uses a light neutral surface, dark slate text and restrained blue actions. Explicit saved site identities are preserved. The subset includes accent/background/text, safe font families and width. Accent labels and links are derived separately for contrast; an unreadable text/background pair falls back together. Only allowlisted font families produce font URLs. The default Zad font is bundled locally; a selected supported web font uses the existing font hosts permitted by CSP. Custom CSS, identity drafts and arbitrary CSS values are excluded. Standalone module templates use the response nonce for generated style tags.

Tokens consumed by standalone presentation include `--standalone-bg`, `--standalone-panel`, `--standalone-ink`, `--standalone-muted`, `--standalone-accent`, `--standalone-link`, `--standalone-on-accent`, `--standalone-line` and `--standalone-tint`.

Launch additionally resolves the published `site.logo` media entry, with a generic mark when unavailable. It does not add theme-manifest or dark-logo selection. Existing paths, POST contracts, CSRF, customer/staff separation and module activation gates are unchanged.

## Inherited customer account

The default account template presents password sign-in first and uses native `details` for email-link sign-in and optional registration. Relevant feedback reopens the corresponding disclosure. Signed-in services, orders and available invoices have separate headings and navigation; empty sections link to the store. No JavaScript is required for those disclosures.

The account layout is scoped under `.gp-account`. The guest surface is narrower than the signed-in account. Fields, secondary text and feedback surfaces use a coherent identity background/text pair, including saved dark identities. Commerce actions, return destinations, honeypot and permission gates remain in their original forms.

Theme overrides remain authoritative: a theme that supplies its own `account.html` does not automatically receive the new default layout. No deployment or rewrite of customer themes is performed by this change.

Membership explains missing submission policies instead of showing an empty type selector. SaaS displays empty subscription/usage states outside horizontally scrolling tables; populated tables keep their existing controls.

## Validation

See [the isolated fixture validation record](validation/shared-site-ui/REPORT.md). Native builder layout variants are documented in [Site Builder](SITE_BUILDER.md). No customer data, credentials, subscription changes or production rollout are part of the fixture.
