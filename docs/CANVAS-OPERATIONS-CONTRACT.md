# Operational canvases: frontend/backend contract v1

This extends the existing native canvas. Saving a board does not execute providers, enable schedules, publish content, or grant access. All routes below are authenticated native admin routes with CSRF for POST. Existing owner/CAS rules remain. No production providers are invoked for development acceptance.

## Documents and nodes

GET/POST `/admin/canvases/{id}/document` retains the existing envelope and POST fields (`version`, `title`, `cards`), adding `edges: [{from,to}]`. The frontend always sends edges. A legacy POST that omits edges preserves existing edges. A document has at most100 cards/200 edges; IDs refer to current cards, edges are unique, no self-links/cycles. Removing a node removes its edges. Only direct edges from source/content/prompt/note to provider/agent, or schedule to provider/agent, are allowed. A schedule has at most one target. No provider/agent chains run automatically.

Existing `note`, `checklist`, `link`, `content`, `widget` cards remain compatible. New kinds use the existing `id,kind,title,body,x,y` plus:

- `source`: `source_type` (registered public type), `source_limit` integer1–5. Optional `source_ids` array of1–5 explicit item IDs. Empty IDs means latest permitted items of that type, bounded by source_limit. Explicit IDs must match the type; all source reads recheck current actor permissions/ownership. The approved dynamic type/count selector may pick newer content at the next scheduled run; it never widens permissions. Private-report tables and other internal types are unavailable.
- `prompt`: body is the editable instruction (existing4000-byte limit).
- `provider`: `provider_id`, either `native-text` or an existing configured Studio **text** binding ID. A provider is a configured generation endpoint, not an autonomous agent identity. No arbitrary endpoint, uploaded code or ad-hoc tool name.
- `agent`: `agent_id` is a named active agent key. This is distinct from a provider. Reviewed runs wait in `waiting_agent` until that external client polls its explicitly granted work; the UI never claims an agent is connected or executing merely because its key exists.
- `schedule`: `interval` enum `hourly|daily|weekly`. Exactly one outgoing edge must target a provider or agent to enable. The interval is saved configuration, not enablement. Its live enabled/next-run state comes from server metadata below.

Ordinary content/product references can be selected using the existing content picker. The source type dropdown includes a registered public product type when present; it does not invent products if none exists. A source node covers type selection/dynamic items; the existing content node covers one exact item.

GET document additionally supplies readonly:

```
provenance: {"card-uuid": {"actor_kind":"user|api_key|provider", "actor_name":"display name", "run_id":"ULID", "created_at":"RFC3339"}},
schedules: {"schedule-card-uuid": {"enabled":false,"next_run_at":"","status":"disabled|enabled|needs_review","reason":""}}
```

These maps are never sent by the editor and never accepted as authored client data. Concurrent output append reads the latest document in a transaction and appends only the new note, bumps version, and preserves every human card/edge. Human CAS conflicts retain unsaved local edits and require explicit reload/reconciliation. Execution/result append must not overwrite a stale whole-board snapshot.

## Options and limited source search

GET `/admin/canvases/{id}/operations/options`

```
{
 "types":[{"id":"post","title":"Posts"}],
 "providers":[{"id":"native-text","title":"Configured text provider","configured":true,"endpoint":"https://provider.example/v1","reason":""}],
 "agents":[{"id":"agent-key-ULID","title":"Named agent key"}],
 "can_share":true
}
```

Only active agent keys belonging to the board owner are offered, and only with the existing key-management permission. Options contain no secrets. Providers that are unavailable appear with configured:false and a human-readable reason. Existing provider IDs absent from options are rendered as unavailable, not silently replaced.

Extend GET `/admin/canvases/{id}/references?q=...` with optional `type` filter. Response remains the existing reference response. Frontend source selection is initially bounded latest; exact content nodes retain the searchable picker, so no raw IDs are required.

## Reviewed manual execution

1. Save document, then POST `/admin/canvases/{id}/operations/preview` with `{version,node_id}` where node_id identifies a provider or named agent. Server builds bounded connected context and snapshots the destination/version, current permissions, selected graph and prompt. Reject unsupported/cyclic/empty configurations. Response:

```
{"review_token":"opaque","expires_at":"RFC3339","board_version":7,"node_id":"uuid",
 "provider":{"id":"native-text","title":"Provider","endpoint":"https://provider.example/v1"},
 "prompt":"reviewable instruction","context":"bounded plain text",
 "items":[{"title":"Source title","type":"post"}],"bytes":1234}
```

2. Explicit Run POST `/admin/canvases/{id}/operations/runs` with `{request_id:"UUID",review_token:"opaque"}`. Returns202 `{run:<run>}`. Tokens bind board/actor/node/provider version/context/graph and expire. Repeated identical request returns the same run; changed destination/graph/consent fails409. No secrets enter browser state. Worker rechecks current grants, source access and provider identity before execution.
3. GET `/admin/canvases/{id}/operations/runs` returns `{runs:[...]}` (newest20, no sensitive raw provider errors):

```
{"id":"ULID","node_id":"uuid","status":"requested|waiting_agent|queued|running|succeeded|failed|uncertain",
 "actor_name":"display name","provider_title":"Provider","created_at":"RFC3339",
 "finished_at":"","result_card_id":"uuid-or-empty","error":"safe human explanation"}
```

A success is a newly appended plain-text note with immutable provenance. It never inserts/replaces/publishes an article. Timeout/ambiguous external delivery is uncertain and requires a new explicit reviewed run; no automatic repeated paid/provider call. No configured provider means a visible explanation, not a fake generated result.

## Schedule activation

POST `/admin/canvases/{id}/operations/schedules/{scheduleNodeID}`:

- Enable `{enabled:true,request_id:"UUID",review_token:"opaque"}` after preview of its connected provider or agent. UI shows interval and recurring outbound-action consent before the explicit enable button.
- Disable `{enabled:false}` without generation preview. Returns `{schedule:{enabled,next_run_at,status,reason}}`.

Schedule starts disabled. Interval/provider/prompt/edge/context-selection edits invalidate activation; changing node coordinates or board title need not invalidate it. Current owner/key/provider permissions are rechecked at every due run. Duplicate cron ticks enqueue at most one run per due slot. Revocation/disable prevents new dispatch. No catch-up flood after downtime. Results append transactionally as above.

## Named agent sharing

GET `/admin/canvases/{id}/operations/grants` returns `{grants:[{agent_id,title,read,append,run,node_ids:[...]}]}`. POST same route `{agent_id,read,append,run,node_ids:[...]}` replaces that one grant; all false revokes. Only owner session may configure grants; agent calls cannot grant themselves access.

The UI picks explicitly exposed note/content/source/prompt node IDs. Both current named-key scope and board grant are mandatory. MCP operations use the existing authenticated operation registry with no changes to the content publishing/approval system:

- `canvases.context`: board ID; returns only granted nodes' bounded text and current board version, not every board or every content row.
- `canvases.request_run`: requires run grant; an external key request creates status requested for explicit human review, never dispatches automatically.
- `canvases.pending`: only delegated waiting_agent runs targeted to the current named key and matching its current board grant. A manual reviewed run targeting an agent waits until this client claims it. Context and result append are tied to that delegated run; unrelated keys cannot fetch or complete it.
- `canvases.append_result`: board/run/version-bound, idempotent new result note only. Requires append grant, run ownership and the tied context/version; cannot edit/delete existing cards. Server authenticates provenance rather than trusting supplied author fields.

Exact MCP tool schemas are backend-owned; reuse the same service validations as HTTP. An API/agent key owned by the human does not automatically inherit their private boards.

## Error semantics and UI

Use existing404 for unowned/unavailable board,403 for permission denial,409 for version/consent conflicts,422 for invalid data,503 for unavailable configuration. JSON `{error:"stable_code",message:"safe display explanation"}`. The editor keeps unsaved edits on failure, exposes retry where safe, and does not display a successful run/schedule/grant before the server confirms it.

## Ownership and tests

Frontend: `web/admin/static/canvas{,-graph,-operations}.{js,css}` as needed, canvas editor template, dedicated canvasops catalogs, JS graph tests. Backend: new focused `internal/admin/canvas_operations*.go`, migration0004 (0001Studio,0002canvases,0003dashboardpins already exist), bounded hooks in existing canvas store/validation/HTTP/module/operations. Root coordinates backend ownership. Do not edit content MCP approval files concurrently.

Acceptance: legacy cards/save/Close, owner/key isolation, type/source filtering, graph limits/cycles, graph/destination consent invalidation, enabled/disabled schedules, at-most-once slot dispatch, revocation before dispatch, uncertain calls not repeated, concurrent additive result append preserving human edits, no content publication, XSS-safe text, EN/AR1440/390/320 and keyboard connections. Use local fake providers only.

## Approved media and ready-board extension

`provider` additionally has `generation_kind` (`text` default, `image`, `video`, `render`) and optional `preset_id` referring to an existing declarative Studio skill. `/options` adds each provider's `kind` and `presets:[{id,title,kind}]`. Agent nodes initially return text notes; media production uses a selected configured provider binding. Unsupported/unconfigured choices cannot execute.

Async runs may be `waiting`. Explicit POST `/operations/runs/{run}/check` with `{}` checks the configured status tool, never repeats creation. Completed media appends a server-authored `result` card with `run_id`. GET document includes `results:{runID:{kind,url,status}}`; URLs must point to the owner-authenticated local `/operations/runs/{run}/media`, never public storage or remote embeds. Result cards may be moved/removed but cannot invent or rebind another run; server checks ownership. Text results remain ordinary notes with immutable provenance and can be manually connected to the next provider.

New-canvas POST accepts an optional allowlisted preset and redirects to the newly created board with `?preset=<id>`. The client offers provider/agent choice, then explicitly fills the still-empty version1 board through normal CAS. IDs: `ask-content`, `prepublish-review`, `social-drafts`, `periodic-brief`, `evidence-worksheet`, `story-video`. Existing populated boards are never replaced. Unresolved source/content/provider/agent fields can save as incomplete nodes but cannot preview or run. No preset enables schedules or fabricates content/results.

Story→video has separate script and video stages, not automatic provider chains. The user reviews the script result note, manually connects it to the video provider, chooses the real configured media tool/skill, and separately reviews/runs that stage. Evidence worksheet explicitly organizes supplied material rather than claiming verification of a real event.
