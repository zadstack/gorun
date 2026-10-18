# Security policy

> **بالعربية.** الإصدار المدعوم حاليًا هو 0.1. للإبلاغ عن ثغرة لا تفتح بلاغًا عامًا: استخدم الإبلاغ الخاص عن
> الثغرات في GitHub (Security → Report a vulnerability) في مستودع المشروع، مع خطوات إعادة الإنتاج والإصدار
> المتأثر. نؤكد الاستلام ونعمل على إصلاح وننسق موعد النشر معك.

## Supported versions

| Version | Supported |
|---|---|
| 0.1.x | yes |
| older | no (pre-release builds) |

## Reporting a vulnerability

Do **not** open a public issue for a security problem. Use GitHub's private vulnerability reporting on this
repository (**Security → Report a vulnerability**) and include:

- the affected version or commit (`gorun version`);
- steps to reproduce, or a proof of concept;
- the impact you expect (data exposure, privilege escalation, remote code execution…).

If GitHub reporting is not available to you, email **info@gocloud.run**.

The maintainers acknowledge the report, work on a fix in a private branch, and agree a disclosure date with
you. Please give us a reasonable time to ship the fix before publishing details.

## Security model (summary)

**Sessions, CSRF and headers**

- Passwords are hashed with Argon2id. Login attempts are rate-limited (more than 5 per 15 minutes per IP + email are refused, counted in
  the database so lockouts survive restarts). A separate limit allows 30 attempts per IP per 15 minutes, including successful logins; at most four Argon2 login checks run concurrently across the process.
- Sessions use a random token in the `gp_session` cookie (`HttpOnly`, `SameSite=Lax`, `Secure` when the request
  is https, i.e. TLS or `X-Forwarded-Proto: https` from a peer listed in `GORUN_TRUSTED_PROXIES`, else when `GORUN_BASE_URL` is
  https; never from the `site.base_url` setting, so a wrong value cannot lock admins out); only its SHA-256 is stored. Sessions slide over 14 days.
- Forwarding headers are trusted from **no** peer by default, including loopback and private networks. Only
  addresses or networks listed in `GORUN_TRUSTED_PROXIES` (or `trusted_proxies` in `gorun.toml`) may set the
  client IP (`X-Forwarded-For`, walked right to left through trusted hops) and https (`X-Forwarded-Proto`);
  `Forwarded`, `X-Real-IP` and provider headers such as `CF-Connecting-IP` are never read. Behind an
  unlisted reverse proxy every visitor shares the proxy's address for the login limits and audit, and the
  server logs a warning (at most hourly). See [docs/LOGIN-SECURITY.md](docs/LOGIN-SECURITY.md).
- Every unsafe request through the admin and the public forms carries a CSRF token (double-submit with the
  `gp_csrf` cookie, `_csrf` field or `X-CSRF-Token` header): the public POSTs are `/forms/contact`,
  `/checkout/{slug}`, `/account/login` and `/account/open/{entitlement}`. API calls made with a session need
  `X-CSRF-Token`.
- Responses carry a Content-Security-Policy with a per-request nonce for inline scripts and
  `object-src 'none'`. Public pages allow only same-origin and nonced scripts, with no CDN and no
  `'unsafe-eval'`, unless the operator adds sources with `GORUN_CSP_SCRIPT_SRC` (https origins and
  `'unsafe-eval'` only). `/api/docs` additionally allows the one pinned, SRI-checked Scalar bundle. They use
  `frame-ancestors 'self'` with `X-Frame-Options: SAMEORIGIN`. Admin and API responses use
  `frame-ancestors 'none'` with `X-Frame-Options: DENY`. Responses also carry `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: strict-origin-when-cross-origin`, and HSTS when the request itself is https (same
  rule). In prod `GORUN_BASE_URL` is required and may not be
  localhost; the server refuses to start otherwise.
- Admin script sources are restricted to self and per-request nonces; the six exact dependency bundles and licenses are embedded locally with SRI. The admin policy keeps `'unsafe-eval'`: its Alpine directives are inline expressions that only the standard Alpine build can evaluate (the CSP build cannot).
- Read-only draft previews apply their own restrictive sandbox, with scripts and form submission blocked. They are distinct from authenticated interactive administration, which retains its session, CSRF and permission checks.
- Authors/contributors may read their own content and public published items, with no access to other users' private drafts, revisions or hidden translations. Public rendering always excludes internal content types.
- The admin is reserved for staff roles; shop customers are refused.
- **First-run setup:** `/admin/setup` creates an administrator while no user exists and is reachable without a
  login. In `prod` it requires `GORUN_SETUP_TOKEN` (at least 16 characters, compared in constant time; wrong
  tokens are audited as `user.setup_denied`), so whoever reaches a fresh deploy first cannot claim it. Without
  a token the page only explains how to proceed: set the token, or create the first admin with
  `gorun user create --admin`. In `dev`, the listener defaults to loopback and a configured token is honored; without a token local setup remains available.

**API keys**

- Keys (`gp_…`) are stored as SHA-256 hashes, shown once at creation, and can expire or be revoked. A key acts as
  its owner and never exceeds the owner's role; its scopes narrow it further. Each key has a per-minute rate limit.
- Two kinds: **REST API** keys (`api`) work on `/api` and `/mcp`; **AI agent** keys (`agent`) work only on
  `/mcp` and are refused on `/api` with `403 agent_key_not_allowed`.

**MCP approvals**

- Sensitive MCP tools (publish, unpublish, delete, settings, edits to live content, template draft creation) do not run when called: the
  call is parked for a human with the `mcp.approve` permission, then runs once as the requesting key, which must
  still be active and in scope. A key can never decide its own calls. `mcp.approve_self` bypasses approval and
  must be listed explicitly (`*` does not include it). Undecided calls expire after 24 hours.
- Draft previews are sandboxed static HTML: JavaScript, forms and external assets are blocked, even after draft creation is approved.
- Every MCP call is audited with the SHA-256 of its arguments, never the arguments themselves.

**Outbound requests (SSRF guard)**

- Media upload from a URL, extension deliveries, billing-server calls and WordPress fetches go through
  `httpx.NewSafeClient` (or `httpx.NewSafeClientOpts`, used for WordPress REST reads and media downloads, which
  may follow redirects to another host such as a CDN; every hop is checked again), which refuses loopback,
  private, link-local (including cloud metadata) and other non-public addresses at dial time, so a public name
  that resolves to a private IP is blocked too. WordPress imports are also rate-limited per source.
  `GORUN_ALLOW_PRIVATE_NET=1` lifts this for home-lab setups; leave it off in production.

**Uploads and SVG**

- Uploads are checked by content sniffing against an allowlist of MIME types, stored under generated names
  (`YYYY/MM/<id>`, never the uploaded filename) and never executed. SVG files are sanitised with an element and attribute allowlist (scripts, `foreignObject`, event
  handlers and similar are dropped) and served with `nosniff` and a sandboxing CSP.

**Secrets at rest**

- `GORUN_SECRET_KEY` (32 bytes, required when `GORUN_ENV=prod`; generated into `<data>/secret.key` with mode
  0600 in dev) signs sessions and encrypts stored secrets with AES-256-GCM: WordPress application passwords,
  billing-server secrets, extension HMAC secrets, Stripe keys saved in the admin and the SMTP password. Changing
  the key makes those values unreadable, so do not rotate it casually.
- Webhooks are verified before any processing (`Stripe-Signature`; deliveries are refused with 503 when no
  webhook secret is set). Extension deliveries are signed with HMAC-SHA256.

**Audit**

- Mutations across modules write `audit_log` rows (actor, action, target). The audit log is append-only from the
  application's point of view.

Backups include local recovery keys and configuration when present. Treat both backup files as secrets; keep external environment credentials separately. See [production recovery](docs/PRODUCTION.md).
