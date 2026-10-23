# Read-only agent design reviews

The administration screen at `/admin/workagents/design-reviews` previews a
selected page's limited copy before sending it to one configured text agent.
The saved-page editor offers an inline review sidebar backed by the same API.
The sidebar links to the task screen for history and findings. The initial
review covers copy hierarchy and clarity; it does not inspect screenshots,
computed styles or the public page in a browser. Findings are advisory only.
There are no apply, draft or publish actions in this workflow.

## Setup and approval

Configure the site's server-side AI settings or an AI connection through the
existing Connections screen. Create an owned, enabled `text` agent using the
existing work-agent profiles. A profile with unsupported tools cannot review.
Configuration readability does not prove that the upstream provider is reachable.

Choose the saved page and orchestrator, then preview the context and destination.
The preview contains selected-page copy with HTML tags removed, basic credential
pattern redaction, locale, template identifier and active theme identifier.
The theme identifier is metadata; its rendered appearance is not inspected.
It omits hidden HTML, script/style content, custom fields, SEO, account records and arbitrary settings. Pattern redaction cannot identify every
possible secret embedded in prose: inspect the preview before approving it.

Explicit approval is required to enqueue. The service binds approval to the
owner, selected page snapshot, agent, resolved provider configuration and
destination. A changed page or provider configuration requires a fresh preview
and approval. Previewing does not call the provider. Results are untrusted,
validated structured findings rendered as escaped text.

## Automation API

Use the existing authenticated API and its generated OpenAPI reference for
request/response schemas and operation identifiers. API keys require the
matching scopes; the active account also needs the underlying permissions.
The read API requires `content.view` and `workagents.read`; previewing and enqueueing also require `workagents.run`, plus
task and agent ownership. GoRun's current deployment is a site boundary; this feature
does not introduce a separate workspace or tenant system.

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/api/v1/workagents/design-reviews/preview` | Preview `{page_id, agent_id}` without a provider call |
| POST | `/api/v1/workagents/design-reviews` | Enqueue `{preview_id, request_key, approved:true}` |
| GET | `/api/v1/workagents/design-reviews` | List this owner's review tasks |
| GET | `/api/v1/workagents/design-reviews/{id}` | Read status and findings |
| POST | `/api/v1/workagents/design-reviews/{id}/cancel` | Cancel queued or running work |
| POST | `/api/v1/workagents/design-reviews/{id}/retry` | Retry eligible failed work |

Enqueue returns HTTP 202 in the standard API envelope. Reusing the same
owner-scoped `request_key` with the same preview returns the existing task;
reusing it for a different preview conflicts. States are `queued`, `running`,
`done`, `failed` and `cancelled`. Each finding has `severity` (`info`, `warning`
or `critical`), `location`, `suggestion` and a literal `evidence` excerpt from
approved context. Empty findings are allowed; they are not proof of visual QA.

Provider execution uses the existing persistent jobs service with a 45-second
review deadline. Failures expose safe codes such as `provider_unavailable`,
`provider_timeout`, `provider_output_invalid` or `approval_stale`. Check provider
configuration or output, refresh changed context and obtain fresh approval when
necessary. Review audit events record lifecycle actions without copying the
approved page context or credentials.

Cancellation suppresses subsequent result persistence; it cannot retract context
already sent to a provider. A status watcher cancels the running HTTP context.
Interrupted workers become failed tasks without automatic resending; retries are
explicit and limited to three total attempts. Task details link to the existing
audit viewer for accounts with `system.health`. Lists show the latest 100 tasks. Do not treat cancellation as an upstream erasure
request. No review task changes page content, settings or published HTML.

## Network and execution boundaries

A messaging network carries tasks and replies. GoRun's site MCP is a separate
tools/data surface. Native authentication, permissions, jobs and audit remain
independent execution boundaries. This review uses a single server-side text
completion through the existing provider abstraction and exposes no site tools,
shell, arbitrary navigation or publishing privileges to the model.

[Reason Network's official project](https://github.com/reason-machines/reason-network)
documents desktop agents and a hosted MCP messaging endpoint. Its developer
connector is not a portable GoRun product adapter. This version does not connect
the site to Reason Network or execute its CLI. No structured design-review
request/result protocol has been verified for that network; do not infer network
availability from a developer's signed-in desktop session.

## Local verification

From `e2e/`, run `npm ci` followed by
`npx playwright test tests/design-review.spec.ts --project=chromium`.
The test harness builds GoRun against a temporary SQLite database and starts a
loopback-only deterministic HTTP provider. It exercises approval through the
real administration form, hostile-output failure, retry and structured results,
English desktop and Arabic mobile layout, keyboard submission, the actual
editor sidebar preview/approval/API-result flow, and unchanged
page content/settings/public response. It sends no data to a real AI provider.
