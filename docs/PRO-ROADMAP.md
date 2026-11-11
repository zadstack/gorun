# Professional-grade roadmap

Status: active. Audit of 2026-11-11 against v0.21.0: admin design, the MCP
surface and professional-CMS capabilities, plus WebMCP for the admin. Each
phase ships as its own pull request with tests and refreshed screenshots, and
is merged only with green CI. Update the status column as phases land.

## What the audit found

**Design.** Colours and type are fine; the admin reads as unfinished because
it has no shared admin component set. Templates use 134 distinct button class
strings, 15 page-title variants, 47 CSS font sizes, 23 border radii and 187
hex colours. The shell stacks four navigation levels, puts a pin button on
every tab, repeats links in the rail, topbar and tabs, and shows dates in four
formats. Agent channels, API keys, approvals, forms and private reports show
raw ULIDs or JSON as primary content. Mixed Arabic/English values are not
isolated with `<bdi>`.

**MCP.** 52 tools in one flat list (~28 KB of `tools/list`). Scope filtering
and approvals are solid, but there are no tool annotations, no `outputSchema`,
no toolsets or read-only mode, no prompts or completions, a one-sentence
server instruction, three naming styles (`verb_noun`, `channels_*`,
`canvases.*`), enums that live only in description text, nested `required`
stripped, and canvas tools that share one 7-field input and report bad input
as `internal`.

**Product.** Operations (backups, staged updates, health, audit, OpenAPI +
Scalar, webhooks, CLI) are at or above Ghost/Strapi level. Missing for daily
editing: autosave, revision diff, shareable preview links, review
notifications, 2FA and a self-service profile, a notifications centre, a
global command palette and shortcuts, bulk publish/status/author, content
export and term translations.

**WebMCP.** The draft (Web Machine Learning CG, 2026-11-10) defines
`document.modelContext.registerTool(tool, {signal})` in secure contexts,
gated by the `tools` permissions policy, with `readOnlyHint` and
`consequentialHint` annotations. GoRun exposes nothing to in-browser agents.

## Phases

| # | Phase | Scope | Size | Status |
|---|---|---|---|---|
| 1 | Admin component foundation | Tokens for type, radius and spacing; `.btn-*`, `.field`, `.page-header`, `.data-table`, `.badge-*`, `.empty-state` in `admin.css`; `partials/page-header`; one date/time output (template `date`/`datetime` + JS helper, full value in `title`); `<bdi>` for mixed-direction values. Migrate the most visible pages: dashboard, content list, API keys, approvals, agent channels, forms inbox, private reports. | L | in review (#227) |
| 2 | MCP organization | Tool annotations (`title`, `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`) on all tools; real enums and nested `required`; split canvas inputs and map their errors; routing-guide server instructions + `gorun://guide`; toolsets and read-only mode (`?toolsets=`/`X-MCP-Toolsets`, default all); one `verb_noun` naming with old names kept as hidden aliases; documented error list. | L | done (#226) |
| 3 | WebMCP in the admin | Feature-detected `document.modelContext` registration on admin pages: read-only tools (search, open a page, read the current item, list pins) and draft-only writes. Consequential actions call existing endpoints and still need the human confirmation and approvals already enforced server-side. Relies on the `tools` permissions-policy default (`'self'`) and the admin never being framed. Tools follow the current user's permissions and unregister on navigation. | M | in review |
| 4 | Shell clean-up | One tab bar (categories fold into the sidebar), pin control on hover/focus only, the rail holds agents only, each destination in one place, row actions in a menu, danger zones in settings. | M | planned |
| 5 | Editing safety | Content autosave with conflict handling, revision diff, signed expiring preview links. | M | planned |
| 6 | Speed for power users | Global command palette (Cmd/Ctrl+K) over admin search and actions, shortcut map (`?`, `/`, `g c`, Cmd+S), bulk publish/unpublish/schedule/author/terms. | M | planned |
| 7 | Accounts and awareness | TOTP 2FA with recovery codes and per-role enforcement, "My account" page, notifications centre (approvals, review requests, comments, failed jobs/backups). | L | planned |
| 8 | Data freedom | Content export (JSON/CSV), term translations. | M | planned |

Order: 1 and 2 touch separate code and run in parallel; 3 builds on 2's
annotations; 4 builds on 1's components. Later phases follow in order.

## Done criteria per phase

- Unit/integration tests for new behaviour; `go test ./...`, `go vet`, the
  affected Playwright specs and the README screenshot capture pass.
- English and Arabic checked, desktop and 390 px mobile, no horizontal scroll.
- No tool name, route or API field removed without an alias.
- CHANGELOG entry under *Unreleased*; docs updated (`docs/MCP.md`,
  `docs/ADMIN_WORKSPACE.md`, `DESIGN.md` as relevant).
