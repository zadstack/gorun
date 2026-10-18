# GoRun documentation / توثيق GoRun

Operator and product docs for self-hosting GoRun. Start with install, then first site, then production hardening. Developer and API references sit in the same tree.

دلائل التشغيل والمنتج لاستضافة GoRun بنفسك. ابدأ بالتثبيت، ثم أول موقع، ثم تقوية الإنتاج. مراجع المطوّر وواجهات البرمجة في الشجرة نفسها.

## Install and run / التثبيت والتشغيل

| Doc | Covers |
| --- | --- |
| [GORUN-INSTALLATION.md](GORUN-INSTALLATION.md) | Fresh install: release binary, GHCR/Docker, build from source, first admin, config map, upgrade from release tags |
| [PRODUCTION.md](PRODUCTION.md) | Production env, HTTPS / reverse proxy / HSTS, backup + restore (CLI and admin), cutover, acceptance checks |
| [../deploy/coolify.md](../deploy/coolify.md) | Coolify Compose or GHCR image, Traefik, trusted proxies, volume, health, restore |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Common boot, proxy, lock, backup, theme, and MCP failures |

## First site and day-to-day CMS / أول موقع والاستخدام اليومي

| Doc | Covers |
| --- | --- |
| [SITE_BUILDER.md](SITE_BUILDER.md) | Starter sites (business / portfolio / magazine), draft → activate, readiness |
| [THEMES.md](THEMES.md) | Theme catalog, activate a theme, overrides, locales on the public site |
| [DEFAULT-THEMES.md](DEFAULT-THEMES.md) | What the five ship-with themes are for |
| [ADMIN_WORKSPACE.md](ADMIN_WORKSPACE.md) | Bilingual admin (AR/EN, RTL), dashboard pins, sidebar |

## Agents and integrations / الوكلاء والتكاملات

| Doc | Covers |
| --- | --- |
| [MCP.md](MCP.md) | MCP overview for operators and agents: keys, scopes, approvals, HTTP vs stdio |
| [AGENT-MODELS.md](AGENT-MODELS.md) | How agent models connect |
| [API.md](API.md) | REST `/api/v1` |
| [CANVASES.md](CANVASES.md) | Canvases as operational workspaces |
| [WEBHOOKS.md](WEBHOOKS.md) | Outbound change notifications |
| [COMMERCE.md](COMMERCE.md) | Stripe Checkout and MCP entitlements |

## Content, migration, storage / المحتوى والترحيل والتخزين

| Doc | Covers |
| --- | --- |
| [CONTENT_STUDIO.md](CONTENT_STUDIO.md) | Reviewable AI text/media candidates |
| [MIGRATION.md](MIGRATION.md) | WordPress REST / WXR import |
| [STORAGE.md](STORAGE.md) | Data directory layout and media |
| [LOGIN-SECURITY.md](LOGIN-SECURITY.md) | Trusted proxies and login IP restrictions |

## Architecture and extension / البنية والتوسيع

| Doc | Covers |
| --- | --- |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Modular monolith, boot order, data dir, building on `pkg/cli` |
| [EXTENSIONS.md](EXTENSIONS.md) | Hosted extension packages |
| [E2E.md](E2E.md) | Playwright suite (not a runtime dependency) |

## Also in the repository root / أيضًا في جذر المستودع

[README.md](../README.md) · [PRODUCT.md](../PRODUCT.md) · [DESIGN.md](../DESIGN.md) · [SECURITY.md](../SECURITY.md) · [CONTRIBUTING.md](../CONTRIBUTING.md) · [CHANGELOG.md](../CHANGELOG.md)
