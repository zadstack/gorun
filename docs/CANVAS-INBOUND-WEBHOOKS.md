# Signed inbound commands for a canvas card

Each owned agent/provider card can have one **disabled by default** inbound rule.
The rule maps one exact command to the card's existing connected saved prompt and
current selected context. It cannot execute shell commands or publish content.
Results use the normal private canvas notes/artifact flow. An external named agent
still needs its own connected MCP client to poll `canvases.pending`, obtain context
and append a result; accepting an event does not launch or verify that client.
Managed agents/providers use the existing queue and approved configured runtime.

From an owned canvas, configure a command (1–64 ASCII letters/digits plus `_.:-`,
starting with a letter/digit), review the actual prompt/context, and explicitly
enable. Configuration and rotation require a browser session, `content.edit`, CSRF
and recent password authentication. Review tokens are bound to this rule revision;
older or generic operation review tokens cannot enable it. Prompt editing remains
in ordinary connected prompt cards; there is no separate hidden prompt or template
substitution. Card ID, agent key ID and webhook endpoint ID are distinct identifiers.

The signing secret is encrypted at rest and returned only on creation/rotation.
Store it in the sender's secret manager, never in URLs, canvas notes or logs.
Rotation disables the rule and immediately invalidates its old secret. Re-enabling
requires a fresh review. Disabling also blocks results from already queued or
in-flight work. Graph/instruction/selection, binding, key, grant, owner-role/status
and role-permission changes invalidate consent with a monotonic revision, including
revoke followed by restore. Layout movement and unconnected private result notes do
not invalidate it. Source content may evolve as with recurring schedules, but source
visibility, current edit tokens and authority are rechecked at execution and append.
Changes to AI/connection settings conservatively require a new review for all rules.

## Sender protocol

POST the **exact raw JSON bytes** below to the card's displayed endpoint, using
HTTPS in production. Endpoint identifiers are public routing information, not auth.
Only the command property is accepted; extra prompt, provider, tool or payload
properties are rejected. Command matching is case-sensitive, without trimming.

```json
{"command":"summarize"}
```

Headers:

- `Content-Type: application/json`
- `X-GoRun-Timestamp`: decimal Unix seconds, at most five minutes old/one minute ahead.
- `X-GoRun-Delivery`: unique 16–100 character identifier using letters, digits, `_` or `-`.
- `X-GoRun-Signature`: `sha256=` followed by the hexadecimal HMAC-SHA256 signature.

The HMAC key is the **UTF-8 bytes of the displayed secret string** (do not hex-decode
it). Sign UTF-8 timestamp, `.`, delivery identifier, `.`, then the unchanged raw body.
Retries keep the same delivery identifier and body; timestamps/signatures may be
refreshed. A valid duplicate returns HTTP 200 without adding another task. A reused
identifier with different body bytes is rejected. Durable receipts remain for 24h,
longer than the signed replay window; fresh delivery IDs represent new commands.

A new accepted event returns HTTP 202 with `accepted:true` and status `queued` or
`waiting_agent`. This means acceptance only, not successful agent execution. Valid
duplicates return HTTP 200 with `accepted:true,duplicate:true`. Responses expose no
run identifiers, prompts, contexts or signing secrets. Errors return a generic
`not_accepted`. No browser session or CSRF token is needed at the signed receiver.

Bodies are capped at 4096 bytes. The receiver has a per-process global budget of
300 requests/minute, plus a durable limit of 10 new accepted deliveries/minute per
endpoint. Existing board limits (including 20 outstanding/recent runs per hour)
still apply. These limits may return HTTP 429; use bounded backoff. This is a
single-node SQLite service, not an unlimited event transport. Dedupe receipt, run
and queue record are committed atomically; failed enqueue never consumes delivery.
Disable/rotate does not erase receipts, so replaying an old accepted delivery cannot
repeat work after re-enabling. Do not send the signing secret to an AI provider.
