# Optional Edge owner administration

This is a miniature of the GoRun content workspace for the standalone Edge Worker: the same sidebar, content list, editor, review, and audit surfaces, without the Full modules. It is not the Full dashboard: no agents, uploads, arbitrary HTML, provider execution or automatic Skills/MCP provisioning. Public reading remains available with administration disabled. Both public reading and the separate owner workflow have passed real Cloudflare HTTPS checks with synthetic data: [source, scope and cleanup record](validation/edge-cloudflare-2026-10-19.json). Test owner access and sessions were revoked, its signing secret removed and the public preview returned to administration disabled. Repeat acceptance for your own account before relying on a deployment.

## Local installation

Use Node 24.22.0 and the locked dependencies. From `edge/`:

```sh
npm ci
npm exec wrangler d1 migrations apply DB --local
npm exec wrangler d1 execute DB --local --file fixtures/demo.sql
npm run owner -- init --local --origin http://localhost:8787
npm run dev
```

Save the generated owner access key privately: it is shown once, only after the database insert succeeds. The tool creates a private `.dev.vars` file without overwriting one. Open `http://localhost:8787/admin`. For a different local port or hostname, use the exact same origin when initializing and starting Wrangler. Local HTTP is explicitly limited to loopback; remote administration requires HTTPS.

`npm run browser:local` exercises the actual owner CLI, disposable Wrangler database, HTTP server and Chromium browser without accessing an existing installation or Cloudflare account. Install Chromium first with `npx playwright install chromium`. `npm test` additionally runs the real workerd/D1/R2 isolation and concurrency suite. CI runs both.

## Owner and recovery

There is no public bootstrap, password reset or recovery URL. Owner setup and recovery require the operator's offline database/Cloudflare access. The access key contains 256 random bits; only its site-bound hash is stored. Keep database access and the private configuration secret separate from content exports.

```sh
npm run owner -- recover --local
npm run owner -- revoke --local
```

Recovery issues a new key, increments the authority epoch and revokes every session atomically while preserving all content. `revoke` revokes sessions only: the existing owner key can still sign in. Concurrent owner changes fail without issuing a credential. To deactivate the owner completely, an operator must atomically set `active=0` and increment `auth_epoch` in the selected site's owner row; recovery explicitly reactivates that owner.

## Editing and publication

The compact English/Arabic editor keeps drafts separate from the public article. Saving a draft does not change the published body. Every save checks both draft and published versions; a stale tab receives a conflict instead of silently replacing another edit. Review displays the exact escaped text and requires a separate approval before publication. Editing or owner recovery invalidates the previous review. Publication requires authentication within the last ten minutes and review within two minutes.

Unpublish immediately removes the item from public routes. Discard removes only the selected draft. Saved revisions can be restored as a new draft, with the same concurrency checks and subsequent review requirement. Audit and revision records are appended in the same D1 transaction as their successful operation. Authentication expiry or a failed version check cannot leave a success audit/revision behind.

Bodies are plain text, escaped on both public and administration pages. Media administration lists metadata only; it cannot upload or download private objects. Existing public R2 access still requires the fixed site's public namespace, published media metadata, permitted MIME type and size limit.

## Security boundary and limits

Administration requires all of `EDGE_ADMIN_ENABLED=true`, an active owner, an exact `EDGE_ADMIN_ORIGIN` and a random `EDGE_ADMIN_SECRET` (43–86 base64url characters). Never publish the signing secret. The default is off. An arbitrary Host or forwarded header cannot change the permitted origin. Remote cookies use Secure, HttpOnly, SameSite=Strict and the `__Host-` prefix. Loopback-only local testing uses non-Secure `edge_` cookies through explicit `EDGE_ADMIN_LOCAL=true`.

Login uses an expiring, signed cookie/form challenge and rejects replay. Failed-key accounting is durable and bounded to one row per site; repeated failures receive 429, while the valid generated owner key remains usable. Sessions expire after one hour, rotate at login and are bounded to twenty per site. Mutating requests require same-origin POST and the session CSRF token. Every database mutation rechecks the current owner epoch, live session and expiry inside the transaction. Owner recovery during an in-flight edit therefore revokes its authority. Default D1 binding queries use the primary database; this implementation does not enable read replication or a potentially stale session read path.

Exports contain only content, drafts and revisions, never owner keys, sessions, login challenges or rate state. Each export section is limited to 1,000 records and reports those limits: this is a bounded content export, not a complete database backup. Use D1's operator backup/export tools for full disaster recovery. Lists are also bounded; there is no bulk import, media upload or multi-user role administration in this milestone. HEAD requests to administration return 405. Public GET/HEAD behavior is unchanged.

## Explicit remote acceptance

Select a dedicated account, Worker, D1 and private R2 bucket according to [the Edge guide](EDGE.md). Do not point an experiment at an existing production database. Apply additive migration `0002_owner_admin.sql` before enabling administration. The owner tool requires an explicit configuration for remote operations:

```sh
npm run owner -- init --remote --config wrangler.production.jsonc --site demo
```

Remote reads use Wrangler's query protocol, while credential-hash mutations use private SQL files. Import progress/metadata is never interpreted as query rows; the tool privately verifies the resulting epoch and generated key hash before issuing a credential. This only provisions the selected site's owner. It does not deploy or enable administration. Privately configure a generated `EDGE_ADMIN_SECRET`, `EDGE_ADMIN_ENABLED=true` and the exact public HTTPS `EDGE_ADMIN_ORIGIN`; omit `EDGE_ADMIN_LOCAL`. Set the secret through Wrangler's secret input, never a committed variable or command argument. Deploy only after reviewing the selected account/resources. Verify `/admin/login`, secure cookie flags, login, private draft, stale save, exact review/publication, Arabic mobile, export isolation, unpublish, recovery/session revocation and logout over actual HTTPS.

An opt-in browser helper accepts credentials only through a private mode-0600 file:

```sh
EDGE_ADMIN_TEST_URL=https://your-dedicated-worker.example \
EDGE_ADMIN_TEST_KEY_FILE=/private/path/owner-key npm run browser:remote
```

It creates synthetic content, checks draft/publication/unpublish boundaries and logs out. The local fixture additionally tests offline recovery. The helper records no traces, HAR, videos, screenshots or credential-bearing storage files. Never attach owner keys, session cookies, hidden token DOM or configuration secrets to reports. After a temporary acceptance test, disable administration and deactivate the test owner with an epoch increment; keep the public demo read-only. A successful local test is not proof of remote deployment, remote recovery or production readiness.
