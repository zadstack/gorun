# Webhooks and the key console

GoRun can notify your services when something happens on a site (outgoing
webhooks), and manage the API/agent keys those services use (key console).
Both are available in the admin, the REST API and the CLI.

## Webhooks

**Admin:** Integrations → **Webhooks** (`/admin/webhooks`, permission
`webhooks.manage`). **API:** `/api/v1/webhooks…` (see [API.md](API.md)).
**CLI:** `gorun webhook …`.

### Events

| Event | Sent when |
|---|---|
| `content.published` | an item is published |
| `content.deleted` | an item is trashed or deleted |
| `content.comment_submitted` | a visitor submits a comment |
| `media.uploaded`, `media.deleted` | a media file is added or removed |
| `menus.saved` | a menu or its items change |
| `redirects.saved` | redirects are saved, deleted or imported |
| `site.form.submitted` | a public form is submitted (includes the visitor's fields) |
| `user.login` | a staff user signs in |
| `commerce.order.paid` | an order is paid |
| `commerce.entitlement.activated` | an entitlement becomes active |
| `migration.item.imported` | a migration imports an item |
| `webhook.test` | sent by **Send test** only; no subscription needed |

Subscribe to `*` for every event. `GET /api/v1/webhooks/events` and
`gorun webhook events` list them.

Payloads may contain personal data (form fields, comment author emails,
customer ids). Only point webhooks at systems allowed to receive it.
`webhooks.manage` is a high-trust permission: whoever holds it can send
these payloads to any public endpoint and read them back from the delivery
log, where they are kept for 30 days. Grant it like `users.manage`.

### Request

`POST` to the endpoint with `Content-Type: application/json`:

```json
{
  "id": "01M52MP73KHMRC4FMCW4BAGPED",
  "event": "content.published",
  "created_at": "2026-10-16T15:18:44Z",
  "site": "https://example.com",
  "data": { "id": "…", "type": "post", "slug": "hello", "title": "Hello" }
}
```

Headers: `X-GoRun-Event`, `X-GoRun-Delivery` (unique per attempt series),
`X-GoRun-Timestamp` (Unix seconds) and `X-GoRun-Signature`. Bodies above
256 KiB are replaced by `{"truncated": true}` in `data`.

### Verifying the signature

`X-GoRun-Signature` is `sha256=` + hex(HMAC-SHA256(secret, timestamp + "." + raw body)).
Compute it over the raw bytes you received, compare in constant time, and
reject timestamps more than 5 minutes from your clock.

```python
import hmac, hashlib, time

def verify(secret: str, body: bytes, ts: str, signature: str) -> bool:
    if abs(time.time() - int(ts)) > 300:
        return False
    expected = "sha256=" + hmac.new(secret.encode(), ts.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, signature)
```

```js
import crypto from "node:crypto";

export function verify(secret, rawBody, ts, signature) {
  if (Math.abs(Date.now() / 1000 - Number(ts)) > 300) return false;
  const expected = "sha256=" + crypto.createHmac("sha256", secret).update(`${ts}.`).update(rawBody).digest("hex");
  return expected.length === signature.length && crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(signature));
}
```

Go receivers can call `webhooks.Verify(secret, signature, ts, body, time.Now(), 5*time.Minute)`.

The secret (`whsec_…`) is shown once when the webhook is created or its
secret is rotated; it is stored encrypted with the site key.

### Delivery, retries and the log

- Each event becomes one delivery per subscribed, active webhook, sent by the
  job queue. A 2xx answer is success; anything else (or a network error,
  10 s timeout) is retried with the queue's exponential backoff, 6 attempts
  in total, then marked `failed`.
- After 50 failed deliveries in a row a webhook is disabled (audited as
  `webhook.auto_disable`); enabling it again resets the count.
- The admin shows the last 50 deliveries per webhook with status, HTTP code,
  attempts and error; **Redeliver** queues the same payload again (the
  webhook must be enabled: `409 webhook_disabled` otherwise). Deliveries
  still queued when a webhook is disabled are skipped (`failed`, "webhook
  disabled") without counting towards auto-disable. Deliveries
  older than 30 days are pruned daily.
- Endpoints must use `https://`. Private, loopback, link-local and other
  special-purpose addresses (CGNAT, NAT64 `64:ff9b::/96`, 6to4 `2002::/16`,
  `192.0.0.0/24`, `198.18.0.0/15`, `240.0.0.0/4`) are refused at connection
  time (DNS rebinding included) unless the site runs with `env = "dev"` or
  `GORUN_ALLOW_PRIVATE_NET`.
- Redirects are never followed: a 3xx answer marks the delivery `failed`
  (not retried) with an error naming the redirect, so the signed body only
  reaches the configured URL. Configure the final URL instead.
- Creating, updating, deleting, testing, rotating and redelivering are
  recorded in the audit log.
- If the endpoint URL carries a token in its query string, only storage,
  the webhook's own page/edit form and `GET /api/v1/webhooks/{id}` show it.
  The admin list, `GET /api/v1/webhooks`, `gorun webhook list` and audit-log
  entries show origin and path with `?…` in place of the query.

### CLI

```sh
gorun webhook events
gorun webhook create --name CRM --url https://crm.example.com/hooks/gorun --events content.published,site.form.submitted
gorun webhook list
gorun webhook test <id>          # one synchronous attempt; exit code 2 when it fails
gorun webhook deliveries <id> --limit 20
gorun webhook redeliver <delivery-id>
gorun webhook secret <id>        # rotate; prints the new secret once
gorun webhook update <id> --active false
gorun webhook delete <id>
```

Commands that queue deliveries (`redeliver`) rely on the running server's
job workers; `test` sends immediately.

## Key console

**Admin:** AI Agents → **API keys** (`/admin/agents/keys`, permission
`api_keys.manage`): every key with its owner, scopes, rate, expiry, last use
and approximate use count, filters for active and revoked/expired keys,
**Rotate** and **Revoke**. **API:** `/api/v1/keys…`. **CLI:** `gorun key …`.

- **Rotate** issues a new token with the same name, kind, owner, scopes, rate
  and expiry, links it to the old key (`rotated_from`), and retires the old
  one: revoked immediately, or kept working for a grace period (up to 7 days;
  the console offers 1 hour and 24 hours) so clients can switch without
  downtime. Only the key's owner can rotate it.
- **Use count** is approximate: uses are counted in memory and written with
  `last_used_at` at most once a minute per key.
- **No escalation:** a key created or rotated in the admin or through the
  API can only carry scopes its creator holds (`*` needs a role holding `*`,
  and for rotation by key also a `*` key). Agent keys still cannot call
  `/api` at all.
- **Keys cannot mint keys:** `POST /api/v1/keys` requires a signed-in
  session; an API-key caller gets `403 session_required`. Without this, a
  leaked key could create children with no expiry and an unlimited rate
  that keep working after the parent is revoked. Rotation by key stays
  allowed: it only replaces the caller's own key with the same scopes, rate
  and expiry.
- Tokens are shown once (create and rotate responses carry
  `Cache-Control: no-store`) and stored as SHA-256 hashes.

```sh
gorun key create --name ci --user ops@example.com --scopes content.view,content.create --expires 2027-01-31
gorun key list
gorun key rotate <id> --grace 1h
gorun key revoke <id>
```

The CLI acts as the operator (it needs shell access to the data directory),
so it may create keys for any user and rotate any key; its actions are
audited as the system actor.
