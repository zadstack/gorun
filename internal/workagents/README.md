# Native agent workflows

Register `workagents.New(admin.CompleteWorkAgent)` in kernel BootOrder after
`admin`. The module publishes the authenticated `/agents` and `/workspace`
screens, scoped `/api/v1/workagents` operations, system health checks, and
`workagents.run` jobs. It uses native auth, CSRF, audit, SQLite and the persistent
job scheduler. No browser session is required for scheduled execution.

The owner permissions migration grants `workagents.read` and `workagents.run`
to native customer/editor/author/contributor roles. Every query still checks
ownership; these grants do not expose other users' profiles or runs. Management
is separate and is not granted by this migration. API keys additionally need
matching scopes. Register reloads the auth role cache after this module's
migrations so the permissions work immediately on first boot.

## Supported execution registry

- `text`: a text-only AI turn through the encrypted native provider registry.
  Connection and model may be blank to use the site's configured defaults.
  No shell, external tools, publishing or arbitrary HTTP actions are executed.
- `github_stars`: public GitHub repository statistics. Input is
  `{"repository":"owner/repo"}`. Only a fixed, read-only GitHub API endpoint
  is contacted; no private repository credential is accepted or returned.
- `github_starred`: public repositories starred by a GitHub user. Input is
  `{"username":"octocat"}`. Returns repository ID, name, canonical link and
  `starred_at`, deduplicated by repository ID across at most three 100-item
  pages. `truncated=true` explicitly signals the 300-item ceiling. Requests
  use the star media type, accept no token/URL, reject redirects and never
  follow upstream pagination links. Each run persists a bounded snapshot;
  completed job replays do not refetch or create duplicate artifacts.
- `content_draft`: creates a sanitized native draft with the owner's author ID,
  provided that owner has `content.create`. Input is
  `{"type":"post","title":"Title","body":"Content","locale":"en"}`.
  Types are limited to `post` and `page`; locale is `ar` or `en`. It cannot
  publish, replace existing content, delete records or choose another author.
  A stable per-run slug avoids duplicate drafts if a process restarts.

`unsupported_tools` blocks execution even for an enabled profile. Configuration
readability is displayed separately from profile enablement and does not assert
upstream reachability. Provider failures persist a safe error code, never a
successful empty artifact. Logs/audit omit credentials and request content.

## Scheduling and import

Workflow intervals range from 60 seconds to one year; the queue checks due
workflows every 30 seconds. Due-run insertion, job enqueue and advancement of
`next_run_at` commit together. Disabled workflows, inactive owners, disabled
agents and unsupported tools are excluded. API idempotency keys are scoped to
the owner; a changed request with the same key returns a conflict.

Source IDs have unique indexes in the agents, workflows, runs, artifacts and
approvals tables. Import source ownership through an explicit account mapping.
Historical runs/artifacts/approvals must have `historical=1` and no new job
records. Imported workflows should remain disabled until an operator maps
their requested action to the native execution registry. Imported `source_config.organization` and `organization` graphs expose
`blocked_reason=workflow_graph_unsupported`. Activation returns HTTP 409;
the scheduler skips legacy enabled graphs and already-queued graph jobs fail
before any provider call. This is a compatibility guard, not a multi-step
executor. Map steps, output contracts and fresh human approval before enabling.
Historical approvals
are read-only; importing them does not authorize a fresh action.

System health warns about unsupported imported tools, paused source workflows
and failed native executions. Raw source provider credentials/configuration
must remain encrypted and must not be copied into profile prompts, inputs,
artifacts or API-visible descriptions.

## Verification

`go test -race ./internal/workagents` exercises real SQLite, the job queue,
provider HTTP boundary, full kernel boot/catalog/API registration, owner
isolation, escaped HTML, schedule deduplication, disabled/history protection,
bounded GitHub requests, and permission-checked sanitized draft creation.

## Explicit linear flows with fresh approval

`POST /api/v1/workagents/linear-flows` starts a manual, operator-owned plan:

```json
{
  "name": "Reviewed article",
  "input": "Verified source material",
  "request_key": "review-2026-01",
  "draft_title": "Reviewed article",
  "draft_locale": "ar",
  "steps": [
    {"kind": "agent", "agent_id": "native-writer-id"},
    {"kind": "approval"},
    {"kind": "draft", "agent_id": "native-draft-agent-id"}
  ]
}
```

Read `/linear-flows/{id}` for status, result, step and result_hash. At
`awaiting_approval`, the same owning operator submits to
`/linear-flows/{id}/decision` with `approve`, exact `step` and `result_hash`.
A stale/replayed decision returns409; rejection stops execution. Historical
approvals are never read. Management permission is checked on start/decision
and again while advancing. The cron runs every30seconds with persistent jobs;
closing the browser does not stop the flow. An unchanged request_key returns
the same flow; changed input/plan returns409. Each stage has a stable queue key
so a repeated tick cannot duplicate output.

Plans support2–12 steps: text agents, an optional initial public GitHub agent,
approval gates, and an optional terminal content_draft agent immediately after
approval. Every agent belongs to the operator. Output is bounded32KiB between
stages. Drafts are sanitized and never published. This v1 is a strict linear
manual runner, not the imported graph executor: conditions, branching, retries,
audio generation, scheduled source triggers and automatic source-ID mapping
are unsupported. Unknown JSON fields are rejected. Existing imported graphs
remain disabled until an operator explicitly maps and verifies equivalent
contracts; removing source conditions merely to activate a plan is invalid.
