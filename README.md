# GoRun

[![ci](https://github.com/zadstack/gorun/actions/workflows/ci.yml/badge.svg)](https://github.com/zadstack/gorun/actions/workflows/ci.yml) [![License: AGPL v3](https://img.shields.io/badge/license-AGPL--3.0-167252.svg)](LICENSE) ![Go 1.27](https://img.shields.io/badge/go-1.27-256b9b.svg)

**العربية** · [English](#english)

نظام إدارة محتوى ذاتي الاستضافة، مبني بـGo وSQLite، للنشر وإدارة الفريق والعمل مع الوكلاء عبر REST وMCP. الواجهة تدعم العربية والإنجليزية واتجاه RTL. البرنامج مفتوح المصدر بترخيص [GNU AGPL v3](LICENSE)؛ راجع أيضًا [NOTICE](NOTICE).

### البدء السريع

يحتاج البناء من المصدر إلى **Go 1.27** و`make`:

```sh
git clone https://github.com/zadstack/gorun.git
cd gorun
make build

GORUN_ENV=dev \
GORUN_DATA=./data \
GORUN_THEMES=./themes \
GORUN_ADDR=127.0.0.1:8080 \
GORUN_BASE_URL=http://127.0.0.1:8080 \
./bin/gorun serve
```

افتح **http://127.0.0.1:8080/admin/setup** لإنشاء المدير الأول، ثم **http://127.0.0.1:8080/admin**. شغّل البرنامج من جذر المستودع ليجد مجلد `themes/`.

### الإنتاج

- إعدادات التشغيل في `gorun.toml` أو متغيرات `GORUN_*`.
- استخدم `setup_token` لحماية إنشاء المدير الأول، واضبط `GORUN_ENV=prod` وعنوان الموقع الصحيح وHTTPS ومفتاح تشفير ثابتًا.
- عملية خادم واحدة لكل مجلد بيانات؛ يفرض النظام ذلك بقفل `gorun.lock` داخل مجلد البيانات، فترفض نسخة ثانية من `gorun serve` (أو `gorun mcp --jobs`) التشغيل برسالة واضحة.
- لا تحفظ كلمات المرور أو مفاتيح المزودين في المستودع.

راجع [دليل التثبيت](docs/GORUN-INSTALLATION.md) و[دليل الإنتاج](docs/PRODUCTION.md).

### القوالب

يأتي النظام بخمسة قوالب عامة:
- `default` للمدونات؛
- `business` للشركات والخدمات؛
- `magazine` للمجلات؛
- `portfolio` للأعمال الشخصية؛
- `docs` للتوثيق.

القوالب المبنية لموقع واحد تُحفظ خارج `themes/` وتُحمَّل عبر `GORUN_THEMES_EXTRA`.

### البناء فوق النواة

`cmd/gorun` غلاف رفيع حول الحزمة `pkg/cli`. يستطيع برنامج منفصل استيرادها وإضافة وحداته وأوامره عبر `cli.Option` مع الإبقاء على كل أوامر النواة. راجع [البناء فوق النواة](docs/ARCHITECTURE.md#building-on-the-core--البناء-فوق-النواة).

### التوثيق والمساهمة

- **التوثيق:** [البنية](docs/ARCHITECTURE.md)، [عقد API](docs/API.md)، [دليل MCP](docs/MCP.md)، [القوالب](docs/THEMES.md)، [الترحيل من ووردبريس](docs/MIGRATION.md).
- **المساهمة والأمان:** [المساهمة](CONTRIBUTING.md)، [الإبلاغ عن الثغرات](SECURITY.md).

**المساهمة:** نرحّب بالبلاغات والاقتراحات عبر [Issues](https://github.com/zadstack/gorun/issues). طلبات الدمج الخارجية لا تُقبل حاليًا إلى أن تُعتمد اتفاقية المساهمة؛ التفاصيل في [CONTRIBUTING.md](CONTRIBUTING.md). أبلغ عن الثغرات الأمنية بشكل خاص كما في [SECURITY.md](SECURITY.md).

التفاصيل الكاملة باللغة الإنجليزية أدناه.

## English

![GoRun — your content, your team, your agents](assets/branding/github-banner.svg)

**A self-hosted CMS for publishing websites and working with AI agents.**

Build a site, organize your content, and give agents scoped access to help your team. GoRun combines a bilingual admin, a visual page builder, operational canvases, REST APIs, and MCP in a Go application backed by SQLite.

[Quick start](#quick-start) · [Documentation](docs/) · [MCP guide](docs/MCP.md) · [License](LICENSE)

> GoRun is free and open-source software under the [GNU Affero General Public License v3.0](LICENSE).

## Why GoRun?

- **Own your deployment.** Run a Go executable with SQLite and the bundled themes. No PHP, separate database service, or frontend build pipeline is required to run the application.
- **Publish with your team.** Manage posts, pages, custom content types, media, roles, revisions, and scheduled content from one admin.
- **Give agents a defined role.** Connect through MCP, scope their permissions, and review sensitive actions before they take effect by default.
- **Keep work connected.** Use canvases to connect content, prompts, agents, and results. Outputs become new notes instead of silently replacing your work.
- **Work in both languages.** English and Arabic interfaces include RTL support, with responsive layouts for desktop and mobile.

## See the workspace

<table>
  <tr>
    <td width="50%"><img src="assets/branding/screenshots/content-local-ar.png" alt="GoRun content administration in Arabic with right-to-left layout" /></td>
    <td width="50%"><img src="assets/branding/screenshots/canvas-local-en.png" alt="GoRun canvas workspace in English" /></td>
  </tr>
  <tr>
    <td><strong>Content administration</strong><br />Structured content and a native Arabic interface.</td>
    <td><strong>A connected workspace</strong><br />Organize references, notes, and agent workflows.</td>
  </tr>
</table>

These are actual screenshots from isolated local environments, not customer data or proof of a connected provider.

## Quick start

Building from source requires **Go 1.27** and `make`. The repository specifies the Go 1.27.2 toolchain.

```sh
git clone https://github.com/zadstack/gorun.git
cd gorun
make build

GORUN_ENV=dev \
GORUN_DATA=./data \
GORUN_THEMES=./themes \
GORUN_ADDR=127.0.0.1:8080 \
GORUN_BASE_URL=http://127.0.0.1:8080 \
./bin/gorun serve
```

Open **http://127.0.0.1:8080/admin/setup** to create the first administrator. Once setup is complete, use **http://127.0.0.1:8080/admin**.

Run from the repository root so the application can find `themes/`. The admin assets are embedded in the executable; website themes are shipped separately and are required at runtime. Development mode creates its data directory and encryption key locally.

For production, configure a persistent data directory, HTTPS, the public base URL, a persistent encryption key, and a setup token. Run one server process per site data directory; GoRun enforces this with an OS lock on `<data>/gorun.lock`, and a second `gorun serve` (or `gorun mcp --jobs`) on the same directory refuses to start. Follow the [installation guide](docs/GORUN-INSTALLATION.md), [production guide](docs/PRODUCTION.md), or [Docker and Coolify guide](deploy/coolify.md).

## What you can build

| Area | Included capabilities |
| --- | --- |
| Publishing | Posts, pages, custom types and fields, taxonomies, revisions, scheduling, and trash |
| Site design | Starter sites, a section-based page builder, editable identity, menus, Go templates, child themes, and template overrides |
| Team workflows | Role-based access, scoped API keys, agent approvals, and audit records |
| Canvases | Content references, notes, operational widgets, agent runs, and reviewed schedules |
| Content studio | Reviewable text and media candidates using explicitly configured AI or MCP providers |
| Integrations | REST/OpenAPI, MCP, encrypted connection profiles, and S3-compatible media storage |
| WordPress migration | REST discovery, mapping, preview, import, validation, and WXR file imports |
| Operations | Persistent jobs, health checks, backups, redirects, SEO metadata, and sitemaps |
| Commerce | Virtual products, Stripe Checkout and entitlements for MCP access |

Provider-dependent features require valid credentials and a supported provider contract. Saving a connection or loading its model list does not prove that generation or payment works.

## Build and publish a website

Open **Appearance → Build your site** and choose a complete starter:

| Starter | Designed for | Starting pages |
| --- | --- | --- |
| `business` | Companies and service businesses | 5 |
| `portfolio` | Independent professionals and portfolios | 5 |
| `magazine` | Magazines and editorial teams | 4 |

Add your identity, copy, services or topics, and contact details. Review the resulting drafts on desktop and mobile, then explicitly publish and activate the site.

The page builder includes 12 section types, layout variants, drag-and-drop and keyboard ordering, undo/redo, autosave, and isolated previews. Identity settings control the logo, colors, fonts, and content width. Starter activation preserves existing content.

[Site builder guide](docs/SITE_BUILDER.md) · [Themes and customization](docs/THEMES.md)

### Themes

Five general themes ship in `themes/` and appear under **Appearance → Themes**:

| Theme | For |
| --- | --- |
| `default` | Blogs and general sites: a featured story, article cards with reading time, and site search |
| `business` | Companies and services: service cards, process steps, a call-to-action band and a contact footer |
| `magazine` | Magazines and editorial teams: a masthead, a lead story and text-first stories |
| `portfolio` | Independent professionals: selected work and services |
| `docs` | Documentation: grouped sections, an "on this page" list and previous/next pages |

Earlier names keep working: `minimal` now means `default`, and `studio-business`, `studio-editorial` and `studio-portfolio` mean `business`, `magazine` and `portfolio`. A site still on an earlier name moves once at startup, together with its theme settings and template overrides.

Themes built for one site can live outside `themes/` and stay out of general releases; load them as an extra directory with `GORUN_THEMES_EXTRA` (see [Themes](docs/THEMES.md)).

## Work with agents

GoRun exposes an MCP server at **`https://your-site.example/mcp`**, as well as a local stdio server through `gorun mcp`. The REST API is documented by **`/api/v1/openapi.json`**.

1. Create an **AI agent** key in **AI Agents → API keys** with only the required scopes.
2. Configure your MCP client with the endpoint and bearer key.
3. Have the agent inspect the site structure before requesting changes.
4. Review sensitive actions in **AI Agents → Approvals**.

Agent keys are restricted to MCP and cannot approve their own requests through REST. Publishing, deleting, changing settings, and editing live content require approval by default. Deliberately granting self-approval changes that policy; see the scope documentation before doing so.

### Start with a clear task

Use this as a starting prompt after connecting an agent:

```text
Inspect the site's structure, content types, and the tools available to your key.
Explain your proposed changes before writing anything.
Create new content as drafts and preserve existing content and settings.
If a tool requests approval, report its request ID and wait for the decision.
Distinguish completed changes from pending approvals and unverified integrations.
```

### Keep results on a canvas

Connect selected content and a prompt to an agent, then review the run before starting it. Access is granted to the selected canvas context; the agent's result is added as a new private note. Saving a canvas does not start a provider automatically.

Managed text agents can use a selected connection and model. External MCP agents execute through their own client; a saved model preference does not override that client's configuration. Image and video workflows use configured Studio/MCP bindings.

[MCP and permissions](docs/MCP.md) · [Agent models](docs/AGENT-MODELS.md) · [Canvas operations](docs/CANVAS-OPERATIONS-CONTRACT.md) · [Content studio](docs/CONTENT_STUDIO.md)

## Bring content from WordPress

The native migration wizard guides you through:

**Connect → Scan → Map types → Map fields → Preview → Import → Validate**

With suitable WordPress credentials, REST migration includes permitted published, draft, pending, private, and scheduled records. Anonymous migration is limited to public content. Preview is a dry run, and repeat imports use source identity to avoid duplicating content.

Migration has been tested against a real isolated WordPress/MariaDB source with **115 records**, including eight nonpublished records: state preservation, pagination, repeat imports, same-ID draft updates, authentication failures, and media handling.

Migration has explicit boundaries: plugins, themes, Elementor layouts, comments, and WooCommerce orders/customers are not transferred. Source changes replace imported destination fields; this is not a destination-edit conflict detector. Unprobed external media remains unverified.

[Migration guide](docs/MIGRATION.md)

## Build on the core

`cmd/gorun` is a thin wrapper around the importable package `pkg/cli`. A separate binary can import it, add its own modules and commands with `cli.Option`s, and keep every core command and flag. See [Building on the core](docs/ARCHITECTURE.md#building-on-the-core--البناء-فوق-النواة).

## Documentation

| Start here | Build and integrate | Operate and contribute |
| --- | --- | --- |
| [Installation](docs/GORUN-INSTALLATION.md) | [Architecture](docs/ARCHITECTURE.md) | [Production operations](docs/PRODUCTION.md) |
| [Site builder](docs/SITE_BUILDER.md) | [REST API](docs/API.md) | [Docker / Coolify](deploy/coolify.md) |
| [Themes](docs/THEMES.md) | [MCP](docs/MCP.md) | [Security](SECURITY.md) |
| [WordPress migration](docs/MIGRATION.md) | [Extensions](docs/EXTENSIONS.md) | [Contributing](CONTRIBUTING.md) |
| [Canvas workspace](docs/CANVASES.md) | [Commerce](docs/COMMERCE.md) | [Changelog](CHANGELOG.md) |

Documentation currently includes English and Arabic guides.

### Development

```sh
make build
make test
make lint
```

The application has no Node.js runtime dependency. The optional browser test suite under `e2e/` uses Node.js and Playwright; see [browser testing](docs/E2E.md).

The main source areas are `cmd/gorun/` for the executable, `pkg/cli/` for the command line, `internal/` for domain modules, `web/admin/` for embedded admin assets, and `themes/` for the general website themes.

## Contributing

Bug reports, documentation problems and feature ideas are welcome as [issues](https://github.com/zadstack/gorun/issues). **Outside pull requests are not accepted yet**, until the project adopts its contribution agreement; see [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Project status

GoRun is an actively developed v0.1 product with no tagged release yet; build from `main`. A running site or successful connection test is not acceptance of every integration.

Current boundaries include:

- One server process per SQLite data directory (enforced by `<data>/gorun.lock`); no shared-database multisite.
- No automatic core updater or complete website generation from a single AI prompt.
- Canvases are owner-scoped with explicit agent grants, not a general shared multiplayer workspace.
- Builder storage retains the current draft and last published layout, not a complete JSON layout revision history.
- Backup restoration is manual; changing to S3 does not automatically move existing local media.
- Real provider generation and live payment acceptance must be verified for the deployment's configured accounts.

## License

GoRun is free software: you can redistribute it and/or modify it under the terms of the **[GNU Affero General Public License v3.0](LICENSE)** as published by the Free Software Foundation. If you run a modified version as a network service, the AGPL requires you to offer its users the corresponding source code.

Third-party components keep their own licenses (see `web/admin/static/vendor/*.LICENSE`, `web/admin/static/fonts/zad/OFL.txt` and `internal/builder/static/THREE-LICENSE.txt`). See [NOTICE](NOTICE) for attribution.

Security reports: see [SECURITY.md](SECURITY.md).

Copyright © 2026 ZAD STACK / Ahmed Morsy.
