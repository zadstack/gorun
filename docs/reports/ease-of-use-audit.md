# Ease-of-use audit: install, first run and daily use

Date: 2026-11-25. Build: `main` at f7ce40e, built from source (`go build ./cmd/gorun`).

## How the audit was run

- `gorun start --data <scratch>/site --addr :18431 --local-only --no-browser` on macOS, a throwaway
  data directory, no `install.sh` against the real home directory.
- In the browser: wrong password, first sign-in with the one-time password, onboarding (weak
  password, then a valid one), dashboard, public home page, create and publish a page, switch theme,
  create a menu and add items, upload media (PNG, an iPhone HEIC photo, a text file), back up now,
  download links, switch the dashboard to Arabic and scan the visited screens for untranslated text.
- On the command line: a second `start` on a busy port, a second `start` on the same data directory,
  a restart of an existing site.
- Read `README.md`, `docs/local-start.md`, `docs/TROUBLESHOOTING.md`, the setup and onboarding
  handlers (`internal/auth/onboarding.go`, `handlers_setup.go`, `handlers_login.go`) and templates.

Impact: **high** = a non-developer gets stuck or ends up with a broken result; **medium** = they lose
time or must read docs; **low** = polish.

Status: **fixed here** (this PR), **parallel** (already being built in another session), or
**proposed** (follow-up).

## What already works well

- `start` prints the local URL, the admin URL and the one-time password, then opens the browser.
- Onboarding is a single screen; validation errors are clear ("Use at least 10 characters") and
  never echo passwords.
- The public home page of an empty site has a friendly empty state, not a blank page.
- Media library and menus have good empty states with a next step.
- Backups: one click, verified, each file downloadable from the admin.
- The Arabic catalog covers every visited screen (the exceptions are listed below).

## Findings

### Install and first run

| # | Where | What goes wrong | Impact | Fix | Status |
|---|---|---|---|---|---|
| 1 | `gorun start` when the port is taken (default 8080 is common) | Prints only `listen tcp4 127.0.0.1:8080: bind: address already in use` and exits | High | Say the port is busy, that GoRun may already be running (with its admin URL), and how to pick another port (`--addr :8081`, `--addr :0`) | **Fixed here** |
| 2 | `gorun start` twice on the same site | A long lock message aimed at `serve`/`mcp --jobs` operators | Medium | Add: GoRun is already running for this site; open its admin link or stop it with Ctrl+C | **Fixed here** |
| 3 | `gorun start` restart | "Password is not displayed again" with no way forward if the user did not save it | High | Print the exact recovery command with the data folder in use | **Fixed here** |
| 4 | `/admin/login` after a wrong password | "Incorrect email or password." with no recovery path; the one-time password is easy to lose | High | After a failed or locked sign-in, explain: ask an admin for a sign-in link, or reset with `gorun user password` | **Fixed here** |
| 5 | Install diagnostics | No single command explains why a site will not start | High | `gorun doctor` | **Parallel** (already in `main`, extended by Codex) |
| 6 | Upgrades | Manual runbook in `docs/UPGRADE.md` | High | `gorun upgrade` / `gorun rollback` | **Parallel** |
| 7 | Configuration | Settings spread over env vars, flags and `gorun.toml` | Medium | `gorun init` writes one commented config file | Proposed |
| 8 | Onboarding | No password rules shown before submitting (only after an error) | Low | Show "at least 10 characters" as field help | Proposed |
| 9 | Fresh site | No content at all; the starter content exists (`gorun starter`) but nothing in the admin offers it | Medium | Offer "Add sample pages and posts" in the empty dashboard and empty content lists | Proposed |

### Daily use

| # | Where | What goes wrong | Impact | Fix | Status |
|---|---|---|---|---|---|
| 10 | Page/post editor, Publish | An item with no title publishes: it gets an id-like slug (`/01m88b6j…`), a blank browser tab, and shows as a raw id in the menu picker | High | Refuse to take an untitled item live from the editor, with a field error; drafts may stay untitled | **Fixed here** |
| 11 | Editor of a published item | No link to the live page; after Publish the flash only says "Saved" | High | "View on site" button next to the heading, using the theme's permalink (same as menus and sitemap) | **Fixed here** |
| 12 | Media upload | A rejected file says "not allowed" without saying what is allowed; iPhone photos (HEIC) are rejected with no explanation | High | List the accepted types under the drop zone and in the error; HEIC gets "export as JPEG" | **Fixed here** |
| 13 | Sidebar "Settings" | Opens Analytics settings (the lowest-positioned settings page) instead of General settings | Medium | A section's own landing item (ID = section key) is its destination | **Fixed here** |
| 14 | Menu editor, Menu settings | Location dropdown shows theme keys (`primary`, `footer`) although the create form says "Header navigation" / "Footer" (Arabic too) | Medium | Same labels as the create form | **Fixed here** |
| 15 | Bare section URLs | `/admin/settings`, `/admin/content`, `/admin/appearance`, `/admin/system`, `/admin/agents` are 404; the troubleshooting guide says "Appearance → …" | Medium | Redirect each to its first page | Proposed |
| 16 | Dashboard, Recent activity | Shows raw audit codes (`user.update`, `system.backup`, `theme.activate`) in both languages | Medium | Reuse the audit log's labels, or a short sentence per action | Proposed |
| 17 | Content lists | Empty list says only "Nothing here yet" with no button | Medium | "No pages yet" + New page (and the starter content, see 9) | Proposed |
| 18 | Theme switch | "Theme activated" with no link to see the result; no way back except re-activating the old theme | Medium | Link to the site in the flash; restore point with one-day undo | Proposed (restore point already suggested) |
| 19 | Editor, Publish at | "A future time schedules the item (UTC)": owners think in local time | Medium | Show and accept the site or browser time zone | Proposed |
| 20 | Content tabs | "Metricool drafts" appears on a fresh site with no Metricool connection | Low | Hide until connected | Proposed |
| 21 | Sidebar | "Orchestrator: Enable the orchestrator from the…" shows on a fresh Simple CMS site | Low | Hide until agents are set up, or move under Canvas & agents | Proposed |
| 22 | Dashboard, Site settings | "Published pages: Not configured" reads like an error; the dashboard also lists ~40 administration tools | Low | "No published pages yet"; collapse the tools list by default | Proposed |
| 23 | Backups page | Dense wording ("pair", "remote upload acknowledgement", "restore rehearsal"); scheduled backups are off by default | Medium | Plain summary first, details collapsed; offer daily backups on first visit | Proposed |
| 24 | Times in lists | Backups and activity show UTC times without saying so | Low | Local time, or label UTC | Proposed |
| 25 | Dashboard widget layout | Screen readers hear raw ids (`setup_status`, `recent_audit`) from the sr-only layout titles | Low | Use the widget's translated title | Proposed |
| 26 | Moving a site | No single-file export/import between machines | High | Site export/import | Proposed (already suggested) |
| 27 | Risky actions | Theme switch, WordPress import and applying an identity have no undo | High | Automatic restore point with a one-day undo | Proposed (already suggested) |

### MCP

| # | Where | What goes wrong | Impact | Fix | Status |
|---|---|---|---|---|---|
| 28 | Connecting an agent | Keys, scopes and client config are assembled by hand | High | "Connect your agent" panel with a connection test | **Parallel** |
| 29 | MCP prompts/completions | Agents must discover tool sequences themselves | Medium | Prompts, completions, resource templates | **Parallel** |
| 30 | Approvals | Sensitive calls wait silently in AI Agents → Approvals | Medium | Approval notifications with a one-click approve link | Proposed (already suggested) |
| 31 | Key scopes | Picking scopes by hand is error-prone | Medium | `--profile editor|publisher|readonly` | Proposed (already suggested) |

## Fixed in this change

1. `gorun start`: busy port, already-running site and lost password each print the next step
   (`pkg/cli/start.go`).
2. Sign-in: after a wrong or locked attempt the page explains how to recover (en/ar).
3. Editor: publishing needs a title (field error, en/ar); drafts are unchanged; the API and imports
   keep their own rules.
4. Editor: "View on site" on published items through a new `content.public_path` filter that the
   theme module answers with its permalink.
5. Media: accepted types under the drop zone and in type errors; HEIC photos get an
   export-as-JPEG message (library and picker, en/ar).
6. Navigation: "Settings" opens General settings; menu location labels match the create form.

## Recommended next

1. Restore point before risky actions with a one-day undo (27, 18).
2. Starter content offered in the admin and better empty content lists (9, 17).
3. Human-readable recent activity (16) and bare section URL redirects (15).
4. Single-file site export/import (26).
5. Publish-at in local time (19).
