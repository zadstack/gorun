# Site analytics: built-in, cookieless, Umami-like

Status: design approved in conversation (sections 1–3), awaiting written-spec review.

## Goal

From the first install, every GoRun site shows its owner who visits, what they read, where they came from, on what
device and from which country, and how often its forms convert. No external service, no cookie, no consent banner.

## What the user decided

- **Metrics on day one:**
  - visitors, page views and top pages;
  - traffic sources (referrers, UTM);
  - devices and countries;
  - form conversions (from the site-forms feature).
- **Unique visitors are cookieless:** `sha256(daily random salt + site + IP + user agent)`. The salt is discarded daily.
- **Collection:** a tiny first-party script beacon.
- **Packaging:** an optional module, enabled by default (`kernel.OptionalModule`, like commerce).
- **Retention:** raw events are kept forever.
- **Storage:** a separate SQLite file, `analytics.db`, in the data dir.
- **Permission:** a new `analytics.view` permission, so a marketer can read analytics without managing settings.
- **GeoIP:** optional. The operator drops `GeoLite2-Country.mmdb` into the data dir; without it, country is "unknown".

## Today (0.18.7)

- There is no visitor analytics. `internal/metrics` is a Prometheus exposition of server health only.
- **Optional modules:** feature modules boot after `webhooks` (`pkg/cli/modules.go`).
- **Permissions:** declared with `d.Perms.Declare` (see `internal/commerce/module.go:63`).
- **Dashboard widgets:** `d.Admin.AddWidget`.
- **Database:** `internal/db.Open(path)` opens a WAL SQLite database.
- **Backups:** `internal/backup` snapshot `gorun.db` plus a files archive.

## Design

### 1. Module

`internal/analytics`, implementing `kernel.OptionalModule` (enabled unless `GORUN_ADDONS` says
`{"analytics":"disabled"}`).

- It is listed after `webhooks` in `pkg/cli/modules.go` and imports no feature module.
- It subscribes to the hook `site.form.submitted` by its string name.
- It declares the permission `analytics.view` (en "View analytics", ar "عرض الإحصائيات"). Administrators already hold it through
  their wildcard grant.
- It owns `analytics.db`, opened with `db.Open`, plus its own migrations in `internal/analytics/migrations/`. If the file is
  missing it is created empty. A failure to open it is logged and leaves the site serving normally with
  collection off.

### 2. Collection

- **Script `GET /_a/s.js`**
  - Under 1 KB, served from the site with `Cache-Control: public, max-age=86400`.
  - Injected as `<script defer src="/_a/s.js"></script>` right before `</head>` by a public-router middleware.
  - Injection happens only on `GET`, status 200, `text/html` responses **rendered by the theme** (amended by the final
    review: the theme renderer marks its pages with `httpx.MarkThemePage`; a module's own HTML page is never injected).
  - Skipped for builder or theme preview requests, any response under `/admin`, signed-in staff (any user with
    `content.edit`), excluded paths, and when tracking is switched off.
  - Hard exclusions (amended): `/auth/`, `/admin` and `/_a/` are never tracked, in injection, beacons and form submits,
    whatever the saved exclusions say (`/auth/magic/{token}` carries a live login token).
  - Paths are canonical (amended): decoded, then re-escaped as `net/url` escapes a path, for views, submits, the
    injection exclusion check and saved exclusion prefixes.
  - The beacon rate limit keys an IPv4 address by /32 and an IPv6 address by /64; an empty or unparseable IP shares
    one constant bucket (amended).
  - It must satisfy the site's CSP (same-origin `script-src`; check the CSP middleware and add the path if needed).
- **The beacon**
  - On load, and after `history.pushState` or `popstate`, it sends `navigator.sendBeacon('/_a/e', JSON)` with:
    - `p`: path, without query except `utm_source`, `utm_medium` and `utm_campaign`;
    - `r`: `document.referrer`;
    - `w`: `innerWidth`;
    - `l`: `document.documentElement.lang`.
  - It sends nothing when `navigator.doNotTrack == "1"` or `navigator.globalPrivacyControl` is true.
- **`POST /_a/e`**
  - Has no CSRF token (it is a beacon). It accepts only same-site requests: `Sec-Fetch-Site` is `same-origin` or absent, and
    `Origin`, when present, equals the site origin.
  - Per-IP rate limit of 120 per minute. The body is capped at 2 KiB.
  - Always answers `204`, so client errors are invisible to visitors and bots learn nothing.
  - **Bot filter:** a user agent matching a maintained list (`bot`, `crawler`, `spider`, `headless`, known monitors) is dropped.
  - **Referrer:** reduced to its host. It becomes `direct` when empty or when the host equals the site's own.
  - **Device:** from `w`: mobile below 768, tablet below 1024, else desktop.
  - **Browser and OS:** parsed from the user agent into coarse families (Chrome, Safari, Firefox, Edge, Samsung, Opera,
    other; iOS, Android, Windows, macOS, Linux, other). The raw user agent is never stored.
  - **Country:** looked up from the IP with the optional GeoIP file (ISO code), else `"--"`. The IP is never stored.
  - **Visitor:** `hex(sha256(salt_today || site_id || ip || ua))[:16]`. `salt_today` is 32 random bytes kept in memory and
    in `analytics.db`, and is regenerated at UTC midnight; the previous salt is deleted.
- **Form submissions**
  - The `site.form.submitted` handler decodes the payload (struct or JSON). It records a `submit` event with the form
    slug and the submission's page path.
  - The visitor is unknown for these events (empty), and they count submissions only.
- **Writes**
  - An in-memory buffer (cap 10,000) is flushed every 2 seconds in batches of up to 500 rows per transaction.
  - Overflow is dropped with a rate-limited warning log, never blocking a request.
  - The buffer is flushed on shutdown.

### 3. Storage (`analytics.db`)

```
events(id INTEGER PRIMARY KEY, ts TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('view','submit')),
       path TEXT NOT NULL, visitor TEXT NOT NULL DEFAULT '', ref_host TEXT NOT NULL DEFAULT '',
       utm_source TEXT NOT NULL DEFAULT '', utm_medium TEXT NOT NULL DEFAULT '', utm_campaign TEXT NOT NULL DEFAULT '',
       browser TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', device TEXT NOT NULL DEFAULT '',
       country TEXT NOT NULL DEFAULT '', locale TEXT NOT NULL DEFAULT '', form TEXT NOT NULL DEFAULT '')
  index (ts), (path, ts), (kind, ts, visitor)  -- covering index for range visitor counts (amended)
daily(day TEXT, dim TEXT, key TEXT, views INTEGER, visitors INTEGER, submits INTEGER, PRIMARY KEY(day, dim, key))
  dim ∈ total | page | ref | utm | browser | os | device | country | form
salts(day TEXT PRIMARY KEY, salt BLOB NOT NULL)
```

- **Retention:** raw events are kept forever.
- **Rollups:**
  - An hourly job recomputes `daily` for today and yesterday from `events` with replace semantics, so it is
    idempotent.
  - A one-time backfill runs on first start, and also when `daily` is empty but `events` is not.
- **Reads:**
  - Ranges ending before today read `daily`. Today reads `events`.
  - Visitors across a multi-day range use `COUNT(DISTINCT visitor)` on `events`, because summing daily values double
    counts.
- **Backups:**
  - `internal/backup` gains an optional `analytics` member: a consistent `VACUUM INTO` copy named
    `<stamp>-analytics.db`, recorded in the manifest as optional. Older manifests stay valid, and restore puts both
    files back.
  - A setting "Exclude analytics from backups" (off by default) skips it.
- **Clearing data:**
  - In settings, "Delete all analytics" (typed confirmation) and "Delete before date" are available.
  - Both are audited, and the file is `VACUUM`ed afterwards.

### 4. Admin

- **`/admin/analytics`** (nav under Site, permission `analytics.view`)
  - **Period:** today, 7 days, 30 days, 12 months or custom, with a comparison against the previous equal period.
  - **Cards:** visitors, views, views per visitor, and submissions. Each card shows its change as a signed percentage and a word, never
    colour alone.
  - **Chart:** visitors and views over days (hours for a single day). It is a server-rendered SVG with no JS library,
    paired with a visually hidden data table. The time axis runs inline-start to inline-end, so it is mirrored in RTL.
  - **Tables:** top pages; sources, with a "Campaigns" tab for UTM; countries; and devices, with browser and OS tabs.
    - Each shows its top 10 with a proportion bar and a "Show all" link.
    - Clicking a row filters the whole dashboard. Filters show as removable chips and live in the URL query.
  - **Forms:** per form, its page's views, its submissions and the conversion rate, each linking to `/admin/forms/{slug}`.
    The section is hidden when there are no forms.
  - **Now:** visitors in the last 5 minutes. It refreshes every 30 s while the tab is visible.
- **Dashboard widget:** 7-day visitors and views with a sparkline, linking to the full page.
- **CSV export:** for any table, honouring the period and filters.
- **API:** `GET /api/v1/analytics/summary?from=&to=&page=&ref=&country=&device=` (scope `analytics.view`), listed in
  OpenAPI.
- **Settings at `/admin/settings/analytics`** (`settings.manage`):
  - tracking on/off;
  - excluded path prefixes (one per line);
  - exclude from backups;
  - delete data;
  - GeoIP status (found or missing, with where to put the file).
- **Accessibility and i18n:** ar and en labels for every string, numbers formatted per admin locale, real `<table>`s, 44px
  controls on coarse pointers, and stacked layout on mobile.

## Out of scope

- Custom events beyond form submissions.
- Funnels.
- Public or shared dashboards.
- Heatmaps.
- Session replay.
- Import from Google Analytics.
- Server-side collection.
- City-level geo.
- Per-user (non-anonymous) tracking.

## Files (expected)

| area | files |
|---|---|
| module, storage, collection | new `internal/analytics/` (module.go, collect.go, store.go, rollup.go, ua.go, geo.go, migrations/) |
| wiring | `pkg/cli/modules.go`, `internal/backup` (optional member), CSP config if needed |
| admin | `internal/analytics/admin.go`, `web/admin/templates/pages/analytics/*.html`, a small `web/admin/static/analytics.js` for tabs and the "now" refresh |
| api | `internal/analytics/api.go` |
| docs | new `docs/ANALYTICS.md`, `docs/BOUNDARIES.md`, `docs/BACKUPS.md`, `CHANGELOG.md` |

## Testing

- **Go unit tests:**
  - visitor hash stability within a day and change across days;
  - salt rotation deletes the old salt;
  - DNT is handled client-side, so add a script unit check;
  - the bot filter;
  - UA parsing families;
  - referrer reduction, including self-referral becoming `direct`;
  - device buckets;
  - rate limit, origin check and body cap;
  - the buffer flushes in batches and drops on overflow without blocking;
  - rollup idempotence;
  - multi-day visitor distinct counts;
  - form `submit` events from the hook payload;
  - backup includes and excludes the analytics member, and restore round-trips;
  - delete-before-date;
  - module disabled means no script, no routes and no file.
- **E2E (`e2e/tests/analytics.spec.ts`):**
  - A logged-out visitor views pages and a beacon is recorded. The admin dashboard shows the counts, top pages
    and the source.
  - A logged-in admin's own visits are not counted.
  - DNT stops the beacon.
  - Filters update the URL and the tables.
  - Form conversion appears after a submit.
  - The dashboard works in ar (RTL chart) and en.
- **Performance check:** 5,000 beacons in a burst flush without failing requests (a Go benchmark or load test, reported).

## Review focus

1. **A site behind a reverse proxy:** the IP comes from the existing trusted-proxy logic (`audit.IPFromContext`), not a raw
   header, so visitor hashes don't collapse to the proxy IP.
2. **Single-page navigation that changes only the hash:** the URL change must not double count. Only path changes count.
3. **A very long path or a path with query secrets:** the query is stripped (except `utm_*`), and paths are capped at 512
   characters.
4. **The clock crossing UTC midnight between view and flush:** the event keeps the day of its view. The salt used is that
   day's salt.
5. **`analytics.db` deleted while running:** writes reopen and recreate it, and the dashboard shows empty data instead of a
   500 error.
