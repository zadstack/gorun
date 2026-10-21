# GoRun Edge: experimental Worker and optional owner administration

The standalone Cloudflare Worker in [`edge/`](../edge) has its own D1 database and private R2 binding. The public runtime has passed 44 actual HTTPS acceptance checks on a dedicated Cloudflare deployment, including site/publication isolation and immediate content/media revocation. It remains an experimental implementation of [issue #46](https://github.com/zadstack/gorun/issues/46), separate from the Full binary, with no measured benchmark. [Install on Cloudflare](#install-on-cloudflare) is the automatic path.

The default public runtime has no write endpoint. Optional [owner administration](EDGE-ADMIN.md) adds explicitly enabled draft/review/publication, revisions, audit and offline recovery. Its separate actual Cloudflare HTTPS browser acceptance passed on 19 October 2026 at source `5b18d350324351bc2ac2aad7294231f6a51fde93`; test access was then revoked and administration disabled. The [validation record](validation/edge-cloudflare-2026-10-19.json) distinguishes this from local tests and performance measurements. The Go [`pkg/edge`](../pkg/edge) package describes future site-scoped manager capabilities; this Worker does not pair with Full, run MCP tools, invoke models or provision agent skills.

## Install on Cloudflare

The experimental Edge edition installs on your Cloudflare account from this button. Cloudflare clones the isolated [`edge/`](../edge) project, creates the D1 database and private R2 bucket named in `wrangler.jsonc`, replaces the placeholder D1 id, applies migrations through the `DB` binding, loads `fixtures/demo.sql`, and deploys the Worker. You sign in to Cloudflare and confirm or rename those resources on one setup page. `SITE_ID` stays `demo` so `/readyz` and the preview posts resolve. The published `https://…workers.dev` address is the site. You manage it from outside the Full admin at `/admin` on that exact origin: set `EDGE_ADMIN_ENABLED=true`, `EDGE_ADMIN_ORIGIN` to that origin, store a random `EDGE_ADMIN_SECRET` with `wrangler secret put`, then run `npm run owner -- init --remote --config wrangler.production.jsonc --site demo` and save the owner key. This does not install the Full GoRun binary, pair a manager, or change an existing Full site.

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/zadstack/gorun/tree/main/edge)

Cloudflare reads [`edge/package.json`](../edge/package.json) and runs `npm run deploy`: `wrangler d1 migrations apply DB --remote`, then the preview fixture, then `wrangler deploy`. Repeating the deploy does not overwrite fixture rows (`INSERT OR IGNORE`). The button follows [Deploy to Cloudflare buttons](https://developers.cloudflare.com/workers/platform/deploy-buttons/). `edge/` is self-contained, which is required because Cloudflare treats that subdirectory as the new repository root. The [operator steps](#remote-deployment-and-acceptance) below remain for someone who already has Wrangler credentials and wants to create the resources before the first deploy.

## Run locally

Use Node **24.22.0**, pinned in `edge/.node-version`, with npm. From the repository root:

```sh
cd edge
npm ci
npm run db:migrate
npm run seed:local
npm run dev
```

Open the localhost URL Wrangler prints (normally `http://localhost:8787`). `/ar/` shows the synthetic Arabic fixture. The migrations create a **separate Edge schema**; do not point this at an existing Full database or import a Full SQLite file. No Cloudflare account, Go compiler or production credential is required for these local commands. The checked-in D1 UUID is a local placeholder. Deploy to Cloudflare replaces it when it provisions the database; it is not an existing remote resource.

Local CLI commands use Wrangler's persistent `.wrangler/state` inside `edge/`: rerunning migration skips applied migrations, and demo seeding uses `INSERT OR IGNORE` without overwriting existing fixture IDs. For a disposable clean environment, use a fresh checkout or copy of `edge/`, rather than removing a working site's state. Local data is not synchronized to Cloudflare. This path does not create a public deployment.

## Public read contract

| Route | Result |
|---|---|
| `/`, `/en/`, `/ar/` | Up to 20 published page/post summaries for that locale |
| `/post/<slug>`, `/page/<slug>` | A plain-text article; other locale uses `/ar/…` or `/en/…` |
| `/api/v1/content?type=post&locale=en&page=1&per_page=20` | Public summaries; no body, author, private metadata or database diagnostics |
| `/api/v1/content/post/<slug>?locale=en` | One public article, including `body_text` |
| `/media/<id>` | An explicitly public D1 media record's allowed R2 object |
| `/healthz` | Liveness only; does not check storage |
| `/readyz` | Required binding presence, configured site record and D1 schema version; does **not** probe R2 availability or prove production readiness |

Public routes accept GET and HEAD only; every other method returns 405. The separately enabled owner interface has its own protected POST contract. Missing/private/unpublished/future content returns 404. All queries bind the configured `SITE_ID`, locale and type; callers cannot select another site or status. Paging is bounded to 50 entries per page and 100 pages. Unknown or repeated query parameters are rejected. API envelopes are `{ "ok": true, "data": … }` or `{ "ok": false, "error": { "code": … } }`. These are a small POC contract, not Full REST compatibility.

All HTML values are escaped, including titles, excerpts and `body_text`; the POC deliberately does not render stored HTML, JavaScript, Markdown or Go themes. Public security headers disallow scripts, framing, forms and cross-origin resource embedding. Public responses use `Cache-Control: no-store`; there is no CDN/Cache API optimization yet, so a publication or media visibility change takes effect on repeat requests without a stale public copy. Administration has private/no-store responses and permits only same-origin forms.

## Storage boundary

`edge/migrations/0001_readonly.sql` creates `edge_sites`, `edge_content`, `edge_media` and a schema marker. Use a dedicated D1/R2 pair per deployed site. Queries additionally isolate records by `SITE_ID` as a defensive boundary; this is not a claim of a managed multi-tenant service.

Only `status='published'` content with a nonfuture `published_at` in UTC `YYYY-MM-DDTHH:MM:SSZ` is publicly readable. Bodies are bounded plain text. Private/draft data may exist in the database but is not returned publicly. There is no public import or editing operation; use explicit Cloudflare operator tooling or the separately enabled owner interface. Additive migration `0002_owner_admin.sql` stores drafts, sessions, revisions and audit separately; editing a draft preserves the existing public article.

An R2 object is readable only when the same site's D1 record explicitly says `visibility='public'`, the exact key is under `sites/<SITE_ID>/public/`, its path components are safe, its actual size matches metadata (maximum 10 MiB), and its configured MIME is one of PNG/JPEG/GIF/WebP/AVIF/plain text/PDF. HTML/SVG/JavaScript are refused. Plain text and PDFs are forced downloads. R2 object metadata is not trusted for MIME, headers or caching. There is no bucket listing, raw-key URL, redirect to storage, private object fallback, or public-bucket requirement. Keep R2's own public access disabled: otherwise direct bucket URLs would bypass this Worker.

## Tests and packaging

```sh
npm test
npm run package:check
# After the documented local migration/seed commands:
npm run smoke:local
```

Tests execute the actual compiled Worker in local `workerd` using real emulated D1 and R2, with isolated ephemeral test storage. They cover site/locale/status isolation, future/null timestamps, SQL and path attacks, HTML escaping, bounded queries, no mutation/admin routes, media namespaces/MIME/size/private/missing objects, immediate revocation, HEAD and generic readiness errors. They do not replace remote acceptance tests. The path-scoped [Edge workflow](../.github/workflows/edge.yml) repeats them on pinned Node, checks the upload bundle and exercises the documented local migration/seed commands without Cloudflare credentials.

Dependencies and the upload tool are exact versions in `package-lock.json`. Miniflare is deliberately pinned to Wrangler's own current runtime dependency (which currently has a beta version), using its exported v4-options adapter; this POC is experimental. A dry-run build validates packaging without uploading or provisioning anything. Node or dependency upgrades should repeat the integration suite.

## Remote deployment and acceptance

[Install on Cloudflare](#install-on-cloudflare) is the automatic path. These steps are the operator-run alternative: they create a new deployment in the operator's own account. The [recorded isolated deployment](validation/edge-cloudflare-2026-10-19.json) passed public reading and owner-workflow HTTPS acceptance for one dedicated deployment; it does not establish production readiness or equivalent performance for every later install. Use an existing account with Workers, D1 and R2 enabled and credentials for the selected resources. Authenticate through the supported browser/keychain or token flow; keep credentials out of issues, README and commit history.

1. Authenticate your installed Wrangler using Cloudflare's supported login/token flow; verify the intended account with `npx wrangler whoami`. Use dedicated POC resource names and review the account's quotas/billing.
2. From `edge/`, create an independent D1 database and a **private** R2 bucket. Copy the returned database ID; these commands create resources and must be run intentionally in your account:

   ```sh
   npx wrangler d1 create gorun-edge-poc
   npx wrangler r2 bucket create gorun-edge-poc
   cp wrangler.jsonc wrangler.production.jsonc
   ```

3. Edit the ignored `wrangler.production.jsonc`: pin the selected `account_id`, choose an unused Worker name, set the D1 `database_name`/`database_id` to the created database, and set the R2 `bucket_name`. Leave `SITE_ID="demo"` for the synthetic acceptance fixture. No custom-domain route, existing Worker, proxy or Full deployment is modified by these instructions.
4. Apply only the standalone Edge migrations and explicitly load the public synthetic fixture:

   ```sh
   npx wrangler d1 migrations apply DB --remote --config wrangler.production.jsonc
   npx wrangler d1 execute DB --remote --config wrangler.production.jsonc --file fixtures/demo.sql
   npx wrangler deploy --config wrangler.production.jsonc
   ```

5. At the actual HTTPS URL Wrangler reports, verify `/healthz`, `/readyz`, both language homepages, an article/API response and a bodyless HEAD. Using administrative D1/R2 tools, repeat private/draft/future/cross-site and media allowlist tests on synthetic records, including an object that is private in D1 and an object from another namespace. Confirm public POST is 405 and default-off `/admin` plus `/mcp` are 404. Confirm security/no-store headers and publication revocation. Record the commit, date, public URL, selected resources and results **without credentials**.
6. To test optional owner workflows, follow [the separate owner setup and HTTPS acceptance](EDGE-ADMIN.md). After a temporary test, revoke its owner access and sessions and return administration to disabled. Keep R2 direct public access disabled. Tear down only dedicated POC resources if finished; do not remove shared resources.

Official sources: [D1 prepared queries](https://developers.cloudflare.com/d1/worker-api/prepared-statements/), [D1 migrations](https://developers.cloudflare.com/d1/reference/migrations/), [R2 Worker bindings](https://developers.cloudflare.com/r2/api/workers/workers-api-reference/), [Wrangler configuration](https://developers.cloudflare.com/workers/wrangler/configuration/), [Workers testing](https://developers.cloudflare.com/workers/testing/).

**بالعربية:** زر Deploy to Cloudflare يثبّت نسخة Edge التجريبية على حساب Cloudflare تلقائيًا: ينشئ D1 وحاوية R2 الخاصة، ويطبّق الترحيلات، ويحمّل المعاينة، ثم ينشر الـ Worker بعد تأكيد صفحة الإعداد. الموقع يبقى على عنوانه، وإدارته من `/admin` على نفس العنوان بمفتاح المالك. هذا لا يثبّت GoRun Full ولا يربط المديرين. اجتاز تشغيل معزول على Cloudflare ببيانات تجريبية، ثم أُلغي وصول الاختبار. لا توجد قياسات أداء، ولا تمنح `/readyz` ضمان جاهزية إنتاجية أو فحصًا فعليًا لـ R2.
