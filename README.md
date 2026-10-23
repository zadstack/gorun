# GoRun

[![CI](https://github.com/zadstack/gorun/actions/workflows/ci.yml/badge.svg)](https://github.com/zadstack/gorun/actions/workflows/ci.yml) [![Latest release](https://img.shields.io/github/v/release/zadstack/gorun?label=release&color=167252)](https://github.com/zadstack/gorun/releases/latest) [![License: AGPL v3](https://img.shields.io/badge/license-AGPL--3.0-167252.svg)](LICENSE)

![GoRun: your content and infrastructure, with one Go binary and SQLite connecting content, themes and agents](assets/branding/readme-hero.svg)

**A self-hosted CMS for your team and AI agents.** One Go binary, SQLite, and an English and Arabic admin with RTL.

Write and publish content, shape your site with themes, and connect agents over MCP. Scoped keys, human approval for sensitive actions and an audit trail keep access explicit. REST serves other integrations; external providers are optional.

[**Quick start**](#run-it-now) · [Why GoRun](#why-gorun) · [Documentation](docs/README.md) · [Releases](https://github.com/zadstack/gorun/releases) · [العربية](README.ar.md)

[Website guides](https://gorun.gocloud.run/gorun-guides) · [Security](SECURITY.md) · [Contribute](CONTRIBUTING.md)


## Application preview

![Actual application interface](docs/media/readme-preview.png)

![Tour of actual application screens](docs/media/readme-tour.gif)

Content list and post editor, using the existing Atlas Studio demonstration screenshots. This GIF is a slideshow of actual UI captures, not a new live-session recording.

[Media provenance](docs/media/README.md)

## Run it now

For a local trial on **macOS or Linux**, copy and paste this whole block. No Go, Docker, database setup or administrator privileges required.

```sh
(
  set -eu
  installer="$(mktemp)"
  trap 'rm -f "$installer"' EXIT
  curl -fsSL https://raw.githubusercontent.com/zadstack/gorun/main/install.sh -o "$installer"
  sh "$installer"
) &&
export PATH="$HOME/.local/bin:$PATH" &&
gorun start --data "$HOME/.local/share/gorun/site"
```

The installer detects Intel or ARM, downloads the latest release, verifies its SHA-256 checksum, and installs GoRun for your user. The block configures this terminal automatically, and the installer adds the default executable directory to your shell startup file for future terminals. No manual PATH setup. Your browser opens, SQLite and the default theme are prepared, and your first `admin` password appears **once** in the terminal. Save it. On first sign-in, choose your own password, email, site name and language. Restarting preserves your site and accounts.

The quick start requires **GoRun 0.4.0 or newer**. The installer refuses older releases without changing an existing installation.

The executable lives at `~/.local/bin/gorun`; your local site lives at `~/.local/share/gorun/site`. Stop the server with **Ctrl+C**. Repeating the block uses the same site. LAN access is a public, read-only preview; administration stays on localhost. Use `--local-only` when starting manually to disable LAN access. [Local setup and recovery](docs/local-start.md).

For a domain with HTTPS and a persistent service, use the [guided Linux server setup](docs/SERVER-SETUP.md). It asks for your domain and installation directory, verifies DNS, and reports the final URL and data path after acceptance checks. It requires Linux root access, Python 3 and Docker Compose; it uses verified public release downloads without a GHCR login. For an existing proxy or Coolify deployment, follow the [Docker / Coolify guide](deploy/coolify.md). The local preview is intended for development.

## Start with your AI assistant

On your computer or a server, copy this into an assistant with terminal access to the target machine:

```text
Set up GoRun from https://github.com/zadstack/gorun.
Read the official README and assistant setup guide first.
Ask whether this is a local trial or a public server deployment.
Check the operating system, architecture, and any existing site before changing anything.
Preserve existing data, users, themes, settings, and credentials.
For a local trial, use the official release installer with checksum verification.
For a server, ask for the domain and installation directory; verify DNS points to it.
Follow the supported production setup, with HTTPS, persistent data, and automatic restart.
Do not replace an existing site or proxy configuration without explicit approval.
Verify the public page and admin sign-in page at the final URL before reporting success.
Keep generated passwords, setup tokens, and keys out of chat, screenshots, and shared logs.
Let me complete first-login setup securely in the browser.
Report the final URL, installation and data paths, and stop/restart/update instructions.
If you lack terminal access or a prerequisite, explain exactly what is needed.
```

The instructions are tool-neutral. A chat-only assistant can explain the steps but cannot install software on your computer. [Assistant setup guide](docs/AI-ASSISTED-SETUP.md).

## Cloudflare Edge — in development

GoRun Edge is a separate, experimental Workers-based edition with its own D1/R2 storage. The button below installs it on your Cloudflare account automatically: Cloudflare clones `edge/`, creates the D1 database and private R2 bucket, applies the Edge migrations, loads the synthetic preview, and deploys the Worker after you confirm the setup page. Leave `SITE_ID` as `demo`. The published address is the site: open `/admin` on that same origin and manage drafts, review and publication with the owner key. This does not install the Full GoRun binary. Both [public reading](docs/EDGE.md) and optional [owner administration](docs/EDGE-ADMIN.md) have passed actual Cloudflare HTTPS acceptance with isolated synthetic data; temporary test access was revoked. [Validation record](docs/validation/edge-cloudflare-2026-10-19.json). Create the primary manager key in the Edge admin and paste it into GoRun at Settings → Edge. GoRun stores the key encrypted and checks the Edge identity endpoint. That check does not edit Edge content. Edge has no MCP server or agent skills yet. There is no measured benchmark result.

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/zadstack/gorun/tree/main/edge)

## Why GoRun?

- **Your content and infrastructure.** A Go executable and SQLite, hosted by you. No Node.js or PHP at runtime.
- **Arabic and English from the start.** A bilingual dashboard with native RTL support and responsive site themes.
- **A complete editorial workflow.** Structured content, drafts, reviews, scheduled publishing, revision history and explicit staff permissions.
- **Agents with boundaries.** MCP tools, scoped agent keys, human approvals for sensitive changes and an audit trail. Separate REST keys serve other integrations. Canvas boards stay private; agents and REST keys read only what they are granted.

## How GoRun works

![How GoRun fits together: visitors, staff, integrations, AI agents and providers reach one gorun process through the public site, the admin, REST, MCP and webhooks. Modules boot in a fixed order on shared kernel services over one SQLite database.](assets/branding/architecture.svg)

- **One process, one database.** The `gorun` binary serves the public site, the admin, REST and MCP. Content, users, settings, the audit log and the job queue live in one SQLite database inside your data directory.
- **Features are modules.** Each module boots in a fixed order and may only use modules that boot before it. Optional modules, such as commerce or the WordPress importer, switch on or off in **Operations → Modules** at the next start. Their data stays.
- **Slow work runs in the background.** Provider checks, heavy reads and webhook deliveries run as durable jobs with retries. Pages show the last saved result and its age instead of waiting.
- **Outside services are optional.** AI providers, Stripe, SMTP mail and Cloudflare Edge connect only when you configure them.

[Architecture reference](docs/ARCHITECTURE.md) · [Modules](docs/MODULES.md)

## A workspace that stays organized

Up to six primary sections keep related tools together, with contextual page tabs above the screen you are using. You see the tools your permissions allow; Store adds an optional seventh section when permitted commerce tools are available. Colored categories keep their text labels and selected borders. Personal visibility, navigation order and dashboard pins stay yours; pinned tools remain within reach under **Quick access** in the main sidebar.

<table>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/admin-navigation-en.png"><img src="assets/branding/screenshots/admin-navigation-en.png" alt="GoRun content workspace with grouped sections, contextual page tabs and the agents rail" /></a><br /><sub>Grouped sections and page tabs</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/admin-navigation-ar.png"><img src="assets/branding/screenshots/admin-navigation-ar.png" alt="The same workspace in Arabic, mirrored right to left" /></a><br /><sub>The same workspace in Arabic, right to left</sub></td>
  </tr>
</table>

The screenshots use an isolated demo site with fictional content. The layout mirrors for Arabic and stays usable on mobile. [Arabic mobile menu](assets/branding/screenshots/admin-navigation-ar-mobile.png) · [Workspace guide](docs/ADMIN_WORKSPACE.md).

## Measured, with context

In a controlled minimal article test at **50 requests/s**, median run p95 latency was **2.66 ms for GoRun**, **16.85 ms for WordPress + MariaDB**, and **0.66 ms for Astro static + Nginx**. Sampled peak working set was **114.54 MiB**, **203.78 MiB**, and **4.09 MiB**, respectively.

Three repetitions on one disposable host; two logical server CPUs and a 2 GiB budget per deployment; 100 identical synthetic articles; no TLS, CDN or full-page cache. Astro is a static-delivery reference, not a full CMS. **The experimental Edge prototype has no measured result.** These numbers describe this workload, not general product superiority. [Full results, limitations, raw evidence and reproducible notebook](docs/BENCHMARKS.md).

## Write, review and publish

Manage posts, pages, custom content types, fields, media and navigation in one place. Give editors, authors and contributors the permissions they need, and review changes before publishing.

<table>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/content-list-ar.png"><img src="assets/branding/screenshots/content-list-ar.png" alt="Arabic content list with published posts and drafts in Arabic and English" /></a><br /><sub>Content list in Arabic</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/editor-en.png"><img src="assets/branding/screenshots/editor-en.png" alt="Post editor with body, excerpt, featured image and publishing panel" /></a><br /><sub>Editor with scheduling and revisions</sub></td>
  </tr>
</table>

[Editorial workflow](docs/README.md) · [WordPress migration](docs/MIGRATION.md)

## Build a site you can make your own

### Themes vs patterns

**Themes** set visual identity (type, color, spacing, chrome). **Patterns** set page layouts. They are independent: pick a theme under **Appearance → Themes**, a layout under **Appearance → Patterns**. Template resolution is `draft → override → pattern → theme`. Clearing a pattern restores the theme’s own templates.

| Theme | Slug | Role |
| --- | --- | --- |
| Classic | `default` | Calm reading |
| Modern | `business` | Sharp chrome |
| Minimal | `portfolio` | Quiet chrome |
| Serif | `magazine` | Type-led |
| Docs | `docs` | Reference chrome |

| Pattern | Slug | Layout |
| --- | --- | --- |
| Editorial | `editorial` | Magazine masthead, lead, story grid |
| Company | `company` | Business hero, offerings, updates |
| Showcase | `showcase` | Portfolio intro, project mosaic |
| Guidebook | `guidebook` | Docs search hero, section cards |

Activating a theme does not create starter pages; use the site builder for that. Patterns live under `patterns/` (not HTTP extensions). [Themes](docs/THEMES.md) · [Pattern plugins](docs/PATTERN-PLUGINS.md) · [Default-theme demo](assets/branding/screenshots/theme-home-en.png) · [Arabic article on mobile](assets/branding/screenshots/theme-post-ar-mobile.png).

**Website → Design → Site design** brings **Build site** and **Visual identity** into one workspace. Prepare pages as drafts, review activation separately, and preview colors and typography locally before explicitly saving identity. Existing site content and theme settings remain yours. [Site design guide](docs/SITE-DESIGN-WORKSPACE.md).

<table>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/site-design-identity-en.png"><img src="assets/branding/screenshots/site-design-identity-en.png" alt="Site design workspace with saved-site summary, visual identity controls and a local sample" /></a><br /><sub>Visual identity, previewed before saving</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/site-design-build-en.png"><img src="assets/branding/screenshots/site-design-build-en.png" alt="Build-site step with starter designs prepared as drafts" /></a><br /><sub>Build site: starters prepared as drafts</sub></td>
  </tr>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/builder-ar.png"><img src="assets/branding/screenshots/builder-ar.png" alt="Page builder with an Arabic interface and live preview" /></a><br /><sub>Page builder in Arabic with live preview</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/theme-home-en.png"><img src="assets/branding/screenshots/theme-home-en.png" alt="Public home page of the default theme with fictional Atlas Studio content" /></a><br /><sub>The public site in the default theme</sub></td>
  </tr>
</table>

[Arabic mobile identity](assets/branding/screenshots/site-design-identity-ar-mobile.png).

[Site builder](docs/SITE_BUILDER.md) · [Theme development](docs/THEME-DEVELOPMENT.md) · [Theme reference](docs/THEMES.md)

## Work with agents, keep the decisions

Connect an MCP client with an **AI agent** key. Its scopes are bounded by its owner's permissions; publishing, deleting and changing sensitive settings require human approval by default. REST integration keys are separate from agent keys.

### How an agent action gets approved

![How an agent action gets approved: the agent calls an MCP tool, GoRun checks scopes and arguments, a sensitive tool waits for a person under Canvas & agents → Approvals, approval runs it once, denial or 24 hours without a decision runs nothing, and every step is audited.](assets/branding/approval-journey.svg)

1. The agent calls an MCP tool with its **AI agent** key. GoRun checks that the key's scopes cover the tool and that the arguments match the tool's schema.
2. A tool that is not sensitive runs at once. A sensitive one, such as publishing, deleting, editing live content or changing settings, waits and the agent receives an approval ID.
3. A person reviews the exact tool, arguments, key and requester under **Canvas & agents → Approvals**. A key never decides its own request.
4. Approval runs the call once, as the requesting key, if that key is still active and its scopes still cover the tool. Denial runs nothing. An undecided request expires after 24 hours.
5. The agent reads the outcome with `check_approval`. The audit log records every call and decision, with a hash of the arguments instead of the arguments.

[How approvals work](docs/MCP.md#human-approval-for-sensitive-tools)

<table>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/agent-keys-en.png"><img src="assets/branding/screenshots/agent-keys-en.png" alt="API keys page with agent keys and a REST key, each with explicit scopes" /></a><br /><sub>Agent keys with explicit scopes</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/approvals-ar.png"><img src="assets/branding/screenshots/approvals-ar.png" alt="A pending publishing approval waiting for review, in Arabic" /></a><br /><sub>A pending approval waiting for a person</sub></td>
  </tr>
</table>

Canvases bring selected content, instructions and agent results into one workspace. Results become private notes for review and do not publish automatically. Agents need `canvas.read` plus an explicit board grant to list or read boards over MCP; REST keys use the matching canvas endpoints. Configure your provider or agent connection before running it.

![Selected content and instructions go to a connected agent; its result becomes a private note for your review, with publication kept separate](assets/branding/reviewed-workflow.svg)

With a configured AI provider, review the exact destination and site context before separately confirming a request for canvas suggestions. Suggestions create an unbound starting point; choose its executor and permissions yourself. [AI suggestion workflow](docs/CANVAS-AI-SUGGESTIONS.md).

<a href="assets/branding/screenshots/canvas-ai-suggestions-en.png"><img src="assets/branding/screenshots/canvas-ai-suggestions-en.png" alt="Three canvas suggestions after explicit review and confirmation" width="49%" /></a>

<sub>Canvas suggestions after review and confirmation</sub>

[Arabic suggestions on mobile](assets/branding/screenshots/canvas-ai-suggestions-ar-mobile.png). These documentation examples come from a scripted local HTTP provider, not a live model or evidence of provider availability.

Each agent or provider card shows its own ID and selected agent/provider identity, with a signed webhook matched to a saved command and reviewed instructions. Incoming-event rules start disabled and require explicit review and enablement; external agents still need their MCP client running. [Configure canvas events](docs/CANVASES.md#incoming-events-on-agent-and-provider-cards).

### Your saved actions, within reach

The opposite-side agents rail opens your own canvas cards. Inspect **Integrations**, **Instructions** and **Defaults**, then open the original card to configure or review it. The drawer reads saved configuration; opening it does not run an agent. **Configured** does not mean online, and an external agent still needs its connected client.

<table>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/agents-drawer-en.png"><img src="assets/branding/screenshots/agents-drawer-en.png" alt="Agent drawer showing a named external campaign assistant, configured status and read-only details" /></a><br /><sub>Agents drawer: saved configuration, read only</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/agents-drawer-ar.png"><img src="assets/branding/screenshots/agents-drawer-ar.png" alt="The agents drawer in Arabic" /></a><br /><sub>The same drawer in Arabic</sub></td>
  </tr>
</table>

[Arabic mobile drawer](assets/branding/screenshots/agents-drawer-ar-mobile.png). Skills and multiple MCP-server resources are not assembled or verified by this drawer.

### An updates brief, from setup to review

**1. Make the recipe yours.** Open **Agent role templates → Build an Updates Researcher recipe**. Choose your saved agent, edit the instructions and supply the topic, source excerpts and dates. Inspect its existing model and MCP tool configuration; previewing or saving creates no execution or access grants. [Recipe setup](docs/AGENT-RECIPES.md).

**2. Review the actual request.** Save the private canvas, grant the agent the context it needs, then inspect the exact instructions and context in the run review. Confirm the run separately. Managed MCP tool calls retain their individual approvals; an external agent needs its running client.

**3. Read the private result.** The returned brief becomes an attributed note alongside the original instructions and sources. Compact card types, separate connection points and a full-note reader make the work easier to inspect. Publishing remains a separate decision.

<table>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/researcher-setup-en.png"><img src="assets/branding/screenshots/researcher-setup-en.png" alt="Updates Researcher recipe with editable instructions and supplied evidence for Atlas Studio" /></a><br /><sub>1. Make the recipe yours</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/researcher-review-en.png"><img src="assets/branding/screenshots/researcher-review-en.png" alt="Run review showing the exact instructions and supplied source before confirmation" /></a><br /><sub>2. Review the exact request</sub></td>
  </tr>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/researcher-result-en.png"><img src="assets/branding/screenshots/researcher-result-en.png" alt="Result shown as an attributed private note beside its source and task cards" /></a><br /><sub>3. Read the private result</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/researcher-result-ar.png"><img src="assets/branding/screenshots/researcher-result-ar.png" alt="The same result in Arabic" /></a><br /><sub>The same result in Arabic</sub></td>
  </tr>
</table>

Switch between **Workflow**, **Grid** and **List** views without changing the saved content or connections. [Arabic mobile story: setup](assets/branding/screenshots/researcher-setup-ar-mobile.png) · [Review](assets/branding/screenshots/researcher-review-ar-mobile.png) · [Result](assets/branding/screenshots/researcher-result-ar-mobile.png).

*These screenshots use fictional Atlas Studio data. The recipe and its cards ship in releases since 0.11.0. A test client sends the reviewed request to a scripted local HTTP provider, then returns its response through the real MCP workflow. This demonstrates setup, consent and private delivery; it does not demonstrate live news retrieval or model quality. This recipe is manually run, with no daily polling.*

**Agent bundles:** add bounded inline skill instructions and reference text to reviewed external-agent or managed-text requests, and attach version-pinned MCP connection profiles with saved discovery status. [Manage skills and MCP references](docs/AGENT-BUNDLES.md). External clients configure their own endpoints, credentials and tools. Named OpenAI-compatible managed text agents can use [explicitly selected MCP tools](docs/MANAGED-MCP-TOOLS.md), with owner review of every exact call and a private final note. This mode starts disabled and supports manual runs only.

**Start from an inspectable role.** Seven compact bilingual examples combine five inline skills each. Inspect the instructions, then prepare an unsaved bundle for an owned agent. A role grants no permissions, chooses no provider and makes no tool call. [Role templates](docs/AGENT-ROLE-TEMPLATES.md).

<table>
  <tr>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/agent-roles-en.png"><img src="assets/branding/screenshots/agent-roles-en.png" alt="Agent role catalog with labeled categories and expandable skill instructions" /></a><br /><sub>Role templates</sub></td>
    <td width="50%" valign="top"><a href="assets/branding/screenshots/agent-roles-ar.png"><img src="assets/branding/screenshots/agent-roles-ar.png" alt="The role catalog in Arabic" /></a><br /><sub>The same catalog in Arabic</sub></td>
  </tr>
</table>

[Arabic mobile roles](assets/branding/screenshots/agent-roles-ar-mobile.png). The catalog shows example instructions; it is not a team of running agents.

[MCP and permissions](docs/MCP.md) · [Canvases](docs/CANVASES.md) · [REST API](docs/API.md)

## Operate with confidence

Schedule daily or weekly local backups, track verified success and storage, and inspect failure alerts. Preview a disposable, read-only restore on localhost before restoring into a new directory. `gorun doctor` diagnoses an existing site without migrating or repairing it.

Extension screens distinguish **Server directory**, **Uploaded ZIP** and **Legacy: method not recorded**. An installation method does not establish a package's origin or safety. [Installation-method guide](docs/EXTENSION-INSTALLATION-METHODS.md).

<a href="assets/branding/screenshots/extensions-methods-en.png"><img src="assets/branding/screenshots/extensions-methods-en.png" alt="Extension directory grouped by recorded installation method, including unknown legacy records" width="49%" /></a>

<sub>Extensions grouped by installation method</sub>

[Arabic extension methods on mobile](assets/branding/screenshots/extensions-methods-ar-mobile.png).

[Backups and recovery](docs/BACKUPS.md) · [Local diagnostics](docs/local-start.md#diagnose-a-local-site)

## Find your next step

| I want to… | Start here |
| --- | --- |
| Install or deploy GoRun | [Installation](docs/GORUN-INSTALLATION.md) · [Production](docs/PRODUCTION.md) |
| Create or customize a theme | [Theme tutorial](docs/THEME-DEVELOPMENT.md) |
| Build an integration | [Plugin tutorial](docs/PLUGIN-DEVELOPMENT.md) · [REST](docs/API.md) · [MCP](docs/MCP.md) |
| Build on the Go core | [Architecture](docs/ARCHITECTURE.md) |
| Operate an existing site | [Backups](docs/BACKUPS.md) · [Upgrades](docs/UPGRADE.md) · [Troubleshooting](docs/TROUBLESHOOTING.md) |
| Explore everything | [Documentation index](docs/README.md) |

## Project status

GoRun is under active development. Use the [latest stable release](https://github.com/zadstack/gorun/releases/latest); the main branch may contain features awaiting release.

Run one server process per SQLite data directory. Canvases are private to their owner with explicit agent access. Full-site generation from a single prompt is not available. Core updates never run on their own: an operator starts a [staged update](docs/UPGRADE.md#7-staged-updates-in-app) under **Operations → System updates**, which checks compatibility, verifies the release, takes a backup, then activates and health-checks the new version. Backups restore into a new directory; switching live traffic remains an operator action. Provider-backed generation and payments require valid credentials and deployment-specific checks.

Screenshots show an isolated local installation with fictional demonstration content. They do not show customer data or a connected provider.

## Contribute

Bug reports, documentation improvements and contributions are welcome. Read the [contribution guide](CONTRIBUTING.md) and [security policy](SECURITY.md). GoRun is released under [GNU AGPL v3](LICENSE); see [NOTICE](NOTICE) and the [changelog](CHANGELOG.md).
