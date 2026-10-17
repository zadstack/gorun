# End-to-end tests / اختبارات الواجهة من البداية إلى النهاية

The browser suite lives in `e2e/`: a Playwright project with its own `package.json`. Node and npm are
used **only** inside `e2e/`; the Go application has no node dependencies.

مجموعة اختبارات المتصفح موجودة في `e2e/`: مشروع Playwright له `package.json` خاص به. يُستخدم Node
وnpm داخل `e2e/` فقط؛ تطبيق Go نفسه لا يعتمد على أي حزمة node.

## Run / التشغيل

```sh
make e2e
# = cd e2e && npm ci && npx playwright install chromium && npx playwright test
```

Requirements: Go 1.27, Node ≥ 22.13 for the suite (it uses `node:sqlite`; Playwright transpiles the
TypeScript). Running a fixture or `tools/wp-rate-proxy.ts` standalone (`node file.ts`) needs Node
≥ 22.18 or ≥ 23.6 (type stripping without a flag). The public theme specs still need **network access** for their CDN assets and Google Fonts. Admin dependency bundles are embedded locally; the offline-admin spec blocks every external browser request and proves editing, menus and language switching still work.

المتطلبات: Go 1.27 وNode 22.13 أو أحدث، و**اتصال بالإنترنت** لاختبارات القوالب العامة التي تحمّل أصولًا من CDN. لوحة الإدارة تستخدم مكتبات محلية مضمّنة، ويُختبر عملها مع حجب كل الطلبات الخارجية.

Useful variants:

```sh
cd e2e
npx playwright test commerce            # one spec (the setup project always runs first)
npx playwright test --headed            # watch it
npx playwright show-report              # HTML report of the last run
E2E_KEEP_DATA=1 npx playwright test     # keep the temp data dir (path printed at the end)
```

## What a run does / ماذا يحدث عند التشغيل

`global-setup.ts`:

1. builds the binary: `CGO_ENABLED=0 go build -o e2e/.bin/gorun ../cmd/gorun`;
2. starts two in-process fixture servers on 127.0.0.1 (see below);
3. spawns `gorun serve` on a free port with a temp `GORUN_DATA`, `GORUN_THEMES=../themes`,
   `GORUN_ENV=dev`, `GORUN_BASE_URL=http://127.0.0.1:<port>`, `GORUN_ALLOW_PRIVATE_NET=1` and a
   webhook secret, logs to `<data>/server.log`, and waits for `/healthz`. The server env is built
   explicitly: every `GORUN_*` and `STRIPE_*` variable of your shell is dropped, and `STRIPE_SECRET_KEY`
   is passed only when it starts with `sk_test_`;
4. tears everything down after the run (the data dir is deleted unless `E2E_KEEP_DATA=1`).

All specs share that one server and database, so the suite runs **serially** (`workers: 1`) in
five projects:

| Project | Specs | Why |
|---|---|---|
| `setup` | `global.setup.ts` | Creates the first admin through `/admin/setup` (possible once per data dir), signs in through the login form, saves `.auth/admin.json`. |
| `chromium` | CMS/API/MCP/commerce/media/migration regressions, offline admin, builder, site starters, guided profiles and readiness | The journeys, signed in as that admin. |
| `catalog` | `catalog-themes` | Activates each catalog theme in turn (design QA), then restores the default theme. |
| `theme` | `public-theme` | Activates the Business theme, which changes every public page. |
| `visual` | `visual` | Full-page screenshots of the finished site into `e2e/screenshots/` (not committed, reviewed by hand). |

A project only runs when the one before it passed.

## Environment variables / متغيرات البيئة

| Variable | Effect |
|---|---|
| `STRIPE_SECRET_KEY` | When it is a `sk_test_…` key, the commerce spec asserts a real 303 to `checkout.stripe.com` and signs the webhook for the real session id. Without it the spec asserts the "Stripe not configured" path (see below). |
| `STRIPE_WEBHOOK_SECRET` | Ignored (stripped like every `STRIPE_*`): the server always gets the local constant `whsec_e2e_local_webhook_secret_0001`, exported to the specs as `E2E_WEBHOOK_SECRET`, which signs the webhooks. |
| `E2E_KEEP_DATA=1` | Keep the temp data dir (database, media, `server.log`). |
| `E2E_DATA_DIR=<dir>` | Use this data dir instead of a temp one (kept). It must be empty: setup runs once per data dir. |
| `E2E_SKIP_BUILD=1` | Reuse `e2e/.bin/gorun` (for example a downstream build copied there). |
| `E2E_THEMES_EXTRA=<dirs>` | Passed to the server as `GORUN_THEMES_EXTRA` (extra theme roots, for a downstream suite). |
| `E2E_PORT` | Force the port (normally chosen free by `playwright.config.ts`). |

Inside the specs, `global-setup.ts` exports `E2E_BASE_URL`, `E2E_DATA`, `E2E_DB`, `E2E_LOG`,
`E2E_WP_URL`, `E2E_BILLING_URL`, `E2E_BILLING_CLIENT`, `E2E_BILLING_SECRET` and `E2E_WEBHOOK_SECRET`
(read them with `env()` from `lib/helpers.ts`).

## Fixtures / الخوادم البديلة

- **`fixtures/wp-fixture-server.ts`** — WordPress stand-in for the migration wizard. It serves the
  fictional Example Press REST answers from `internal/wpmigrate/testdata/example-press/` exactly like the Go
  helper (`internal/wpmigrate/helpers_test.go`): `routes.json` maps a canonical key (path plus the
  query sorted, without `per_page`, `_embed`, `_fields` and `page=1`) to `{file, status, headers}`;
  `https://example.org` (plain and JSON-escaped) is rewritten to the fixture URL; every
  `/wp-content/uploads/*` image answers a tiny valid PNG (gif, svg and pdf too; other types 404);
  unknown routes answer `404 rest_no_route`. Standalone: `node fixtures/wp-fixture-server.ts 18095`.
- **`fixtures/fake-billing-server.ts`** — a port of `internal/commerce/fakebilling`: the
  `mcp-billing/v1` endpoints (`/catalog`, `/accounts`, `/accounts/{id}`, `…/suspend|resume|login-link`)
  with HMAC verification per [COMMERCE.md](COMMERCE.md#signing) (checked against the golden vector),
  ±5-minute timestamps, `Idempotency-Key` replay and in-memory accounts. `GET /__calls` (unsigned,
  test-only) returns the recorded calls and accounts.
- **`tools/wp-rate-proxy.ts`** — not used by the suite: the read-only, rate-limited proxy used for
  a live rehearsal against a real WordPress site.

### The commerce fixture tweak

GoRun creates an order only after Stripe accepts the secret key, and there is no way to point the
Stripe client at a fake server. Without `STRIPE_SECRET_KEY` the commerce spec therefore:

1. asserts the 503 "not configured" answer and that no order exists;
2. saves a dummy `sk_test_…` key in **Store settings** — checkout then creates the customer and the
   order through the app, Stripe rejects the key (502 "checkout failed") and the app cancels the order;
3. flips that one order back to `pending` in SQLite (`UPDATE orders SET status='pending'`) — the only
   direct database write in the suite — and clears the dummy key;
4. delivers a `checkout.session.completed` event signed with the webhook secret
   (`t=<unix>,v1=hex(hmac_sha256(secret, "<t>.<payload>"))`, `lib/helpers.ts stripeSignature`).

Step 2 makes a real outbound request to `api.stripe.com` with the dummy key on every run without
`STRIPE_SECRET_KEY`. Offline, the request fails with a transport error instead of a 401; the app
still answers 502 and cancels the order, so the spec behaves the same.

Everything after that (paid → provisioning job → fake billing `POST /accounts` → magic link in the
server log → `/account` → "Open") is the real code path.

## Known-bug tests / اختبارات الأخطاء المعروفة

Tests named `BUG-NN …` reproduce bugs found by this suite and are marked `test.fail()`: they pass while
the bug exists and turn red ("expected to fail but passed") once it is fixed — then delete the
`test.fail` line. The list with owners is in the Task 16 report.

## Adding a spec / إضافة اختبار

1. Create `e2e/tests/<name>.spec.ts`. It runs in the `chromium` project, signed in as the admin
   (`storageState`). For an anonymous visitor use
   `browser.newContext({ storageState: { cookies: [], origins: [] } })` — a bare `newContext()`
   inherits the admin session.
2. Select elements by `name=`, `id`, `action=`, `href` or `data-*` attributes, not by copy, so the
   spec works in both locales. Switch the admin language with `setLocale(page, 'en')` and switch it
   back (the choice is stored on the user).
3. Use `test.describe.serial` when tests build on each other. Give records unique, prefixed names
   (`E2E …`): the database is shared with the other specs.
4. Public pages: assert no 500 with `expectNoServerError(page)` (it looks for the theme's
   `<!-- gorun:500 -->` marker).
5. Emails are not sent in the suite: SMTP is not configured, so the mailer logs the body to
   `server.log`; take links from it with `waitForLog(re, serverLog().length)`.
6. Read the database only for assertions (`db()` from `lib/helpers.ts`); drive changes through the UI
   or the API.
7. Pages loaded over HTMX: before typing into a freshly swapped fragment, wait until htmx has bound it
   (see the media-picker upload in `admin-setup-and-content.spec.ts`).

## Guided site acceptance

`site-profile.spec.ts` uses the actual media picker to upload/select logo and hero images, checks Arabic/English custom profiles across the three Studio themes, private full-shell previews, unchanged public settings before activation, one-service offerings, invalid-input preservation and mobile overflow. `site-readiness.spec.ts` verifies real settings corrections, direct action links, read-only content/settings/layout state, honest SMTP presence versus delivery, and anonymous/contributor denial. Both restore only the mutable settings/menu assignments of the disposable test site.

The local profile/readiness round passed **62 tests** across all projects. Final copy-only verification passed **3 focused tests**. Evidence under `dist/` is local and excluded from Git; the focused suites regenerate screenshots for human review.
