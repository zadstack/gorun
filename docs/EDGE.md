# GoRun Edge: read-only Worker proof of concept

This is the second milestone of [issue #46](https://github.com/zadstack/gorun/issues/46): a separate Cloudflare Worker in [`edge/`](../edge), with its own D1 database and R2 binding. It runs locally in Cloudflare's `workerd` runtime. **No real Cloudflare deployment has been verified for this milestone.** It is not a supported replacement for GoRun Full, is not included in the GoRun release binary, and has no measured benchmark result or Deploy to Cloudflare button.

The existing Go [`pkg/edge`](../pkg/edge) package defines future site-scoped manager capabilities. This Worker does not yet authenticate managers, pair with Full, run MCP tools, invoke models, or expose those capabilities. It has no admin, write API, agent, shell, webhook or publication endpoint. Edge admin/storage editing, owner recovery, revisions, audit and manager pairing remain later milestones; do not infer that local read tests implement them.

## Run locally

Use Node **24.22.0**, pinned in `edge/.node-version`, with npm. From the repository root:

```sh
cd edge
npm ci
npm run db:migrate
npm run seed:local
npm run dev
```

Open the localhost URL Wrangler prints (normally `http://localhost:8787`). `/ar/` shows the synthetic Arabic fixture. The migrations create a **separate Edge schema**; do not point this at an existing Full database or import a Full SQLite file. No Cloudflare account, Go compiler or production credential is required for these local commands. The checked-in D1 UUID is a local placeholder, not a remote resource.

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

GET and HEAD only; every other method returns 405. Missing/private/unpublished/future content returns 404. All queries bind the configured `SITE_ID`, locale and type; callers cannot select another site or status. Paging is bounded to 50 entries per page and 100 pages. Unknown or repeated query parameters are rejected. API envelopes are `{ "ok": true, "data": … }` or `{ "ok": false, "error": { "code": … } }`. These are a small POC contract, not Full REST compatibility.

All HTML values are escaped, including titles, excerpts and `body_text`; the POC deliberately does not render stored HTML, JavaScript, Markdown or Go themes. Security headers disallow scripts, framing, forms and cross-origin resource embedding. Responses use `Cache-Control: no-store`; there is no CDN/Cache API optimization yet, so a publication or media visibility change takes effect on repeat requests without a stale public copy.

## Storage boundary

`edge/migrations/0001_readonly.sql` creates `edge_sites`, `edge_content`, `edge_media` and a schema marker. Use a dedicated D1/R2 pair per deployed site. Queries additionally isolate records by `SITE_ID` as a defensive boundary; this is not a claim of a managed multi-tenant service.

Only `status='published'` content with a nonfuture `published_at` in UTC `YYYY-MM-DDTHH:MM:SSZ` is readable. Bodies are bounded plain text. Private/draft data may exist in the database but is not returned. There is no public import or editing operation; the owner currently manages data using explicit Cloudflare administrative tooling.

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

## Remote deployment: operator-run, not yet verified

These steps are an explicit acceptance path, **not evidence that a deployment has happened**. The operator needs an existing Cloudflare account with Workers, D1 and R2 enabled, and credentials for the selected account/resources. No credential was available during implementation. Do not paste tokens into issues, README, commit history or an assistant conversation.

1. Authenticate your installed Wrangler using Cloudflare's supported login/token flow; verify the intended account with `npx wrangler whoami`. Use dedicated POC resource names and review the account's quotas/billing.
2. From `edge/`, create an independent D1 database and a **private** R2 bucket. Copy the returned database ID; these commands create resources and must be run intentionally in your account:

   ```sh
   npx wrangler d1 create gorun-edge-poc
   npx wrangler r2 bucket create gorun-edge-poc
   cp wrangler.jsonc wrangler.production.jsonc
   ```

3. Edit the ignored `wrangler.production.jsonc`: choose an unused Worker name, set the D1 `database_name`/`database_id` to the created database, and set the R2 `bucket_name`. Leave `SITE_ID="demo"` for the synthetic acceptance fixture. No custom-domain route, existing Worker, proxy or Full deployment is modified by these instructions.
4. Apply only the new Edge migration and explicitly load the public synthetic fixture:

   ```sh
   npx wrangler d1 migrations apply DB --remote --config wrangler.production.jsonc
   npx wrangler d1 execute DB --remote --config wrangler.production.jsonc --file fixtures/demo.sql
   npx wrangler deploy --config wrangler.production.jsonc
   ```

5. At the actual HTTPS URL Wrangler reports, verify `/healthz`, `/readyz`, both language homepages, an article/API response and a bodyless HEAD. Using administrative D1/R2 tools, repeat private/draft/future/cross-site and media allowlist tests on synthetic records, including an object that is private in D1 and an object from another namespace. Confirm POST is 405 and `/admin`/`mcp` are 404. Confirm response security/no-store headers and publication revocation. Record the commit, date, public URL, tested resource IDs and results **without credentials**.
6. Only after those checks succeed can this milestone be called remotely deployed. Owner recovery, write workflows and production support still require later implementation and review. Tear down only the dedicated POC resources if finished; do not remove shared resources.

Official sources: [D1 prepared queries](https://developers.cloudflare.com/d1/worker-api/prepared-statements/), [D1 migrations](https://developers.cloudflare.com/d1/reference/migrations/), [R2 Worker bindings](https://developers.cloudflare.com/r2/api/workers/workers-api-reference/), [Wrangler configuration](https://developers.cloudflare.com/workers/wrangler/configuration/), [Workers testing](https://developers.cloudflare.com/workers/testing/).

**بالعربية:** هذا نموذج مستقل للقراءة فقط يعمل محليًا على `workerd` مع D1 وR2، وليس لوحة Edge أو بديلًا كاملًا لـGoRun. لا توجد تجربة نشر Cloudflare موثقة أو قياسات أداء أو زر نشر جاهز. بياناته منفصلة عن SQLite الخاص بـFull؛ لا تمنح صفحة `/readyz` ضمان جاهزية إنتاجية أو تحققًا من R2. الإدارة والكتابة وربط المديرين واستعادة المالك مراحل لاحقة.
