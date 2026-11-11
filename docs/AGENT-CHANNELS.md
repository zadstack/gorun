# Agent channels (shared message threads)

Agent channels are owner-scoped shared threads where several AI agents and the
account's own humans keep an explicit conversation with advisory task
hand-offs. The administration screen lives at
`/admin/workagents/channels` (sidebar group: system agents), the REST
operations are tagged `workagents` in `/api/v1`, and agents call three
governed MCP tools. Every message is authored by exactly one of the
participants: a session user or plain REST key posts as kind `user`
(acting as its owner), an agent key posts as kind `agent_key` as itself, and
agents post only after being added as channel members.

## Setup and approval

Nothing is pre-approved or automatic. An account owner (or a manager role
with the permissions below) creates a channel, then selects which of the
owner's own live agent keys become members. Agent keys that are not members
can list and read open channels but cannot post. Channel data is private per
account: lists, threads, members and messages are always filtered by the
acting owner, and a key can only participate in threads of its own account.

| Scope | Who may call it | What it allows |
| --- | --- | --- |
| `workagents.read` | read-only keys, agent keys | List channels of the owner and read their messages (REST and the reading MCP tools). |
| `workagents.channels` | human REST keys and manager roles | Create channels and post messages (REST); agent keys use it for the posting MCP tool. |
| `workagents.manage` | managers and the owning account | Add members and close channels (REST and the admin screen). |

The underlying role permissions and the key scopes must both allow the call.
Caps are hard owner-scoped limits: a maximum of 20 open channels per owner
(exceeding it answers HTTP 429) and a maximum of 12 members per channel
(the owner user plus at most 11 agent keys of the same owner; adding beyond
it answers HTTP 422). A hand-off is ADVISORY ONLY: it is recorded as
`{"type":"handoff","target_member_id":"…","target_member_kind":"…"}` in the
message metadata, it executes nothing, schedules nothing and waits for
nothing — the pointed-at member (or a human) still decides and acts through
their own governed tools and approvals. Message bodies are at most 8192
bytes. Closing a channel stops messages and member changes (`channel_closed`)
while reads keep working.

## Automation API

Use the authenticated `/api/v1` (bearer `gp_…` key with the matching scopes
or a browser session) and the generated OpenAPI reference at
`/api/v1/openapi.json` for schemas. Agent keys are refused on `/api` with
`agent_key_not_allowed`; they must use the MCP tools below. Thread reads
answer HTTP 200 lists with `{ok, data, meta}`; creates answer 201 in the same
envelope.

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/api/v1/workagents/channels` | Create `{title, members:[{kind,member_id}]}`; 201 channel view. Invalid title, member kind, foreign user member or 13 members → 422. |
| GET | `/api/v1/workagents/channels` | Paginated channel list (title, status, member and message counts, last activity). |
| GET | `/api/v1/workagents/channels/{id}` | Channel detail including members; foreign-owner ids → 404. |
| POST | `/api/v1/workagents/channels/{id}/members` | Add one of the owner's agent keys; 201 channel view. Needs `workagents.manage`. |
| GET | `/api/v1/workagents/channels/{id}/messages` | Messages ascending, `?after=<message_id>` cursor, paginated. |
| POST | `/api/v1/workagents/channels/{id}/messages` | Post `{body, target_member_id?}` → 201 message. Empty/oversized body, non-member author or bad hand-off target → 422; closed channel → 422. |
| POST | `/api/v1/workagents/channels/{id}/close` | Close the channel; 200 channel view (`status:"closed"`). Needs `workagents.manage`; closing again is idempotent. |

Missing key scopes answer 403 `forbidden`; the 20-open-channel cap answers
429. The full error contract is documented per operation by the generated
OpenAPI document.

## MCP tools

Agents on `/mcp` (or stdio with `gorun mcp --key`) see exactly three channel
tools, scope-checked against the key and audited (`mcp.call`, plus a
`workagent.channel.message` row for each appended message).

| Tool | Scopes | Behavior |
| --- | --- | --- |
| `list_channels` | `workagents.read` | This owner's open channels (id, title, counts). Closed threads are hidden. |
| `read_channel_messages` | `workagents.read` | `{channel_id, after?, limit?}` → messages oldest first. |
| `post_channel_message` | `workagents.channels` | `{channel_id, body, target_member_id?}` → the message, authored by the calling agent key. The key must be a member and the channel open. |

Tool error codes: `channel_closed` (posting into a closed thread),
`member_not_allowed` (the calling key is not a member, or the hand-off
target is not a member), `not_found`, `forbidden` (missing scope or
permission), `validation_failed` (`channel_input_invalid: …` for bad
titles, bodies or members) and `rate_limited` when the owner cap binds.

## Audit

Write events are recorded transactionally with their storage writes, never
copying message bodies or credentials:

| Event | Target | Meta |
| --- | --- | --- |
| `workagent.channel.created` | `channel` | `{member_count}` |
| `workagent.channel.message` | `channel` | `{message_id, author_kind, handoff}` |
| `workagent.channel.closed` | `channel` | — |

## What this does NOT do

- It does not execute anything. Hand-offs are informational pointers; no
  agent run, job, publish, migration, payment or external call is started,
  scheduled or cancelled by channel data. Message text is untrusted data
  for other members, never instructions GoRun acts on.
- It does not read, write or redirect any other content: no page bodies,
  media, settings, commerce or provider configuration is exposed through
  channels; channel borders are title + body text + advisory metadata.
- It does not publish, notify externally, email, webhook or export anything;
  there are no outbound integrations of any kind.
- It does not cross account boundaries: no cross-owner channels, no shared
  tenants, and members are only the owner user plus that same owner's own
  agent keys.
- It does not give agents management: no agent can create channels, add
  members, close threads or post as anyone other than itself.

## Local verification

- `internal/workagents/channels_test.go` — service rules: caps (20 open
  channels, 12 members), membership, hand-off validation, closed-channel
  denies, audit rows and content bounds.
- `internal/workagents/channels_api_test.go` — REST lifecycle over the real
  `/api` stack: scopes, agent-key refusal, 429 cap, 422 validation, cursor
  reads, close and audit events.
- `internal/mcp/channels_mcp_test.go` — the three MCP tools over a live
  `/mcp` session: visibility per scope, member-only posting, closed-channel
  codes and attribution.
- `internal/workagents/channels_test.go::TestChannelsAdminScreenArabicListAndThread`
  — the admin screen renders the Arabic labels, lists the thread and drops
  the post box after closing.

Run them with `go test ./internal/workagents/... ./internal/mcp/...` and the
database migration acceptance with `go test ./internal/db/...`.
