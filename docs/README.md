# GoRun documentation / توثيق GoRun

Operator and product docs for self-hosting GoRun. Start with install, then first site, then production hardening. Developer and API references sit in the same tree.

دلائل التشغيل والمنتج لاستضافة GoRun بنفسك. ابدأ بالتثبيت، ثم أول موقع، ثم تقوية الإنتاج. مراجع المطوّر وواجهات البرمجة في الشجرة نفسها.

## Install and run / التثبيت والتشغيل

| Doc | Covers |
| --- | --- |
| [GORUN-INSTALLATION.md](GORUN-INSTALLATION.md) | Fresh install: release binary, GHCR/Docker, build from source, first admin, config map, upgrade from release tags |
| [local-start.md](local-start.md) | Local trial: `gorun start`, first password, LAN preview, `gorun doctor`, local recovery |
| [AI-ASSISTED-SETUP.md](AI-ASSISTED-SETUP.md) | Install or deploy GoRun with an AI assistant that has terminal access, without exposing secrets |
| [UPGRADE.md](UPGRADE.md) | Upgrade / rollback runbook: tag → binary or GHCR/Coolify → migrate → smoke → prior artifact + backup pair |
| [SERVER-SETUP.md](SERVER-SETUP.md) | Guided Linux domain setup: verified public release, DNS, Caddy HTTPS, persistent data, safe resume and acceptance checks |
| [PRODUCTION.md](PRODUCTION.md) | Production env, HTTPS / reverse proxy / HSTS, backup + restore (CLI and admin), cutover, acceptance checks |
| [BACKUPS.md](BACKUPS.md) | Scheduled local backups, verification, failure alerts, read-only restore preview, restore into a new directory |
| [STAGING.md](STAGING.md) | Staging site (second data dir) + promote-to-live via `gorun staging seed\|promote` |
| [OBSERVABILITY.md](OBSERVABILITY.md) | Optional `GET /metrics` (Prometheus text) + JSON log shipping notes |
| [ERROR-INVENTORY.md](ERROR-INVENTORY.md) | System → Errors: read-only owner view of recent application errors by category |
| [BENCHMARKS.md](BENCHMARKS.md) | Reproducible minimal-article benchmark against WordPress and static Astro, with limits and raw evidence |
| [LOADTEST.md](LOADTEST.md) | Local HTTP load harness: p50/p95, error rate, RSS/CPU, concurrent readers (`make loadtest`) |
| [../deploy/coolify.md](../deploy/coolify.md) | Coolify Compose or GHCR image, Traefik, trusted proxies, volume, health, restore, [HTTPS acceptance checklist](../deploy/coolify.md#9-https-acceptance-checklist), upgrade |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Common boot, proxy, lock, backup, theme, upgrade, and MCP failures |

## First site and day-to-day CMS / أول موقع والاستخدام اليومي

| Doc | Covers |
| --- | --- |
| [SITE_BUILDER.md](SITE_BUILDER.md) | Starter sites (business / portfolio / magazine), draft → activate, readiness |
| [THEMES.md](THEMES.md) | Theme catalog, activate a theme, overrides, locales on the public site |
| [PATTERN-PLUGINS.md](PATTERN-PLUGINS.md) | Site layout patterns (independent of theme identity): package shape, admin picker, starters |
| [DEFAULT-THEMES.md](DEFAULT-THEMES.md) | What the five ship-with themes are for |
| [DEFAULT-THEME-EXAMPLES.md](DEFAULT-THEME-EXAMPLES.md) | How the Classic theme uses the saved site identity, with editable examples |
| [SITE-DESIGN-WORKSPACE.md](SITE-DESIGN-WORKSPACE.md) | Website → Design: build-site drafts and visual identity in one workspace |
| [THEME-DEVELOPMENT.md](THEME-DEVELOPMENT.md) | Step-by-step theme tutorial (AR/EN) |
| [THEME-CONTRACT.md](THEME-CONTRACT.md) | Catalog theme contract checked by `pkg/themekit` and `gorun theme doctor` |
| [ADMIN_WORKSPACE.md](ADMIN_WORKSPACE.md) | Bilingual admin (AR/EN, RTL), dashboard pins, sidebar |
| [SIDEBAR-QUICK-ACCESS.md](SIDEBAR-QUICK-ACCESS.md) | Personal quick-access shortcuts in the main sidebar |

## Agents and integrations / الوكلاء والتكاملات

| Doc | Covers |
| --- | --- |
| [MCP.md](MCP.md) | MCP overview for operators and agents: keys, scopes, approvals, HTTP vs stdio |
| [MANAGED-MCP-TOOLS.md](MANAGED-MCP-TOOLS.md) | Managed text agents calling explicitly selected MCP tools, with owner review of every call |
| [mcp-optional-connections.md](mcp-optional-connections.md) | Opt-in remote MCP connection records for Studio; nothing runs until configured and approved |
| [AGENT-BUNDLES.md](AGENT-BUNDLES.md) | Inline skills and version-pinned MCP references on agent keys |
| [AGENT-ROLE-TEMPLATES.md](AGENT-ROLE-TEMPLATES.md) | Inspectable bilingual role templates; a role grants no permissions |
| [AGENT-RECIPES.md](AGENT-RECIPES.md) | Editable agent recipes such as the Updates Researcher, from setup to reviewed result |
| [AGENT_TASKS.md](AGENT_TASKS.md) | Consent-bound, read-only page design reviews by an agent |
| [IDENTITY-AI.md](IDENTITY-AI.md) | Optional AI identity suggestions written to the identity draft after review |
| [AGENT-MODELS.md](AGENT-MODELS.md) | How agent models connect |
| [API.md](API.md) | REST `/api/v1` |
| [CANVASES.md](CANVASES.md) | Canvases as operational workspaces |
| [CANVAS-AI-SUGGESTIONS.md](CANVAS-AI-SUGGESTIONS.md) | Reviewed AI canvas suggestions: exact destination and context confirmed first |
| [CANVAS-INBOUND-WEBHOOKS.md](CANVAS-INBOUND-WEBHOOKS.md) | Signed inbound commands for a canvas card, disabled by default |
| [CANVAS-OPERATIONS-CONTRACT.md](CANVAS-OPERATIONS-CONTRACT.md) | Operational canvas frontend/backend contract v1 |
| [CONNECTIONS-DIRECTORY.md](CONNECTIONS-DIRECTORY.md) | Connections: saved MCP, AI and S3 profiles in one table |
| [CONNECTION-MODELS.md](CONNECTION-MODELS.md) | Loading a provider's model catalog explicitly before saving a profile |
| [CONNECTION-SETUP-HELP.md](CONNECTION-SETUP-HELP.md) | Provider presets that fill an empty endpoint only after an explicit click |
| [WEBHOOKS.md](WEBHOOKS.md) | Outbound change notifications |
| [COMMERCE.md](COMMERCE.md) | Stripe Checkout and MCP entitlements |
| [service-intake.md](service-intake.md) | Optional per-product intake fields recorded with a manual service request, not a payment |
| [native-business-workflows.md](native-business-workflows.md) | Customer accounts, persistent agents and app subscriptions without WordPress plugins |
| [metricool.md](metricool.md) | Metricool text drafts only: no scheduling, publishing or analytics |
| [MAIL-PROVIDERS.md](MAIL-PROVIDERS.md) | Settings → Mail: SMTP and Resend; saving never sends, a test email is an explicit action |

## Content, migration, storage / المحتوى والترحيل والتخزين

| Doc | Covers |
| --- | --- |
| [CONTENT_STUDIO.md](CONTENT_STUDIO.md) | Reviewable AI text/media candidates |
| [MIGRATION.md](MIGRATION.md) | WordPress REST / WXR import |
| [STORAGE.md](STORAGE.md) | Data directory layout and media |
| [INTEGRATIONS.md](INTEGRATIONS.md) | Integrations hub: Cloudflare token and discovery, bucket binding, mail test |
| [LOGIN-SECURITY.md](LOGIN-SECURITY.md) | Trusted proxies and login IP restrictions |
| [private-intake.md](private-intake.md) | Private report intake, disabled by default: a limited human-operated queue, not an emergency channel |

## Architecture and extension / البنية والتوسيع

| Doc | Covers |
| --- | --- |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Modular monolith, boot order, data dir, building on `pkg/cli` |
| [BOUNDARIES.md](BOUNDARIES.md) | Core / theme / built-in module / extension boundary map, import policy, optional-module decision |
| [MODULES.md](MODULES.md) | System → Modules catalog, switching optional modules off and on, parked jobs, core services, dependency consequences |
| [EXTENSION-INSTALLATION-METHODS.md](EXTENSION-INSTALLATION-METHODS.md) | Server directory, uploaded ZIP and legacy records; a method does not prove origin or safety |
| [EDGE.md](EDGE.md) | Experimental Edge Worker. The Deploy to Cloudflare button provisions D1, R2 and the preview; owner admin is optional; GoRun can store the primary manager key and check identity only |
| [EDGE-ADMIN.md](EDGE-ADMIN.md) | Optional Edge owner administration: drafts, review and publication with the owner key |
| [PLUGIN-DEVELOPMENT.md](PLUGIN-DEVELOPMENT.md) | Step-by-step plugin tutorial: manifest, signed HTTP hooks, install and test (AR/EN) |
| [EXTENSIONS.md](EXTENSIONS.md) | Full hosted extension manifest and HTTP bridge reference |
| [E2E.md](E2E.md) | Playwright suite (not a runtime dependency) |

## Project records / سجلات المشروع

Audit follow-ups and status records. They describe what changed and why; the guides above stay authoritative.

| Doc | Covers |
| --- | --- |
| [SITE-QUALITY-ROADMAP.md](SITE-QUALITY-ROADMAP.md) | Site quality in the core: theme contract, identity, readiness, assets, preview, theme updates, module readiness |
| [CORE-UPGRADE-ROADMAP.md](CORE-UPGRADE-ROADMAP.md) | Shared Core operations after v0.14.1: requirements and their status |
| [CONTENT-UX-REMEDIATION.md](CONTENT-UX-REMEDIATION.md) | Content and website audit follow-up (2026-10-19) |
| [ADMIN-AUDIT-WORKSPACE-FIXES.md](ADMIN-AUDIT-WORKSPACE-FIXES.md) | Workspace audit improvements |
| [ADMIN-OPERATOR-REPAIRS.md](ADMIN-OPERATOR-REPAIRS.md) | Operator audit repairs |
| [ADMIN-MIGRATION-REPAIR.md](ADMIN-MIGRATION-REPAIR.md) | Migration list audit repair |

## Also in the repository root / أيضًا في جذر المستودع

[README.md](../README.md) · [PRODUCT.md](../PRODUCT.md) · [DESIGN.md](../DESIGN.md) · [SECURITY.md](../SECURITY.md) · [CONTRIBUTING.md](../CONTRIBUTING.md) · [CHANGELOG.md](../CHANGELOG.md)
