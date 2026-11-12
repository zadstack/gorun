# Spec: notification delivery (email and Telegram)

Status: approved for implementation (brief of 2026-11-12). Implements on top of the
in-app notifications centre shipped in 0.22.0.

## Objective

The notifications centre (`/admin/notifications`, the bell) shows items that are
*derived* on each request from module tables (agent approvals, content to review,
comments to moderate, failed jobs/backups/updates, agent hand-offs), filtered by
the viewer's permissions. Nobody sees them unless they open the admin. This
feature delivers the same items outside the app, by email and Telegram, without
turning them into stored events and without widening who sees what.

User stories:

- As an editor I get one email within ~5 minutes when content is submitted for
  review, or one daily digest at 07:00 UTC, in my admin language.
- As an operator I get a Telegram message when a job, backup or update fails.
- As any user I can turn channels off, mute categories, send myself a test, and
  unsubscribe from email from the email itself without signing in.
- As an administrator I configure one Telegram bot for the site; its token is
  never shown again.

## Assumptions

1. Delivery is per user and opt-in: nothing is sent until a user enables a channel.
2. Enabling a channel does not send the backlog: only items whose time (`At`) is at
   or after the moment the channel was enabled are delivered.
3. Delivery is **at most once** per (user, channel, key). A key whose send failed
   is retried on the next runs, up to 3 attempts. A crash between claiming and
   sending loses that item on that channel (it stays visible in the app).
4. The source `Detail` (a content title, tool name, channel title, job kind) is a
   title, not a body; it is sent truncated to 80 characters. Nothing else from the
   source rows is sent.
5. Telegram is reached only at `https://api.telegram.org` through the SSRF-safe
   client; the base URL is replaceable by tests only (unexported field).
6. Polling (`getUpdates`) is not implemented: the webhook is required (documented
   as deferred).

## Design

### Categories

`kernel.NotificationSource` gains `Category` (stable id). Categories a user can
mute: `approvals` (mcp.approvals), `review` (content.review), `comments`
(content.comments), `failures` (admin.jobs, admin.backups, admin.updates),
`handoffs` (workagents.handoffs). A source without a category is `other` and
cannot be muted. Muting affects delivery only, never the in-app centre.

### Data (migration `admin/0013_notification_delivery`)

- `notification_prefs` (one row per user, `ON DELETE CASCADE`): `email_mode`
  (`off|immediate|digest`), `digest_hour` (0–23 UTC), `email_since`,
  `last_digest_at`, `telegram_enabled`, `telegram_since`, `telegram_chat_id`,
  `telegram_linked_at`, `telegram_failures`, `telegram_disabled` (reason),
  `muted` (JSON array of categories), `checked_at`, `updated_at`.
- `notification_deliveries` (`user_id, channel, key` primary key): `status`
  (`sending|sent|failed`), `attempts`, `delivered_at`. Pruned after 14 days
  (sources look back 7).
- `notification_telegram_codes`: `code_hash` (SHA-256, primary key), `user_id`,
  `expires_at`, `used_at`.

Site settings: `notify.telegram.bot_token` (encrypted with `db.Encrypt`, AAD
`settings:notify.telegram.bot_token`), `notify.telegram.bot_username`,
`notify.telegram.webhook_secret` (encrypted), `notify.telegram.webhook_at`.

### Outbox run

- Cron `notifications.deliver` (every 5 minutes) enqueues the job
  `notifications.deliver` with idempotency key `notifications.deliver:<UTC
  5-minute bucket>`, so with two instances exactly one job exists per bucket and
  the queue's atomic claim runs it once.
- The job selects at most 200 users (active, with email on and mail configured,
  or Telegram on, linked and a bot configured), least recently checked first.
- Per user: load the user, build a context *as that user* (`auth.WithUser`, admin
  locale), and for each registered source check `auth.Can(source.Permission)`
  then `Load` (the same function the bell uses). This is the permission re-check
  at send time: a user who lost a permission, was suspended or deleted receives
  nothing.
- Per channel: drop muted categories and items older than the channel's `since`,
  then claim up to 20 keys with
  `INSERT … ON CONFLICT DO UPDATE … WHERE status='failed' AND attempts<3`; only
  rows this run inserted or re-claimed are sent (safe against a concurrent run).
  Success marks them `sent`, failure marks them `failed`.
- Email `immediate`: one message per run listing the newly claimed items.
  Email `digest`: when the current UTC hour equals `digest_hour` and no digest was
  sent today, one message with the items not yet delivered; `last_digest_at` is
  set even when there is nothing to send.
- Telegram: one message per run with the new items.

### Email

Mailer templates `notification.{en,ar}.{txt,html}` (plain text + simple HTML, RTL
for Arabic). Content: subject with count, each item's translated title, truncated
detail, and an absolute link to `/admin/notifications/go?key=…`; links to the
preferences page and a one-click unsubscribe page. Sent with
`Mailer.SendRequired` (never logged as a fallback). When mail is not configured the
preferences page shows "Configure mail first" and the email controls are disabled;
the run skips email.

Unsubscribe: `GET /notifications/unsubscribe?token=…` shows a confirmation page;
`POST` sets `email_mode=off` and audits `notifications.email_unsubscribed`. The
token is `v1.<user id>.<HMAC-SHA256>` under a key derived from `GORUN_SECRET_KEY`
with the purpose `gorun/notifications/unsubscribe/v1`; it can only turn email off
for that user. GET never changes anything (link scanners).

### Telegram

- Settings page `/admin/settings/notifications` (`settings.manage`): bot token
  (password field, never rendered; "Configured" badge), bot username, **Register
  webhook** (calls `setWebhook` with the secret header) and remove-token in a
  danger zone. Saving a token rotates the webhook secret. Audited:
  `notifications.telegram_bot_updated`, `…_bot_removed`, `…_webhook_registered`
  (no secret values in metadata).
- Linking: My account → Notifications → **Link Telegram** creates a code (12
  random bytes, base64url), stores only its SHA-256, expires in 15 minutes,
  replaces earlier unused codes, and shows `https://t.me/<bot>?start=<code>`.
- Webhook `POST /webhooks/telegram/{path}`: `path` and the
  `X-Telegram-Bot-Api-Secret-Token` header are derived from the webhook secret by
  HMAC; both compared in constant time. Bot not configured or wrong path → 404,
  wrong header → 401, body over 64 KiB → 413. `/start <code>` in a private chat
  consumes the code atomically (single use, unexpired) and stores the chat id; the
  answer is a `sendMessage` method in the webhook response (no outbound call).
  Audited `notifications.telegram_linked`; unlink audits
  `notifications.telegram_unlinked`.
- Sending: `sendMessage` (plain text, no previews) through
  `httpx.NewSafeClientOpts{NoRedirects}`; 3 attempts with backoff (1 s, 2 s,
  honouring `retry_after` up to 10 s) for 429/5xx/transport errors. A 403 counts a
  failure; after 3 consecutive 403s Telegram is disabled for that user
  (`telegram_disabled='blocked'`), shown on the preferences page. Errors are
  logged with the token redacted.

### Preferences page `/admin/account/notifications`

Signed-in users. Email mode (off / immediate / daily digest + UTC hour), Telegram
enable / link / unlink, category mutes (only categories with a source the user
may see), and **Send test** per channel (3 per channel per 10 minutes per user).
Linked from My account (one line) and the bell panel footer.

### Observability

- `/metrics`: `gorun_notification_deliveries_total{channel,outcome}` (counter;
  outcome `sent|failed`).
- Structured logs: `notify: delivery run` (users, sent, failed, duration),
  `notify: send failed` (channel, user id, redacted error).
- Audit events listed above.

## Commands

```sh
gofmt -l . ; go vet ./... ; go test ./...
go test ./internal/admin -run 'Notify' -race
python3 tools/docs-check.py
node tools/admin-icons/update.mjs --check
cd e2e && npx playwright test tests/notification-delivery.spec.ts
```

## Project structure

- `internal/kernel/notifications.go` — `Category` field.
- `internal/admin/notify_*.go` — preferences, outbox, email, Telegram, settings.
- `internal/admin/migrations/0013_notification_delivery.sql`.
- `internal/admin/catalog/notifydelivery.{en,ar}.json`.
- `internal/mailer/templates/notification.*`.
- `internal/metrics` — delivery counters.
- `web/admin/templates/pages/notifications/*.html`.
- Tests next to the code (`internal/admin/notify_*_test.go`), e2e in
  `e2e/tests/notification-delivery.spec.ts`.

## Testing strategy

Real SQLite via the admin test harness; fakes only for the mailer (send hook) and
the Telegram API (`httptest` server injected in tests). Cases: dedupe across runs,
permission lost → nothing sent, digest only at the chosen hour and once a day,
unsubscribe token (valid, tampered, other user), webhook auth (bad path 404, bad
header 401), link-code expiry and single use, 403 → disable, bot token never
rendered, mail-not-configured state, test-send rate limit, metrics counters.

## Boundaries

- Always: permission check per source per run; constant-time secret compares;
  redact tokens in logs; audit linking and bot changes.
- Ask first: new Go dependencies (none needed), `BootOrder`/`Modules()` changes
  (none needed).
- Never: send bodies or secrets; render the bot token; call any host other than
  `api.telegram.org`; touch `internal/auth`.

## Success criteria

All test cases above pass under `-race`; `go vet`, docs check and icon check pass;
preferences page renders in English and Arabic (RTL) at 390 px without horizontal
scroll; Playwright covers the page, the link flow via a fake webhook call, and the
mail-not-configured state.

## Deferred

- `getUpdates` polling fallback.
- Per-item Telegram buttons / inline keyboards.
- `List-Unsubscribe` headers (the mailer has no custom-header support yet).
