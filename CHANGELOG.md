# Changelog

All notable changes to GoRun are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow semantic versioning.

## [Unreleased]

## [0.7.0] - 2026-10-19

### Agent skills and connection bundles

- Manage a private bundle for an owned, active agent key: bounded inline skill
  instructions and reference text, plus multiple version-pinned MCP connection
  profiles. Review the combined effective instructions in the canvas before
  execution. Skills become part of real managed-text and external-agent requests.
- Recheck the current bundle version and authority at execution/result boundaries.
  Bundle changes retire previous incoming-event and schedule consent; restoring
  old text does not restore previous consent. Keep ownership, permission, size and
  configuration-version checks active when a saved profile changes or disappears.
- Show the saved discovery result and timestamp separately from live availability.
  Export a manifest without stored credentials or endpoint URLs for an external
  client to configure explicitly. Attaching MCP profiles does not grant every tool,
  install server packages or create an automatic managed-model tool loop.

### Current interface screenshots

- Refresh the English/Arabic README campaign story and admin screenshots for
  grouped navigation, contextual page links and the opposite-side agent drawer.
  Include real desktop and mobile captures using isolated synthetic data, with
  configured status distinguished from an online external client.

### Separate Edge read-only proof of concept

- Add a locally runnable Workers implementation with its own D1 schema and
  explicitly public R2 media. Scope reads to one site, expose published content
  only, escape text output and reject write/admin routes. Keep the prototype
  separate from the GoRun Full database and release binary.
- Test the Worker through the actual local workerd runtime with D1/R2 bindings.
  Remote Cloudflare deployment remains unverified; this is not a complete Edge
  CMS, manager pairing, a deploy template/button or an Edge benchmark result.

Existing sites require an operator deployment to receive application changes.
Publishing this release does not upgrade a running installation.


## [0.6.0] - 2026-10-19

### Admin navigation

- Group the admin sidebar into Home, Content, Website, Canvas & agents, Inbox
  and Settings, with Store shown only when permitted commerce tools are available.
  Put related pages in contextual category/page tabs; nested editors keep their
  parent section selected. Preserve existing URLs, permissions, personal visibility,
  dashboard pins and saved navigation order. Unknown extension groups remain
  discoverable under Settings.
- Keep the compact navigation usable in Arabic/RTL and English/LTR, on small
  screens and without browser storage. Contextual page links also work without
  JavaScript; dashboard pin buttons are available beside the page tabs.

### Agents rail and details

- Add an opposite-side rail for agent/model cards on the current user's own
  canvases. Open a compact drawer with Integrations, Instructions and Defaults
  tabs showing the real saved model/mode, effective instructions, card identity
  and output type. Links open the original canvas card for configuration/review.
- Keep card, key and model identities distinct. Exclude source context and
  credentials, reject foreign/revoked/expired agent keys and recheck current
  preparation/grants. Opening the panel never dispatches work. Configured does
  not mean online: external agents still need their clients, and Skills/MCP
  resource bundles are not assembled or verified by the drawer.
- Hide the entire agents surface in Simple CMS mode. Support RTL/mobile,
  keyboard tab navigation, modal focus containment/return and safe sidebar
  handoff. Discard late responses after closing/leaving, and preserve logical
  focus when refreshed cards change or disappear.

### Validation and upgrades

- Add bilingual browser and permission/ownership regressions, including dashboard
  pin/unpin, runtime content types, small-screen lazy previews, unavailable
  browser storage and delayed drawer responses. The combined interface passed
  27 focused Chromium checks and race tests before merge.
- Existing installations need a manual deployment to receive these UI changes;
  publishing this release does not upgrade a running site.

## [0.5.0] - 2026-10-19

### Installation and server operations

- Add guided Linux server setup using verified public release archives, a local
  non-root runtime image, persistent data and Caddy HTTPS. Validate domain/DNS,
  ports, proxy trust, certificates and application health before reporting success.
- Resolve the latest public stable release for new server installations and record
  its numeric version and immutable image identities. An explicit `--version`
  pins a release; `--resume` keeps the original manifest release without resolving
  latest, downloading, pulling or rebuilding. API errors never silently select an
  older release. Add a manually dispatchable isolated Linux acceptance workflow.
- Add an optional admin update page for a server-configured Coolify resource,
  with recent authentication, a verified backup, audited deployment requests
  and duplicate-request cooldown. Existing sites need a manual deployment before
  this page is available; no running customer site is upgraded automatically.
- Preserve sidebar scroll position across navigation/reloads with a safe fallback
  when browser storage is unavailable.

### Canvases and agent workflows

- Add workflow, grid and list views, collision-aware card arrangement, clearer
  compact card tools and separate connection ports. View-only changes preserve
  the saved graph; refresh the README with a three-step synthetic workflow story.
- Add one compact dashboard activity board combining the current owner's agent
  results with permission-scoped webhook delivery metadata. Exclude webhook URLs,
  credentials, payloads and raw errors; refresh does not run work again.
- Convert a reviewed text result into a new draft using current source, owner and
  permission checks. Publishing remains a separate action. Save private structural
  workflow templates without copying prompts, notes, outputs, credentials or runs.
- Show configured agent identity/model/mode alongside permission context. An
  external agent still requires its client to connect and execute; configuration
  is distinct from verified readiness.
- Add opt-in signed commands for individual agent/provider cards. Owner review,
  recent authentication and CSRF protect configuration; encrypted signing secrets
  appear once. The receiver accepts only the configured command, verifies HMAC and
  freshness, deduplicates deliveries and enqueues atomically within bounded rates.
  Grant/key/role/provider or relevant graph changes require fresh consent; disabled
  or stale rules cannot append results after an in-flight call. Accepted events
  are audited without prompts, payloads or secrets. External clients remain in
  `waiting_agent`; no automatic publishing or arbitrary shell/tools are added.

### Bundled themes

- Give the five bundled themes descriptive category names and distinct visual
  treatments. Replace standalone demo brands in the theme gallery and README
  previews with generic sample identities. Keep existing theme IDs, saved site
  titles, logos and identity settings compatible; custom palettes take precedence.

### Evidence and boundaries

- Publish reproducible GoRun/WordPress/MariaDB article-serving measurements with
  raw evidence, a validated summary and an executed notebook. Astro static output
  is a separate delivery reference. The measured GoRun source remains the recorded
  0.4.0-era checkout, not a benchmark of the new canvas UI.
- Keep Cloudflare Edge labelled in development: permission contracts are not a
  runnable Edge CMS or a deployable template. Combined Skills/MCP resource bundles
  remain a future design and are not presented as automatic agent provisioning.

## [0.4.0] - 2026-10-19

### Installation and documentation

- Add a copy-and-paste macOS/Linux installer with Intel/ARM detection, HTTPS
  release downloads, SHA-256 verification, atomic user-local installation and
  direct startup without Go, sudo or manual PATH edits. Preserve existing
  installations on failed verification or when an old release lacks `start`.
- Put the English quick start first and preserve a complete linked Arabic README.
  Include hands-on plugin and theme development tutorials.

### Workspace and foundations

- Add Simple CMS and Agents + Commerce workspace modes to tailor the admin UI.
- Add portable, site-scoped Edge manager capability contracts and regression tests;
  this is an authorization foundation, not a hosted Edge runtime.
- Clarify API key and webhook command help and scope errors.

### Automatic backup and safe recovery

- Add daily/weekly local scheduling choices, atomic policy saves, recorded verified
  success and storage usage, persistent failure/retention alerts and System Health checks.
- Fix relative/URI-character SQLite paths in read-only backup inspection.
- Verify local admin/CLI backups before success or pruning. Retention counts only
  freshly inspected pairs; corrupt/legacy/partial copies cannot displace good copies.
- Add `gorun backup-preview` with a disposable sanitized restore, fresh secret,
  fixed public-content module allowlist, no workers/extensions/integrations,
  loopback/Host checks and read-only browser isolation. Source backups and live data
  remain unchanged; final restore/cutover still uses a separate new directory.


### First-login setup and diagnostics

- Require the auto-generated administrator to replace the temporary password and
  choose email, site name and site/dashboard language before admin or authenticated
  API/MCP access. Save all fields atomically, rotate the browser session and revoke
  old sessions, keys and magic links; completed/existing configured accounts skip it.
- Add read-only `gorun doctor [--json]` with configuration, permissions, SQLite,
  migration drift, module activation, admin, theme and local port checks plus recovery
  advice; do not initialize a site, generate secrets or apply migrations.

### Operations

- Local load harness (`tools/loadtest`, `make loadtest`): boots a real SQLite
  `gorun serve`, seeds published posts, and reports p50/p95/p99 latency, error
  rate, throughput, and approximate server RSS/CPU under N concurrent readers
  (optional light write mix). Docs: [docs/LOADTEST.md](docs/LOADTEST.md). Not
  wired into CI (avoids flaky host-noisy gates).

### Local startup

- Add `gorun start` with SQLite migrations, bundled default-theme provisioning,
  atomic first-admin creation and a 256-bit password printed once. Print local
  and private IPv4 LAN preview URLs and open the local admin in a browser.
- Keep administration, accounts, API and writes on localhost; validate Host and
  peer addresses, ignore forwarded identity and discard LAN credentials.
  `--local-only` disables LAN; `--no-browser` supports headless use.
- Advertise/open the IPv4 loopback address to match the listener and avoid
  localhost resolving to an unrelated IPv6 listener on the same port.
- Add a durable `admin` login alias for newly bootstrapped first administrators,
  preserving email login and shared throttling. Existing users are untouched.


## [0.3.0] - 2026-10-19

Third tagged release: theme quality brief (#33–#37) and compact admin/default density
(#32) after v0.2.0.

### Changed

- Compact density (#32): tighter admin shell padding, gaps, and control heights plus denser
  default-theme layout/type tokens; other ship-with themes unchanged; 44px touch targets kept.

### Documentation

- `SECURITY.md` supported-versions table lists `0.3.x` and `0.2.x` (Arabic summary matches);
  `0.1.x` is no longer supported.

### Themes

- Starter content honesty (PR5 / #37): company starter omits testimonials (no invented
  quotes/clients); illustrative project blurbs stay labeled as concepts; regression
  tests lock theme lang (no em dash / lorem), starter copy honesty, and catalog home
  OG + indexable (never `robots: noindex`). Theme versions `1.5.0`.
- Performance (PR4 / #36): site identity and builder brand tokens ship as cached
  `/site/identity.css` and `/site/builder-identity.css` (hash `?v=`) instead of per-request
  inline `<style>`; default 500 page drops `style=` attributes; theme scripts carry CSP
  nonce; docs fonts trimmed to ≤4 weights; catalog themes `1.4.0`.
- A11y + RTL hardening (PR3 / #35): shared `a11y.css` (after tokens) across catalog themes with
  contrast-safe `--muted`, `:focus-visible` rings (light accent / dark `--focus-ring`), 44px tap
  targets (prose inline links excluded), visible skip links, `prefers-reduced-motion`, Arabic
  `letter-spacing: 0` + no mono on body/dates, mirrored go arrows. Headless §6 acceptance audit in
  Playwright (`theme-acceptance.spec.ts`: home + 3 inners @ 1440 & 390). Theme versions `1.3.0`.
- Page anatomy (PR2 / #34): shared `anatomy.css` via `parts/shell.html`, reusable hero /
  section-head / list-row / closing-band components, default header with filled CTA + 44px
  mobile `<details>`, facts `<dl>` from real content/product counts, hairline cards with CSS
  cover fallback, numbered archive rows, footer `aria-labelledby` columns, and reading rhythm
  (~18px / 1.9 / 760px) on inner templates. Catalog themes keep personality CSS; versions bump
  to 1.2.0.
- Shared base design tokens (#33): `themes/_base/tokens.css` (served as
  `default/assets/tokens.css`) load first in every catalog theme (`default`, `business`,
  `magazine`, `portfolio`, `docs`). Themes override personality tokens; default migrates off
  hard-coded `:root` hex/spacing.
- Default RTL ship-blockers: dates no longer force LTR on `<time>` (isolate + `<bdi>`), Arabic
  headings keep `letter-spacing: 0` (home + editorial shell), magazine RTL drops Latin-only
  Georgia fallthrough, `/shop` product grid uses real CSS columns (3 / 2 / 1).
- Shipped themes still use precompiled Tailwind only; the Play runtime part stays opt-in for
  custom children (no CDN).

## [0.2.0] - 2026-10-18

Second tagged release after the CMS hosting gap sequence, launch-in-core path, admin/theme
polish, and operator runbooks landed on `main` (#16–#30).

### Added

- Productized staging / promote-to-live for single-process SQLite: `gorun staging seed`
  and `gorun staging promote` wrap backup + confined restore into a new `--dest`
  (never merges into a live data dir), with operator next-steps. Runbook:
  `docs/STAGING.md`.
- Embedded launch overlay stylesheet at `/static/launch.css` (`web/launch/launch.css`),
  served by the binary so Coolify/Traefik file mounts for that path can be removed
  after upgrade (see `deploy/coolify.md`).
- Launch journey in core: `/start`, `/start/login`, `/start/verify` with site-title
  titles (em dash) distinct from staff `/admin/login`; embed `/static/launch-polish.js`
  (title/validation agent). Drop Traefik mounts for that script too.

### Changed

- Admin UI polish: light non-flat workspace atmosphere, clearer sidebar/topbar hierarchy,
  brand-first auth panels (login/setup/magic/reauth), shared empty-state treatment on
  high-traffic lists, and quieter flash/form focus rhythm — still Zad + green selection, RTL-safe.
- Public theme polish: `default` home is a full-bleed brand-first band (pill featured badge
  removed); shared `home-composition` intro is edge-to-edge; light focus/empty/hero polish on
  business, portfolio, docs, and magazine (personalities unchanged). Themekit doctor still green.

### Themes

- Shared catalog contract in `pkg/themekit` (`ValidateDir` / `ValidateCatalogRoot`): required
  templates (via inheritance), own `lang/en.json` + `lang/ar.json`, `rtl` + `identity` supports,
  screenshot, sections/identity/builderStyle head, token selector, no third-party script CDNs.
  Documented in `docs/THEMES.md` and `docs/THEME-CONTRACT.md`; covered by ship-with Go tests.
- `gorun theme doctor [slug] [--json] [--warnings]`: filesystem contract checks via the same
  `pkg/themekit` validators (no database). Soft `{{t}}` lang-key scans are opt-in warnings.
- Ship-with uplift: `business` and `portfolio` gain bilingual lang files; business/portfolio
  comments and portfolio contact forms no longer depend on Tailwind utilities; full-bleed home
  heroes for business, magazine, portfolio and docs (each keeps its existing personality); docs
  tokens move under `:where(html[data-theme="docs"])`. E2E theme project smokes
  magazine/portfolio/docs home.

### Added

- Custom roles UI and REST API: Settings → Roles (`/admin/roles`) and `/api/v1/roles`
  (`users.manage`) create bilingual packs with explicit permissions on the existing
  `roles` / `role_permissions` tables. System roles stay undeletable; Administrator `*`
  is locked; actors may only grant permissions they hold; UI permission changes and
  deletes require recent password confirmation.
- Optional Prometheus text scrape at `GET /metrics` when `GORUN_METRICS=true` (stdlib exposition:
  HTTP status-class counters, job gauges including dead/failed, backup last-success timestamp).
  Optional `GORUN_METRICS_TOKEN` Bearer; off by default. Operator guide: `docs/OBSERVABILITY.md`.
- Local backup schedule + retain-N: System → Backups settings (`backup.schedule_hours`,
  `backup.retain`) drive hourly cron `backup.schedule` (secrets still excluded; no remote upload).
  `backup.Prune` keeps the newest N complete local pairs; `gorun backup --retain N` for host
  cron / Coolify `docker exec`. Manual create and encrypted S3 upload unchanged.
- `gorun backup-restore --input <stamp>.manifest.json --dest <new-directory-path>` restores a local
  backup pair into a destination that must not already exist (inspect preflight; never merges into a
  live data dir).
- Operations → Backups → Inspect / restore: bilingual preflight and confined restore under
  `<data>/restores/<stamp>/`, gated by recent password confirmation and `system.backup`.
- Restores are staged in a hidden `.<name>.partial` sibling and renamed into place only after the
  database and archive checksums verify, so an interrupted restore never looks complete; a leftover
  partial directory is reported on the inspect page and blocks the next restore until removed (admin
  can discard it with the same permission and reauth gate as restore).
- Restored files are always `0600` and directories `0700`, whatever modes the archive records.
- The inspect page explains each preflight error and warning in English and Arabic (the code stays
  alongside for support).
- Page builder layout history: `builder_revisions` stores save/publish/reset/restore snapshots
  (default keep 25 via `builder.revisions_keep`). Admin lists history with preview and
  restore-to-draft (restore requires recent password confirmation). REST and MCP expose
  list/get/restore; MCP restore is sensitive. Public HTML is unchanged until publish.

### Documentation

- Upgrade / rollback runbook (`docs/UPGRADE.md`): tag → binary or GHCR/Coolify image → migrate on
  `serve` → smoke → rollback with prior artifact + pre-upgrade backup pair + original secret key.
  Cross-linked from install, production, troubleshooting, Coolify §8, and the docs index. System
  Health and `/api/v1/system/health` expose the binary build as `version` (read-only; compare to
  GitHub Releases — no in-app updater).
- Coolify / Docker HTTPS acceptance checklist (`deploy/coolify.md` §9): discover trusted-proxy CIDR,
  verify cookie `Secure`, default HSTS (`max-age=31536000`) and opt-ins, and probe `/healthz` +
  `/readyz` from outside the container — linked from `docs/PRODUCTION.md`, `docs/TROUBLESHOOTING.md`,
  `docs/GORUN-INSTALLATION.md`, and `docs/README.md`.
- Operator docs expansion: `docs/README.md` index; `docs/TROUBLESHOOTING.md`; install/upgrade from
  release tags (binary + GHCR) in `docs/GORUN-INSTALLATION.md`; HTTPS / proxy / HSTS and restore
  cutover in `docs/PRODUCTION.md`; Coolify restore + upgrade + HSTS in `deploy/coolify.md`; bilingual
  admin and first-site / theme-switch paths in `docs/ADMIN_WORKSPACE.md` and `docs/SITE_BUILDER.md`;
  MCP operator overview in `docs/MCP.md`; README documentation tables link the new index.
- `docs/GORUN-INSTALLATION.md` no longer describes an old MIT license or a "source-available" policy:
  it states `AGPL-3.0-only`, as LICENSE, NOTICE and README do, and points to the public repository and
  releases. CONTRIBUTING names the SPDX identifier.
- README: new screenshots (1440×900 at 2×, fictional demo content) under each pillar in both languages
  (content list, editor, agent keys, approvals, a canvas with an agent result, the page builder, and the
  default theme on desktop and mobile), a `docker run` example for the GHCR image, links to the v0.1.0
  release, and short notes on precompiled theme CSS, trusted proxies, HSTS, backups and the DCO check.

### Fixed

- Job workers no longer leave a job `running` when shutdown cancels the context while the job is being
  claimed: the claim itself is not cancellable, so a claimed job is always finished or requeued (it
  previously waited for stale-job recovery). This also fixes the flaky `TestStartRequeuesStaleRunningJobs`.
- Provider truthfulness: System Health no longer marks a stored Stripe secret key or webhook secret as
  `ok` (both are `warn` until checkout/delivery are verified elsewhere). Content Studio, AI writing, and
  connection admin copy state that save or list/discovery probes do not prove generation works.


## [0.1.0] - 2026-10-18

The first tagged release: a multi-arch image on `ghcr.io/zadstack/gorun` and binary archives for
linux and darwin (amd64, arm64) with checksums.

### Release

- `.github/workflows/release.yml` runs on `v*` tags: it builds and pushes `ghcr.io/zadstack/gorun`
  for linux/amd64 and linux/arm64 (tags `X.Y.Z`, `X.Y`, `latest`; `VERSION` from the tag; OCI labels
  for source and license), checks that `gorun version` in the image matches, builds the binary
  archives with `SHA256SUMS`, and creates the GitHub release from this file. Pull requests that change
  the Dockerfile or the workflow run the builds without publishing.
- `deploy/Dockerfile` cross-compiles on the build platform (`TARGETOS`/`TARGETARCH`) and carries OCI
  source and license labels.

### Media privacy

- New JPEG, PNG and WebP uploads are stored without camera metadata (GPS, device serials, names,
  XMP/IPTC, comments, JPEG trailers such as HDR gain maps). Stripping is lossless; ICC profiles and
  the JPEG EXIF Orientation are kept. Setting `media.strip_metadata` (default on) turns it off.
  Existing files are unchanged.
- Variants of rotated photos (EXIF orientation 2–8) are now generated upright, and the recorded
  width and height are the displayed ones.

### Fixed

- A process that runs job workers (`gorun serve`, `gorun mcp --jobs`, `gorun reindex` without
  `--no-jobs`) now holds an exclusive OS lock on `<data>/gorun.lock`; a second one on the same data
  directory refuses to start and names the holder. The lock is released on shutdown or if the
  process dies.
- Startup no longer requeues every running job. Workers refresh `locked_at` every 30 seconds, and only
  running jobs whose heartbeat is older than 2 minutes are requeued, at startup and on every scheduler
  tick, so jobs still executing elsewhere are never run twice.
- Forwarding headers from a peer outside `GORUN_TRUSTED_PROXIES` are logged as a warning (first
  occurrence, then at most hourly), since behind an unlisted proxy every visitor shares its address.
- A data directory whose path contains `?`, `#` or `%` opened the wrong database file and silently
  lost the WAL, foreign-key and busy-timeout pragmas; the SQLite `file:` URI is now percent-encoded
  (backups too).
- The checkout result page loads the embedded HTMX instead of an unpinned jsDelivr copy.
- WordPress migration render checks no longer disable the private-network block because of the
  admin-editable site base URL; they reach a private address only when it matches the configured
  `GORUN_BASE_URL` (or `GORUN_ALLOW_PRIVATE_NET` is set).
- `HEAD` requests to GET routes (`/admin/setup`, `/healthz`, …) answer like GET without a body instead
  of 405.
- An MCP approval decided with an API key now requires that key's owner to differ from the owner of
  the request; one admin with two keys can no longer approve their own calls. Single-admin sites can
  opt out with `GORUN_MCP_SAME_OWNER_APPROVAL=true` / `mcp_same_owner_approval`. Approving in the admin
  UI is unchanged.

### Changed

- **Default theme CSS is precompiled.** The default theme (and `docs`) links `themes/default/assets/tailwind.css`
  (12.4 KB, 3.3 KB gzipped) instead of loading the in-browser Tailwind compiler (407 KB, 127 KB gzipped)
  on every page. `make css` rebuilds it with the pinned, checksum-verified standalone CLI; CI fails when it is
  stale. Custom themes that need the runtime add `{{part "tailwind-runtime" .}}` to `parts/head-extra.html`.
- Text responses (HTML, CSS, JS, JSON, XML, SVG) are gzip-compressed when the client accepts it.
  Admin pages stay uncompressed (they carry the CSRF token; BREACH).
- Version-named vendor files and `?v=` asset URLs are served with
  `Cache-Control: public, max-age=31536000, immutable`.
- **Public CSP:** public pages no longer allow scripts from `cdn.tailwindcss.com`,
  `cdn.jsdelivr.net` or `unpkg.com`, nor `'unsafe-eval'`; the bundled themes need none of them. A custom
  theme that does can be allowed with `GORUN_CSP_SCRIPT_SRC` / `csp_script_src`. `/api/docs` allows only
  the pinned Scalar bundle, now with SRI. The admin keeps `'unsafe-eval'` for Alpine.
- CI runs staticcheck (`staticcheck.conf`); unused code it found was removed.
- **Local backups leave out `secret.key` and `gorun.toml` by default.** Keep them separately (a password
  manager or offline copy) and restore them next to the archive; `gorun backup create --include-secrets`
  restores the old behaviour for an archive you protect yourself. Admin and remote backups never include
  them.
- Downloading a backup from the admin requires confirming your password within the last 10 minutes
  (`/admin/reauth`); signing in counts. Migration `0009` adds `sessions.reauth_at`.
- **HSTS:** the header is now `max-age=31536000` without `includeSubDomains` by default.
  `GORUN_HSTS_INCLUDE_SUBDOMAINS=true` and `GORUN_HSTS_PRELOAD=true` (`hsts_include_subdomains`,
  `hsts_preload`) add the directives; preload requires includeSubDomains. Set the first to keep the
  previous header.
- GitHub Actions are pinned to commit SHAs, and Dependabot proposes weekly Go module, Actions and e2e
  npm updates.
- `deploy/docker-compose.yml` passes `VERSION` (or Coolify's `SOURCE_COMMIT`) to the image build.

### Documentation

- Outside contributions are accepted under the Developer Certificate of Origin: commits are signed off with
  `git commit -s`, checked by the `dco` workflow (bot commits skipped). There is no CLA.
- `deploy/docker-compose.yml` and `deploy/coolify.md` configure `GORUN_TRUSTED_PROXIES`.
- SECURITY.md and docs/PRODUCTION.md now state that no forwarding headers are trusted by default.

### First public release

- GoRun is published as open source under the GNU Affero General Public License v3.0 (see
  `LICENSE` and `NOTICE`).
- The command line lives in the importable package `pkg/cli`; `cmd/gorun` is a thin wrapper.
  A separate binary can add modules, commands, boot-order entries, error-inventory categories and
  a trusted site-policy directory through `cli.Option`s (see docs/ARCHITECTURE.md, "Building on
  the core"). Every core command and flag is unchanged.
- The trusted directory for `GORUN_SITE_POLICY` files defaults to `/var/lib/gorun-site-policies`;
  a downstream build can set another one with `cli.WithSitePolicyDirectory`.
- Hosting-platform add-ons, brand themes and the commands that served them (`tenant-init`,
  `addon-install`, `page-catalog`) are no longer part of the core.

[Unreleased]: https://github.com/zadstack/gorun/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/zadstack/gorun/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/zadstack/gorun/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/zadstack/gorun/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/zadstack/gorun/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/zadstack/gorun/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/zadstack/gorun/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/zadstack/gorun/releases/tag/v0.1.0
