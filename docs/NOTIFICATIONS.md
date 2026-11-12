# Notification delivery (email and Telegram)

**العربية:** يرسل GoRun عناصر مركز الإشعارات بالبريد أو تيليجرام لمن يفعّل ذلك من
*حسابي ← التوصيل بالبريد وتيليجرام*. تُرسل العناوين والأعداد وروابط لوحة الإدارة فقط، وتُفحص
صلاحيات المستلم في كل تشغيل. يضبط المدير بوت تيليجرام من *التكاملات ← توصيل الإشعارات*.

The notifications centre (the bell and `/admin/notifications`, see
[ADMIN_WORKSPACE.md](ADMIN_WORKSPACE.md#notifications)) derives its items from current data on each
request. Delivery sends the same items by email and Telegram to users who opt in. Design and decisions:
[specs/notification-delivery.md](specs/notification-delivery.md).

## For users

My account → **Email and Telegram delivery** (`/admin/account/notifications`; also in the bell panel):

| Setting | Effect |
|---|---|
| Email: Off / Right away / Daily digest | *Right away* sends one email with what is new, checked every 5 minutes. *Daily digest* sends one email a day at the chosen hour (UTC) with what was not sent yet. Needs the site's mail to be configured; otherwise the page says **Configure mail first**. |
| Telegram | **Link Telegram** shows a one-time code and a `https://t.me/<bot>?start=<code>` link (valid 15 minutes, single use). After you press Start in Telegram, the account is linked and delivery is on. **Unlink Telegram** forgets the chat. |
| What to send | Mute approvals, review queue, comments, failed jobs/backups/updates or hand-offs. Only categories you can see are listed. Muted items stay in the bell. |
| Send a test | One sample message per channel, at most 3 per channel every 10 minutes. |

Turning a channel on does not send the backlog: only items newer than that moment are delivered. Every email has
a **Stop these emails** link that turns email off without signing in.

## For administrators

**Mail** uses the existing mail settings (`/admin/settings/mail`, SMTP or Resend); nothing else to configure.

**Telegram** (`/admin/settings/notifications`, `settings.manage`):

1. Create a bot with [@BotFather](https://t.me/BotFather); copy its token and username.
2. Paste both and save. The token is encrypted with `GORUN_SECRET_KEY` and never shown again; the page only
   says *Token stored*. Saving a new token rotates the webhook secret.
3. **Register webhook** calls Telegram's `setWebhook` with
   `<GORUN_BASE_URL>/webhooks/telegram/<secret path>` and a secret header. The base URL must be `https://`.
4. Users link their accounts from My account.

**Remove the Telegram bot** (danger zone) deletes the token and webhook secret; linked users receive nothing until
a bot is configured again. It does not call `deleteWebhook`; Telegram's calls then get 404.

Polling with `getUpdates` (for sites Telegram cannot reach) is not implemented; the webhook is required.

## What is sent

Each message lists, per item, the translated title (for example *Content submitted for review*), the item's short
detail (content title, tool name, channel title or job kind, cut to 80 characters) and an absolute link to
`/admin/notifications/go?key=…`, which opens the item after sign-in. Nothing else from the item is sent: no bodies,
no field values, no secrets. Emails are plain text plus simple HTML in the user's admin language (Arabic emails are
right to left); Telegram messages are plain text without link previews.

## How delivery works

Items are not stored as events. Every 5 minutes the cron `notifications.deliver` enqueues one job per 5-minute
bucket (the jobs queue's idempotency key, so several instances still run one job per bucket). The job:

1. picks at most 200 active users with email on (and mail configured) or Telegram on (linked, bot configured),
   least recently checked first;
2. for each, loads the user and evaluates every notification source **as that user**: the source's permission is
   checked and the source filters further, exactly as for the bell. A user who lost a permission, was disabled or
   deleted gets nothing;
3. drops muted categories and items older than the channel's start time;
4. claims up to 20 keys per channel in `notification_deliveries` (`user_id, channel, key` unique). Only keys this
   run inserted, or keys whose earlier send failed fewer than 3 times, are sent, so restarts and concurrent runs do
   not send twice;
5. sends one message per channel and marks the keys `sent` or `failed`.

Delivery is at most once per key and channel; a crash between claiming and sending loses that message (the item
stays in the bell). Delivery records are removed after 14 days, expired link codes after a day.

Telegram sends retry 429, 5xx and network errors twice (1 s and 2 s, or Telegram's `retry_after` up to 10 s). A
403 (bot blocked) is not retried; after 3 consecutive 403s Telegram is turned off for that user, which their
preferences page shows, and `notifications.telegram_disabled` is audited.

## Security

| Concern | Decision |
|---|---|
| Bot token | `db.Encrypt` (AES-256-GCM, AAD `settings:notify.telegram.bot_token`); never rendered, logged or audited. Telegram error texts are cut and the token is redacted; transport errors are reduced to their class because Go's errors repeat the URL. |
| Outbound calls | Only `https://api.telegram.org`, through the SSRF-safe client with redirects refused. The token is validated before it is placed in the path. |
| Webhook | `POST /webhooks/telegram/{path}`: the path and the `X-Telegram-Bot-Api-Secret-Token` header are both HMAC-derived from a random secret stored encrypted, and compared in constant time. No bot or wrong path → 404; wrong header → 401; body over 64 KiB → 413. Only `/start <code>` in private chats is handled; the reply is returned in the webhook response, so linking needs no outbound call. |
| Link codes | 12 random bytes (base64url); only SHA-256 stored; 15-minute expiry; consumed atomically; creating a code removes the user's earlier unused codes. |
| Unsubscribe | `/notifications/unsubscribe?token=v1.<user>.<HMAC-SHA256>` with a key derived from `GORUN_SECRET_KEY` for this purpose only. It can only turn email off for that user. GET shows a confirmation; POST changes the setting (link scanners do not unsubscribe anyone). |
| Abuse | Test sends: 3 per channel per 10 minutes per user. Per run: 200 users, 20 items per channel. |

Audit events: `notifications.preferences_updated`, `notifications.test_sent`, `notifications.test_failed`,
`notifications.telegram_linked`, `notifications.telegram_unlinked`, `notifications.telegram_disabled`,
`notifications.telegram_bot_updated`, `notifications.telegram_bot_removed`,
`notifications.telegram_webhook_registered`, `notifications.email_unsubscribed`.

## Observability

- `/metrics`: `gorun_notification_deliveries_total{channel="email|telegram",outcome="sent|failed"}` counts messages
  (one message may carry several items; test sends included). See [OBSERVABILITY.md](OBSERVABILITY.md).
- Logs: `notify: delivery run` (users, sent, failed, duration_ms), `notify: send failed` (channel, user, error),
  `notify: telegram disabled`, `notify: telegram webhook rejected`.
- The job appears in System → Jobs as `notifications.deliver`.

## Data

Migration `admin/0013_notification_delivery` adds `notification_prefs` (per-user preferences and Telegram chat id),
`notification_deliveries` (the dedupe outbox) and `notification_telegram_codes` (hashed link codes). Site settings:
`notify.telegram.bot_token` and `notify.telegram.webhook_secret` (encrypted), `notify.telegram.bot_username`,
`notify.telegram.webhook_at`. Backups include them like other settings; the encrypted values need the same
`GORUN_SECRET_KEY` after a restore.

## For module authors

Give a notification source a category so users can mute it:

```go
d.Admin.AddNotificationSource(kernel.NotificationSource{ID: "shop.orders", Permission: "commerce.view_orders",
	Category: kernel.NotifyOther, Load: loadOrders})
```

`Category` is one of `kernel.NotifyApprovals`, `NotifyReview`, `NotifyComments`, `NotifyFailures`,
`NotifyHandoffs`; empty means `NotifyOther`, which is always delivered. Keep `Detail` a short title: it leaves the
site by email and Telegram.
