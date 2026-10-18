# Troubleshooting / استكشاف الأعطال

Short fixes for common self-host failures. For full procedures see [installation](GORUN-INSTALLATION.md), [production](PRODUCTION.md), and [Coolify](../deploy/coolify.md).

إصلاحات مختصرة لأعطال الاستضافة الشائعة. للإجراءات الكاملة راجع [التثبيت](GORUN-INSTALLATION.md) و[الإنتاج](PRODUCTION.md) و[Coolify](../deploy/coolify.md).

## Boot and process

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| `data directory is in use by another GoRun process` | Second `serve` / `mcp --jobs` / `reindex` on the same `GORUN_DATA` | Stop the holder named in the message. On Docker/Coolify: stop-then-start (no overlapping replicas). Do not delete `gorun.lock` while a process is alive; the OS releases it on exit. |
| Prod refuses to start: empty or loopback `GORUN_BASE_URL` | `GORUN_ENV=prod` requires a public base URL | Set `GORUN_BASE_URL=https://your-site.example` (no trailing slash). |
| Prod refuses: missing / invalid `GORUN_SECRET_KEY` | Key must decode to 32 bytes (Base64) | `openssl rand -base64 32`. Keep the same value forever for that data dir. |
| `/admin/setup` only shows instructions | No `GORUN_SETUP_TOKEN`, or an admin already exists | Set a token ≥16 chars for web setup, or `gorun user create --admin …`. After the first admin exists, setup is closed. |
| Encrypted settings unreadable after restore / redeploy | New secret key | Restore the **original** `GORUN_SECRET_KEY` / `secret.key`. Generating a new key does not decrypt old rows. |

## HTTPS, proxy, HSTS

Operator acceptance recipe (CIDR, cookie Secure, HSTS, outside health):
[deploy/coolify.md §9](../deploy/coolify.md#9-https-acceptance-checklist).

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| Every login attempt hits the shared IP limit; audit shows `10.x` / `172.x` | Empty `GORUN_TRUSTED_PROXIES` behind Traefik/Caddy/nginx/Coolify | Set the proxy IP or Docker network only. Verify with **AI Agents → Audit** (your public IP). Never `0.0.0.0/0`. Details: [LOGIN-SECURITY.md](LOGIN-SECURITY.md). |
| Log: `forwarding headers from an untrusted peer` | Proxy not listed (or wrong CIDR) | Inspect the shared Docker network / Traefik IP; set `GORUN_TRUSTED_PROXIES`; redeploy stop-then-start. |
| No `Strict-Transport-Security` on `curl -sSI https://…/healthz` | Peer not trusted, so `X-Forwarded-Proto` is ignored | Fix §A in the Coolify checklist. Cookies may still be `Secure` via `GORUN_BASE_URL=https://…` — that alone does not prove HSTS/IP trust. |
| Mixed content / wrong scheme cookies | Proxy not sending `X-Forwarded-Proto`, or peer not trusted, and `GORUN_BASE_URL` is still `http://…` | Public `GORUN_BASE_URL=https://…`; fix Traefik/Caddy headers; list the proxy in `GORUN_TRUSTED_PROXIES`. |
| `includeSubDomains` breaks a subdomain | Opt-in HSTS covers every subdomain of the host | Leave `GORUN_HSTS_INCLUDE_SUBDOMAINS` unset until every subdomain serves HTTPS. Preload (`GORUN_HSTS_PRELOAD`) requires that flag and is hard to undo. |
| Health green but site unreachable publicly | In-container `gorun healthcheck` only probes localhost | Probe `https://<public-host>/healthz` and `/readyz` from outside. Check Coolify/Traefik domain, TLS cert, and that port `8080` is only exposed to the proxy. |
| `/metrics` returns 404 | `GORUN_METRICS` unset / false | Opt in with `GORUN_METRICS=true`. See [OBSERVABILITY.md](OBSERVABILITY.md). |
| `/metrics` returns 401 | `GORUN_METRICS_TOKEN` set; scrape missing Bearer | Send `Authorization: Bearer …` or `X-Metrics-Token`. |

## Themes and first site

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| Public site looks unstyled / missing templates | Binary run without adjacent `themes/`, or wrong `GORUN_THEMES` | Run from the release extract directory, or set `GORUN_THEMES` to an absolute path. Docker images embed themes. |
| Theme switch did not create starter pages | Activating a theme only changes rendering | Use **Appearance → Build your site** for drafts, then **Publish and activate**. See [SITE_BUILDER.md](SITE_BUILDER.md). |
| Boot stops on theme migration conflict | Override path exists in both old and new theme with different content | Resolve the conflicting override under `<data>/overrides/` (or keep the legacy theme installed) before upgrading. |

## Backup and restore

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| Inspect succeeds with `review_required` | Warnings in the pair (not a hard fail) | Read the bilingual codes on the inspect page; fix or accept before restore. |
| Leftover `.<stamp>.partial` under `restores/` | Interrupted admin restore | Delete the partial directory, then retry. It blocks the next restore until removed. |
| Restored site cannot decrypt settings | Secrets were outside the archive | Copy operator-held `secret.key` / set original `GORUN_SECRET_KEY` and `gorun.toml` into the restore dir. Default backups omit them unless `gorun backup --include-secrets`. |
| Tried to point `--data` at `<data>/restores/<stamp>/` while live | Restore dir is inside the live volume | Move it **out** of the live data directory, stop the old process, then `serve --data` the moved copy. |
| In-app schedule never creates backups | Hours = 0, or process without job workers | Set hours on **System → Backups**, or use host cron: `gorun backup --retain N` / `docker exec … /app/gorun backup --retain N`. Schedule does not upload to S3. |
| Local backups disappeared unexpectedly | Retain-N after schedule or CLI `--retain` | Raise retain, or set retain to `0` (no prune). Manual **Back up now** does not prune. |

## MCP and agents

| Symptom | Likely cause | What to do |
| --- | --- | --- |
| Agent key gets `403 agent_key_not_allowed` on `/api` | AI agent keys are MCP-only | Use MCP (`/mcp` or `gorun mcp`). Issue a separate REST key for `/api/v1`. |
| Sensitive tool returns an approval id and waits | Default human-in-the-loop | Approve or deny under **AI Agents → Approvals**. Agent polls `check_approval`. |
| Key cannot approve its own call | Same-owner / self-approval rules | Use a different human session, or an explicit `mcp.approve` / `mcp.approve_self` scope; `*` does not grant those. Single-operator opt-in: `GORUN_MCP_SAME_OWNER_APPROVAL=true`. See [MCP.md](MCP.md). |

## Upgrade from a release tag

If the new binary/image fails after a tag bump:

1. Stop the new process (release the data-dir lock).
2. Run the **previous** release binary/image against a **restored** data directory (or the pre-upgrade copy you kept), with the **same** secret key.
3. Do not expect rolling back the binary alone to undo SQLite migrations already applied.

Full runbook (smoke + rollback drill): [UPGRADE.md](UPGRADE.md). Summaries:
[installation — upgrades](GORUN-INSTALLATION.md#upgrades-from-release-tags--الترقية-من-وسوم-الإصدار),
[production](PRODUCTION.md).

## العربية — أين تبدأ؟

- القفل على مجلد البيانات → أوقف العملية المذكورة؛ لا تحذف `gorun.lock` أثناء التشغيل.
- حدود الدخول المشتركة خلف الوكيل / غياب HSTS → اضبط `GORUN_TRUSTED_PROXIES` ونفّذ [قائمة قبول HTTPS](../deploy/coolify.md#9-https-acceptance-checklist).
- الصحة خضراء داخل الحاوية والموقع غير متاح → افحص `https://…/healthz` من الخارج.
- الاستعادة → مجلد جديد فقط؛ انقل `restores/<stamp>/` خارج المجلد الحي قبل `serve`.
- المفتاح الجديد بعد الاستعادة → استخدم المفتاح الأصلي دائمًا.
