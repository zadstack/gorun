# Site Analytics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship built-in, cookieless page-view analytics with an admin dashboard. It covers visitors, top pages, sources, devices, countries and form conversion, stored in its own `analytics.db`.

**Architecture:**
- A new optional module `internal/analytics` (on by default) owns `analytics.db`, plus a beacon script and endpoint, an in-memory write buffer, hourly rollups, the admin dashboard, settings, an API and a dashboard widget.
- It hooks into `site.form.submitted` by name.
- Backups gain an optional analytics member by file-name convention, so `internal/backup` never imports `analytics`.

**Tech Stack:** Go, SQLite (WAL), html/template, server-rendered SVG, a little plain JS, Playwright.

**Spec:** `docs/superpowers/specs/2026-11-03-site-analytics-design.md`

## Global Constraints

- **Commits:** `git -c user.name=ahmedvnabil -c user.email=vacrom414@gmail.com commit -s`, with the trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Module:** `analytics`, implementing `kernel.OptionalModule` (`Enabled(ctx, *sql.DB) (bool, error)` returns true). It is listed in `pkg/cli/modules.go` after `webhooks`. It imports no feature module, and nothing imports it except `pkg/cli`.
- **Permission:** `analytics.view` (en "View analytics", ar "عرض الإحصائيات"), declared with `d.Perms.Declare`. Settings use the existing `settings.manage`.
- **Database:** `<dataDir>/analytics.db`, opened with `internal/db.Open`. It has its own migrations in `internal/analytics/migrations/`, and its schema follows spec §3 exactly.
- **Visitor:** `hex(sha256(salt || siteID || ip || ua))[:16]`. The salt is 32 random bytes per UTC day, kept in `salts`, and older days are deleted at rotation. The IP comes from `audit.IPFromContext`.
- **Never stored:** the raw IP, the raw user agent, and query strings (except `utm_source`, `utm_medium`, `utm_campaign`). Paths are capped at 512 characters.
- **Endpoints:**
  - `GET /_a/s.js`: under 1 KB, `Cache-Control: public, max-age=86400`.
  - `POST /_a/e`: always 204, 2 KiB body cap, 120/min per IP, same-site only (`Sec-Fetch-Site` same-origin or absent, and `Origin` equal to the site origin when present).
- **Device buckets** from width: `<768` is mobile, `<1024` is tablet, anything else is desktop. **Unknown country** is `--`.
- **Write buffer:** cap 10,000, flushed every 2 s in batches of up to 500 rows per transaction. Overflow is dropped with a rate-limited warning. The buffer is flushed on shutdown.
- **Rollups:** the hourly cron `analytics.rollup` recomputes `daily` for today and yesterday with replace semantics, and backfills when `daily` is empty.
- **Retention:** raw events are kept forever. "Delete all" and "Delete before date" are audited and VACUUM the file afterwards.
- **i18n:** ar and en labels for every string. RTL-mirrored chart. Real `<table>`s. 44px controls on coarse pointers.
- **Tests:**
  - `go test ./internal/analytics/... ./internal/backup/...`;
  - e2e from `e2e/`: `npx playwright test tests/global.setup.ts tests/<spec>.ts --project=setup --project=chromium`.

## Rulings (made while planning)

- **A1. GeoIP library.** GeoIP reads MMDB with `github.com/oschwald/maxminddb-golang/v2` (ISC licence). Add it to go.mod and add its licence to `docs/licenses/` and to NOTICE the way the existing deps are listed. Writing our own MMDB reader is riskier than one small, widely used dependency. Cost if wrong: one dependency.
- **A2. Backup by convention.** `internal/backup` treats `<dataDir>/analytics.db` as an optional member by file name.
  - `backup.Options` gains `SkipAnalytics bool`.
  - The callers (admin backup handlers and the CLI) set it from the setting `analytics.backup_exclude`.
  - This keeps backup free of feature imports. Cost if wrong: a rename means editing two places.
- **A3. Script injection.** Injection happens in a middleware on the public router. It wraps responses and inserts the script tag before the first `</head>` of `GET` 200 `text/html` bodies. The script is same-origin, so the current CSP (`script-src` includes self, `connect-src 'self'`) already allows it. Verify it, and adjust only if needed. Cost if wrong: a template-func approach later.
- **A4. Staff are not counted.** A request whose user has `content.edit` gets no script, and its beacons are ignored. Cost if wrong: the owner sees fewer views of their own site.

## Review Focus

1. **Behind a reverse proxy,** visitors must not collapse to one hash. The IP comes from `audit.IPFromContext` (Task 2 test `TestVisitorUsesTrustedClientIP`).
2. **A hash-only URL change** (`#section`) must not count a second view (Task 2 script check `TestScriptIgnoresHashChange`).
3. **A long path or a query carrying secrets** is stored stripped and capped (Task 2 test `TestBeaconStripsQueryAndCapsPath`).
4. **A view just before UTC midnight that is flushed after midnight** keeps its own day and that day's salt (Task 1 test `TestFlushKeepsEventDay`).
5. **If `analytics.db` is deleted while running,** writes recreate it and the dashboard shows empty data instead of a 500 (Task 1 test `TestStoreRecreatesMissingFile`, Task 4 test `TestDashboardEmptyAfterFileLoss`).

---

### Task 1: Module skeleton, `analytics.db`, store and buffer

**Files:** create `internal/analytics/{module.go,store.go,buffer.go,salt.go,store_test.go,buffer_test.go,salt_test.go}`, `internal/analytics/migrations/0001_init.sql`; modify `pkg/cli/modules.go`, `docs/BOUNDARIES.md` (add the module row).

**Interfaces (produces):**
- `type Event struct { TS time.Time; Kind, Path, Visitor, RefHost, UTMSource, UTMMedium, UTMCampaign, Browser, OS, Device, Country, Locale, Form string }`
- `type Store struct{...}`, created by `func OpenStore(dataDir string) (*Store, error)`, with these methods:
  - `Insert(ctx, []Event) error`
  - `Close() error`
  - `DB() *sql.DB`
  - `Reopen() error`, used by the recreate path
- `type Buffer struct{...}`:
  - `func NewBuffer(s *Store, cap int, every time.Duration, batch int, log *slog.Logger) *Buffer`
  - `Add(Event) bool`, which returns false when the event is dropped
  - `Flush(ctx) error`
  - `Run(ctx)`
- `func (s *Store) VisitorHash(ctx, day time.Time, siteID, ip, ua string) (string, error)`: gets or creates the salt for that day and deletes older salts.

- [ ] **Step 1: Write the failing tests.**
  - `TestOpenStoreMigrates`: the tables `events`, `daily` and `salts` exist.
  - `TestInsertBatch`: 1,200 events are stored, and are written in at most 3 transactions. Assert through a counter hook or by timing.
  - `TestBufferDropsOnOverflow`: with cap 3, the 4th `Add` returns false and the warning is logged at most once per interval.
  - `TestBufferFlushOnStop`.
  - `TestVisitorHashStableWithinDayChangesAcrossDays`.
  - `TestSaltRotationDeletesOldSalts`.
  - `TestFlushKeepsEventDay`: an event whose TS is 23:59:59Z keeps that date after the flush.
  - `TestStoreRecreatesMissingFile`: delete the file, then insert; the call succeeds and the file exists.
  - `TestModuleDisabled`: with `Addons{"analytics":"disabled"}`, the app boots, there is no `analytics.db`, and `/_a/s.js` returns 404. Use the kernel boot test helpers other modules use.
- [ ] **Step 2: Run** `go test ./internal/analytics/...` and expect FAIL.
- [ ] **Step 3: Implement.**
  - The module's `Register` opens the store, starts the buffer goroutine (stopped on app close), declares `analytics.view` and adds the `analytics.rollup` cron (its body comes in Task 3).
  - If the store fails to open, log the error and register nothing else.
- [ ] **Step 4: Run** `go test ./internal/analytics/... ./internal/kernel/...` and expect PASS. The boundaries test in kernel must pass.
- [ ] **Step 5: Commit** `feat(analytics): module, analytics.db store and write buffer`.

### Task 2: Collection (script, beacon, injection)

**Files:** create `internal/analytics/{collect.go,ua.go,geo.go,script.js (embedded),collect_test.go,ua_test.go}`; modify the module registration, `go.mod`/`go.sum`, and `docs/licenses` plus NOTICE (A1).

**Interfaces:**
- Consumes the Task 1 `Buffer.Add` and `Store.VisitorHash`.
- Produces:
  - `func parseUA(ua string) (browser, os string, bot bool)`
  - `func refHost(ref, siteHost string) string`
  - `func deviceFor(width int) string`
  - `func cleanPath(raw string) (path string, utm [3]string)`
  - `type geo interface{ Country(ip string) string }` with an MMDB implementation and a nil implementation that returns `--`

- [ ] **Step 1: Write the failing tests.**
  - `TestScriptServed`: 200, under 1,024 bytes, the cache header, and JS content-type.
  - `TestScriptInjectedOnPublicHTML`: a public page's HTML has `<script defer src="/_a/s.js"></script>` exactly once, before `</head>`.
  - `TestNoInjectionForAdminPreviewStaffOrExcluded`: no script under `/admin`, on a preview request, for a signed-in user with `content.edit`, on an excluded path prefix (setting `analytics.exclude`), or when `analytics.enabled=false`.
  - `TestBeaconRecordsView`: a POST with a JSON body creates one `view` event after a flush, with the device, browser, OS, `ref_host`, locale and visitor filled in.
  - `TestBeaconAlways204`: bad JSON, a bot user agent, a cross-origin `Origin`, `Sec-Fetch-Site: cross-site`, a body over 2 KiB, and the rate limit at the 121st request all return 204, and none of them records anything.
  - `TestBeaconStripsQueryAndCapsPath`: `/a?token=x&utm_source=nl` stores path `/a` with `utm_source` set to `nl`. A 600-character path is cut to 512.
  - `TestVisitorUsesTrustedClientIP`: two requests from different client IPs behind the configured proxy give different visitors.
  - `TestStaffBeaconIgnored`.
  - `TestRefHostSelfIsDirect`.
  - `TestDeviceBuckets`: 767 is mobile, 768 is tablet, 1023 is tablet, 1024 is desktop.
  - UA family table tests.
  - `TestScriptIgnoresHashChange`: this runs in the Task 6 e2e. Here, assert the script source only patches `pushState` and `popstate` and compares `location.pathname`.
- [ ] **Step 2:** Run the tests and expect FAIL.
- [ ] **Step 3:** Implement per spec §2.
  - The script respects `navigator.doNotTrack == "1"` and `globalPrivacyControl`.
  - The beacon uses `sendBeacon`, with a `fetch(..., {keepalive:true})` fallback.
  - The GeoIP file is `<dataDir>/GeoLite2-Country.mmdb`, opened once, and missing means nil.
- [ ] **Step 4:** Run `go test ./internal/analytics/...` and `go vet ./...` and expect PASS.
- [ ] **Step 5: Commit** `feat(analytics): cookieless beacon collection and script injection`.

### Task 3: Form events, rollups and the query layer

**Files:** create `internal/analytics/{forms.go,rollup.go,query.go,forms_test.go,rollup_test.go,query_test.go}`.

**Interfaces:**
- Produces:
  - `type Filter struct{ From, To time.Time; Page, Ref, Country, Device string }`
  - `type Summary struct{ Visitors, Views, Submits int; ViewsPerVisitor float64; Prev *Summary }`
  - `type Point struct{ At time.Time; Visitors, Views int }`
  - `type Row struct{ Key string; Visitors, Views int }`
  - `type FormRow struct{ Form, Page string; Views, Submits int; Rate float64 }`
  - `func (s *Store) Summary(ctx, Filter) (Summary, error)`, where `Prev` is the equal-length previous period
  - `func (s *Store) Series(ctx, Filter) ([]Point, error)`: hourly for a single day, else daily
  - `func (s *Store) Top(ctx, Filter, dim string, limit int) ([]Row, error)`, where dim is one of `page|ref|utm|browser|os|device|country`
  - `func (s *Store) Forms(ctx, Filter) ([]FormRow, error)`
  - `func (s *Store) Now(ctx, time.Time) (int, error)`: visitors in the last 5 minutes
  - `func (s *Store) Rollup(ctx, now time.Time) error`

- [ ] **Step 1: Write the failing tests.**
  - `TestFormSubmittedHookRecordsSubmit`: both a struct payload and a JSON payload record a `submit` event with the form and page.
  - `TestRollupIdempotent`: running the rollup twice gives identical `daily`.
  - `TestRollupBackfillsWhenEmpty`.
  - `TestMultiDayVisitorsDistinct`: the same visitor hash on 3 days within one day's salt is impossible, so seed explicit hashes. A 3-day range counts distinct hashes, not the sum of daily counts.
  - `TestSummaryPrevPeriod`.
  - `TestSeriesHourlyForSingleDay`.
  - `TestTopFiltered`: the filter page `/a` narrows the `ref` rows.
  - `TestFormsConversion`: 10 views of `/contact-us` and 2 submits give a rate of 0.2.
  - `TestNowFiveMinutes`.
  - `TestTodayReadsEventsPastReadsDaily`: delete today's `daily` rows and the result is still correct.
- [ ] **Step 2:** Run the tests and expect FAIL.
- [ ] **Step 3:** Implement. Ranges that end before today read `daily`, today reads `events`, and visitor counts over a range always use `COUNT(DISTINCT visitor)` on `events`. Wire the cron body.
- [ ] **Step 4:** Run the tests and expect PASS.
- [ ] **Step 5: Commit** `feat(analytics): form submits, daily rollups and queries`.

### Task 4: Admin dashboard, widget, CSV, API and settings

**Files:**
- Create `internal/analytics/{admin.go,chart.go,api.go,settings.go,admin_test.go,chart_test.go,api_test.go}`.
- Create `web/admin/templates/pages/analytics/{dashboard.html,settings.html}`, `web/admin/static/analytics.js` and the analytics catalogs (`internal/analytics/catalog/analytics.{ar,en}.json`). Follow how other modules ship catalogs (`kernel.CatalogProvider`).

**Interfaces:** consumes the Task 3 query methods.

- [ ] **Step 1: Write the failing tests.**
  - `TestDashboardRequiresPermission`: an editor without `analytics.view` gets 403. A role granted `analytics.view` gets 200.
  - `TestDashboardShowsCardsChartTables`: seed events. The cards show their numbers and a signed change, the SVG has a `<title>`, the hidden data table has the series, and the top pages are listed.
  - `TestFilterChipsInURL`: `?page=/a` shows a removable chip, and the tables are filtered.
  - `TestChartMirroredRTL`: in ar, the first point is at the inline start. Assert the transform or the x order.
  - `TestCSVExport`: per table, with the filter and period honoured, through `csvSafe`-equivalent escaping.
  - `TestAPISummary`: `GET /api/v1/analytics/summary` with the `analytics.view` scope returns JSON with summary, series and top. Without the scope it returns 403. It is listed in OpenAPI.
  - `TestWidget`: the dashboard widget renders the 7-day numbers.
  - `TestSettings`:
    - toggling tracking off stops injection;
    - the excluded prefixes are saved;
    - "Delete before date" needs the typed confirmation and is audited;
    - the GeoIP status reads found or missing.
  - `TestDashboardEmptyAfterFileLoss`.
- [ ] **Step 2:** Run the tests and expect FAIL.
- [ ] **Step 3:** Implement per spec §4.
  - The chart is server SVG, and the RTL mirror uses `transform` or reversed x.
  - `analytics.js` handles only the tabs and the "now" refresh: every 30 s, paused while `document.hidden`.
  - The nav item sits under the Site group.
- [ ] **Step 4:** Run `go test ./internal/analytics/...` and expect PASS.
- [ ] **Step 5: Commit** `feat(analytics): admin dashboard, widget, CSV, API and settings`.

### Task 5: Backups

**Files:** modify `internal/backup/{backup.go,manifest.go,restore*.go}` and their tests, the admin and CLI backup callers (to pass `SkipAnalytics` from the setting `analytics.backup_exclude`), and `docs/BACKUPS.md`.

- [ ] **Step 1: Write the failing tests.**
  - `TestBackupIncludesAnalyticsWhenPresent`: there is a `<stamp>-analytics.db` that is a consistent `VACUUM INTO` copy, and the manifest records it as an optional member with a digest.
  - `TestBackupSkipsAnalytics`: with `SkipAnalytics` set, the member is absent.
  - `TestBackupWithoutAnalyticsFile`: when the file does not exist, nothing about analytics is recorded.
  - `TestOldManifestStillValid`.
  - `TestRestoreRoundTripsAnalytics`.
- [ ] **Step 2:** Run the tests and expect FAIL.
- [ ] **Step 3:** Implement (ruling A2).
- [ ] **Step 4:** Run `go test ./internal/backup/... ./internal/admin/...` and expect PASS.
- [ ] **Step 5: Commit** `feat(backup): optional analytics member`.

### Task 6: End to end, docs and changelog

**Files:** create `e2e/tests/analytics.spec.ts` and `docs/ANALYTICS.md` (English, then an `## العربية` section); modify `CHANGELOG.md` (`## [Unreleased]` → `### Added`), `docs/README.md` (link) and `docs/BOUNDARIES.md` (routes `/_a/s.js`, `/_a/e`, `/admin/analytics*`, `/admin/settings/analytics`, the hook `site.form.submitted` consumed by analytics, and the `analytics.view` permission).

- [ ] **Step 1:** Write the spec.
  - A logged-out visitor context views 3 pages (one with `?utm_source=nl`) and follows a link from another page.
  - After the buffer flushes (poll the admin dashboard), the admin sees visitors ≥1, views 3+, top pages, `nl` under Campaigns, and the source.
  - A signed-in admin browsing adds nothing.
  - A visitor with DNT on (`page.addInitScript` setting `navigator.doNotTrack`) sends no `/_a/e`.
  - `TestScriptIgnoresHashChange`: changing only `location.hash` sends no second beacon.
  - Filters update the URL.
  - A form submit (use a site form from the forms feature if it is merged, else skip this check with a note) shows in Forms.
  - The ar dashboard has `dir=rtl`, and the chart is present.
- [ ] **Step 2:** Run it together with the e2e specs that touch the public pages, the dashboard and backups (`grep -lEi 'dashboard|backup|public|theme' e2e/tests/*.ts`), and expect PASS.
- [ ] **Step 3:** Write the docs and the changelog.
- [ ] **Step 4:** Run `go vet ./... && go test ./...` in the background and expect PASS. Also run the quality checks: `gofmt -l`, `go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...` and `python3 tools/docs-check.py`.
- [ ] **Step 5: Commit** `docs(analytics): guide, changelog and boundaries`.
