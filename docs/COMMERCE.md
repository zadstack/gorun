# Commerce: selling access to MCP servers

> **ملخص بالعربية.** وحدة المتجر في GoRun تبيع الوصول إلى خوادم MCP، وليست نسخة من WooCommerce. يختار
> العميل منتجًا في `/shop` ويدفع عبر Stripe Checkout (دفعة واحدة)، فيصل webhook موقّع إلى
> `/webhooks/stripe` ويصبح الطلب مدفوعًا، ثم تنشئ مهمة خلفية الحساب على خادم الفوترة عبر بروتوكول
> `mcp-billing/v1` الموقّع بـ HMAC، ويُفعَّل الاستحقاق ويصل العميل رابط دخول سحري إلى `/account`. الدفع
> لمرة واحدة يبقى كما هو، وتدعم الخطط أيضًا اشتراك Stripe (`mode=subscription`) بحالة نشطة أو فترة سماح
> أو تعليق. الإعداد: مفتاح
> Stripe السري وسر الـ webhook من **المتجر ← إعدادات المتجر** أو من `STRIPE_SECRET_KEY` و`STRIPE_WEBHOOK_SECRET`،
> ثم خادم فوترة من **المتجر ← خوادم الفوترة**، ثم المنتجات.

GoRun commerce sells access to MCP servers. It does not try to be a WooCommerce clone.

```
product ──POST /checkout/{slug}──▶ order (pending) ──Stripe Checkout──▶ webhook ──▶ order (paid)
   └─ job commerce.provision ──▶ POST /mcp-billing/v1/accounts ──▶ entitlement (active) ──▶ magic-link email ──▶ /account ──▶ "Open" (login-link)
```

The code lives in `internal/commerce` and boots after `admin`. The other modules use it through the
`commerce.Service` contract in `contract.go`, published as `kernel.ServiceCommerce`.

One-time products stay on `billing_mode=payment` and `mode=payment`. A product with
`billing_mode=subscription` checks out as a Stripe subscription and keeps the same order, customer
and entitlement records.

## Subscriptions

1. **Plan.** A product stores `stripe_price_id`, `billing_interval` (`month` or `year`), `trial_days`
   and `prices_json`. `prices_json` maps a currency to `{price_id, amount_minor}`. The admin form
   edits USD and AED; checkout uses the store currency when that entry exists, otherwise the product
   currency. `trial_days` is sent as `subscription_data.trial_period_days`.
2. **Customer.** `customers.stripe_customer_id` is saved from the first paid Checkout session and
   sent as `customer` on later sessions. A new buyer still uses `customer_email`.
3. **Checkout.** Subscription sessions use `mode=subscription`, `client_reference_id = order.id` and
   `subscription_data.metadata` (`account_id`, `plan`, `site_id`, `order_id`). One-time sessions are
   unchanged. Optional `automatic_tax` is a store setting.
4. **Webhooks.** `POST /webhooks/stripe` still verifies `Stripe-Signature` and records `stripe_events`
   idempotently. It also accepts:
   - `customer.subscription.created`
   - `customer.subscription.updated`
   - `customer.subscription.deleted`
   - `customer.subscription.trial_will_end`
   - `invoice.paid`
   - `invoice.payment_failed`

   `POST /webhooks/stripe/test` and `POST /webhooks/stripe/live` verify with the matching secret.
   The original path uses the active mode (live unless Admin → Store settings selects test).
5. **Access.** `active` and `trialing` keep the commerce entitlement active and emit
   `subscription.activated`. `past_due` keeps access for `commerce.grace_days` (default 7), emits
   `subscription.past_due`, and emails the customer. When the grace window ends, access is suspended.
   `canceled` and `unpaid` suspend immediately and emit `subscription.canceled`. A later `invoice.paid`
   resumes access. SaaS listens to the same three events and sets entitlement status to `active`,
   `grace` or `suspended`.
6. **Portal and invoices.** Signed-in customers with a Stripe customer id can open
   `POST /account/billing-portal` (upgrade, downgrade, cancel, payment method; proration is Stripe's
   portal default). Invoices store `stripe_invoice_id`, `hosted_invoice_url` and `invoice_pdf` and
   are listed on `/account`.
7. **Refunds.** Admin → order → Refund calls the Stripe Refunds API for the PaymentIntent, then
   marks the order refunded and suspends the entitlement. `charge.refunded` remains idempotent.
8. **Plan gate.** `commerce.RequirePlan(ctx, deps, accountID, plan)` and
   `Service.RequirePlan` allow an account with an active, trialing, or in-grace subscription.
   An empty plan means any such subscription. Plugins should subscribe to the three events above
   instead of calling Stripe.
9. **Test and live mode.** Store settings keep separate test and live secrets and select the mode
   with `commerce.stripe_mode`. Store settings show the current mode as a badge and, for each mode,
   three separate states: **configured** (key and webhook secret present), **authenticated** (the
   stored key passed a read-only `GET /v1/balance` after **Verify key**; the result is tied to that
   key's fingerprint, so a replaced key is unverified again) and **webhook delivery verified** (a
   signed event from Stripe reached `/webhooks/stripe/<mode>`; the local **Send test event** is
   recorded as `source=local` and never counts). A key's presence or `sk_test_`/`sk_live_` prefix is
   only a hint, never verification. Switching to **test** needs no confirmation and no Stripe call
   (a live-prefixed key in the test slot is refused). Switching to **live** is refused unless the
   form carries the explicit confirmation, a live key that is not test-prefixed and a live webhook
   secret are effective, and the read-only check reports `livemode=true`; a refused switch saves
   nothing. No charge is ever created by these checks. **Send test event** signs a local
   `customer.subscription.updated` with the test webhook secret and processes it. Secrets are not
   written to logs. The setup checklist is `/admin/commerce/setup`.
10. **Provenance.** `orders.stripe_mode` records the mode whose key created the Checkout Session
   and `stripe_events.mode`/`source` the mode whose secret verified each event; both are shown as
   chips in Orders. The current setting never relabels an order: a test order is only ever paid by
   an event the test secret verified (a live-signed event is audited as `commerce.order.mismatch`
   with reason `stripe_mode`), and a refund uses the order's own mode's key. Orders from before
   this column keep `''` ("unrecorded").
11. **Bootstrap.** `commerce.settings_version` pins `commerce.stripe_mode` once per store: a store
   with no orders and no Stripe secret in settings or the environment starts in **test**; any other
   store is pinned to the mode it already runs in (live when the value was absent). An absent
   value still means live at read time; the bootstrap only makes the choice explicit.

## Flow

1. **Shop.** `/shop` lists active products grouped by `group_slug`. Products are ordered by `sort`,
   and groups follow the order of their first product. `/product/{slug}` shows one product.
   Both pages render through the active theme, using kinds `shop` and `product`.
2. **Checkout.** `POST /checkout/{slug}` is protected by CSRF. Its form fields are `email` and a
   `website` honeypot, and it is rate-limited per IP. It:
   - finds or creates the user (role `customer`, no password) and its `customers` row;
   - creates a `pending` order with a frozen product snapshot and the next order number (from 1000);
   - creates a Stripe Checkout Session with `mode=payment`, `client_reference_id = order.id` and
     `customer_email`. The success URL is `/checkout/success?o={order}&s={CHECKOUT_SESSION_ID}` and
     the cancel URL is `/product/{slug}`;
   - answers 303 to Stripe and sets a signed `gp_order` cookie (an HMAC of the order id).
3. **Webhook.** `POST /webhooks/stripe` verifies `Stripe-Signature` with the webhook secret. It
   records the event id in `stripe_events` and processes the event inside the same transaction, so
   a replayed event changes nothing.
   - `checkout.session.completed` or `checkout.session.async_payment_succeeded`: the order moves
     `pending → paid` only when the session matches it exactly. That means `payment_status=paid`,
     `mode=payment`, `amount_total` equal to the order total, the same currency, the stored session
     id (when one is stored), and `metadata.order_id` equal to `client_reference_id` when both are
     present. Then `commerce.provision {order_id}` is enqueued in the same transaction (job
     idempotency key `commerce.provision:{order}`), and after commit `commerce.order.paid` is
     emitted. Other completions:
     - a session that does not match the order, whatever its status, is audited as
       `commerce.order.mismatch` with the session id and amounts. It never pays, and the event is
       still recorded as processed. This stops a cheap Payment Link that carries another order's
       `client_reference_id`;
     - a redelivery of the session that already paid the order is ignored;
     - a matching completion for an order that is no longer pending (e.g. cancelled) is audited as
       `commerce.order.paid_closed`: money was taken that nothing will provision, so refund or
       fulfil it by hand.

     Only `pending` orders can be paid.
   - `checkout.session.expired` moves a pending order to `cancelled` (audit
     `commerce.order.cancelled`). `checkout.session.async_payment_failed` moves it to `failed`
     (audit `commerce.order.payment_failed`).
   - `charge.refunded` with `refunded=true`: the order becomes `refunded` and its entitlement
     `suspended`, and `commerce.suspend` is enqueued. A partial refund keeps access. A refund that
     arrives before the completion is remembered: by `orders.refunded_pending` when the charge's
     `metadata.order_id` names the order, otherwise by its payment intent. The late completion then
     marks the order `refunded` and never provisions it.
4. **Provisioning.** The `commerce.provision` job allows 8 attempts with exponential backoff.
   - The entitlement is created exactly once (`UNIQUE(order_id)`), as is its `create` operation
     (`provisioning_ops.idempotency_key = "create:"+order.id`).
   - It calls `POST /accounts` and stores the response. The entitlement becomes `active` with
     `ends_at = period_end`, and the order becomes `active`.
   - The customer is emailed a 24-hour magic link to `/account`, and
     `commerce.entitlement.activated` is emitted.
   - Only a non-retryable answer from the billing server is permanent: a 4xx other than 408 or
     429, or a response that breaks the contract (`status` not `active` or `pending`). It marks the
     operation, entitlement and order `failed` and writes the audit entry
     `commerce.entitlement.failed`.
   - Everything else is retried: transport errors, 408, 429, 5xx, and local errors such as a busy
     database, an undecryptable secret or a bad server URL. The job worker passes the attempt
     number to the handler (`jobs.AttemptFromContext`). On the last allowed attempt it fails the order instead, with audit
     `commerce.order.provision_failed`.
   - **Admin → Orders → Retry provisioning** re-enqueues a failed paid order with a fresh operation
     key, `create:{order}:retryN` (audit `commerce.order.retry`). The entitlement's access window is
     recomputed from the time of the retry, so the customer still gets the full period.
   - An unparseable `period_end` in the answer falls back to the `period_end` GoRun sent, with a
     warning logged. This is GoRun's own choice, not something the PHP plugin does. Accepted
     formats: RFC 3339 (offset with or without a colon), `Y-m-d H:i:s`, `Y-m-d\TH:i:s`, `Y-m-d`, and
     epoch seconds of at least 9 digits (1973 onwards) and before year 10000. Smaller numbers are
     treated as unparseable.
   - If the webhook arrives before the browser returns, `/checkout/success` just shows the current
     state. It polls `/checkout/status/{id}` every 3 seconds with HTMX, and the poll answers `286`
     (stop polling) once the order is final.
5. **Account.** On `/account`, visitors request a magic link (`POST /account/login`, CSRF,
   rate-limited per IP and email). The lookup and the email run in the `commerce.magic_mail` job
   for every address, so neither the answer nor its timing reveals whether an email is known. Only
   customer accounts get links. Signed-in customers see their entitlements with status and end
   date. `POST /account/open/{entitlement}` (CSRF, ownership checked, rate-limited) calls
   `login-link` for an active entitlement whose period has not ended, and redirects with 303 to
   the returned https URL. WooCommerce's old account URL keeps working: at boot commerce seeds a 301
   `/my-account` (and `/my-account/`) → `/account` once, unless a redirect or a page already owns
   that path (deleting it is respected; the setting `commerce.my_account_redirect` records the seed).
6. **Extending access.** To extend, the customer buys again. The new entitlement starts at the
   later of now and the latest `ends_at` of that customer's pending or active entitlements on the
   same server.
7. **Expiry.** The daily cron `commerce.expire` moves `active` entitlements past `ends_at` to
   `expired` and enqueues `commerce.suspend`.

**Shared accounts.** The billing account is shared per customer (`external_customer_id = customer
id`). A suspension (refund, expiry or admin) is therefore skipped on the server while the customer
still holds another active entitlement on the same server. A resume is refused once the period has
ended; the customer buys again instead.

## mcp-billing/v1 protocol

GoRun talks to billing servers exactly like `class-mcbb-client.php` 0.3.1, so existing servers
work unchanged.

| Method | Path (below `/mcp-billing/v1`) | Purpose |
|---|---|---|
| GET | `/catalog` | Connection check and plans (`{product, plans:[{code,name,name_ar}], tools}`) |
| POST | `/accounts` | Create or extend an account |
| GET | `/accounts/{external_id}` | Reconcile (`status`, `period_end`) |
| POST | `/accounts/{external_id}/suspend` | Suspend (`{reason}`) |
| POST | `/accounts/{external_id}/resume` | Resume |
| POST | `/accounts/{external_id}/login-link` | Short-lived login URL (`url`) |

Every response is the envelope `{"ok":true,"data":…}`, where `data` is a JSON object or list
(PHP `is_array`). Anything else is an error. For non-2xx answers, the response body (up to 500
characters) is kept in the operation's `last_error`.

The `POST /accounts` body uses exactly the fields, field order and date format of
`MCBB_WooCommerce::process` (mcp-billing-bridge 0.3.1). `period_end` is the new absolute end of the
period, so a later end extends the account.

```json
{"external_customer_id":"<customer id>","order_ref":"gp_<order id>","plan_code":"pro","period_end":"2026-11-10T12:00:00Z"}
```

Validation follows PHP `valid_result`:
- create: `status ∈ {active, pending}`;
- suspend: `status ∈ {suspended, inactive}` (body `{"reason":"…"}`);
- resume: `status = active`.

### Signing

- **Body:** compact JSON with slashes and unicode unescaped (Go: `json.Encoder` with
  `SetEscapeHTML(false)`, trailing newline trimmed). A GET sends an empty body.
- **Target:** `/mcp-billing/v1` + path.
- **time:** unix seconds.
- **nonce:** 32 random alphanumeric characters.
- **String to sign:** `time + "\n" + nonce + "\n" + METHOD + "\n" + target + "\n" + hex(sha256(body))`.
- **Headers:** `X-MCBB-Client: <client_id>`, `X-MCBB-Timestamp`, `X-MCBB-Nonce`,
  `X-MCBB-Signature: hex(hmac_sha256(secret, string_to_sign))`, `Accept` and
  `Content-Type: application/json`. Every mutation also carries `Idempotency-Key`. Servers must
  replay the first response for a key.
- **Transport:** HTTPS on port 443 only. `GORUN_ALLOW_PRIVATE_NET=1` also allows http and private
  addresses, for the home lab. Requests time out after 15 seconds, never follow redirects, and go
  through an SSRF-safe dialer.

**Golden vector.** This is `testdata/mcbb_signature_vector.json`, checked by
`TestMCBBSignatureMatchesPHPVector`.

| Input | Value |
|---|---|
| time | `1700000000` |
| nonce | `abcdefghijklmnopqrstuvwxyz012345` |
| method | `POST` |
| target | `/mcp-billing/v1/accounts` |
| body | `{"a":1}` |
| secret | `secret` |
| sha256(body) | `015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862` |
| signature | `741d5808562352237a024a82eb5938edaaaf24469a056377d52e5ae496647177` |

Idempotency keys:

| Operation | Key |
|---|---|
| Create | `create:{order}` |
| Refund suspend | `refund:{entitlement}` |
| Expiry suspend | `expire:{entitlement}` |
| Admin actions | `suspend:…`, `resume:…`, `reconcile:…`, `login:…` with a fresh id each time |

Each operation is a `provisioning_ops` row: the request is frozen, and attempts and the last error
are recorded. Login-link responses are never stored.

## Stripe setup

1. In the Stripe Dashboard, in **test mode**, copy the secret key (`sk_test_…`).
2. Add a webhook endpoint. Use `https://<your site>/webhooks/stripe/test` with the test signing
   secret and `https://<your site>/webhooks/stripe/live` with the live secret. `https://<your site>/webhooks/stripe`
   follows the mode selected in store settings. Subscribe to:
   - `checkout.session.completed`
   - `checkout.session.async_payment_succeeded`
   - `checkout.session.async_payment_failed`
   - `checkout.session.expired`
   - `charge.refunded`
   - `customer.subscription.created`
   - `customer.subscription.updated`
   - `customer.subscription.deleted`
   - `customer.subscription.trial_will_end`
   - `invoice.paid`
   - `invoice.payment_failed`

   Copy the signing secret (`whsec_…`).
3. Set both keys in one of two ways:
   - the environment: `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` (or `[stripe]` in
     `gorun.toml`);
   - **Admin → Commerce → Store settings**.

   Values saved in the admin override the environment. They are stored with AES-GCM under
   `GORUN_SECRET_KEY`, can be set or cleared, and are never shown again.
4. To test locally, forward events with
   `stripe listen --forward-to localhost:8080/webhooks/stripe`, then pay with card `4242 4242 4242 4242`.

The signature is always verified, and without a webhook secret every delivery is refused with 503.
The API-version check is relaxed (`IgnoreAPIVersionMismatch`) because Dashboard endpoints often run
another version and GoRun reads only stable `data.object` fields.

## Settings

| Key | Meaning |
|---|---|
| `commerce.currency` | Store currency, default `AED`. Products may set their own. |
| `commerce.stripe_secret_key` | Encrypted (`db.Encrypt`, AAD = key). Overrides `STRIPE_SECRET_KEY`. |
| `commerce.stripe_webhook_secret` | Encrypted (`db.Encrypt`, AAD = key). Overrides `STRIPE_WEBHOOK_SECRET`. |
| `commerce.stripe_mode` | `test` or `live`; absent means live. Pinned explicitly by the bootstrap (new stores: `test`). |
| `commerce.stripe_test_secret_key`, `commerce.stripe_test_webhook_secret` | Encrypted test-mode secrets; no environment fallback. |
| `commerce.settings_version` | Bootstrap steps applied to this store (`1`: mode pinned). |
| `commerce.stripe_verify.<mode>` | Last read-only key check: key fingerprint, ok, livemode, time, error. Not a secret. |

Billing-server secrets are stored in `billing_servers.secret_enc`, encrypted with `db.Encrypt`
(AAD = server id).

## Admin, API, permissions

**Admin.** Admin screens live in the Commerce group:

- Products: list and editor, with group, plan, server, months, features, image, SEO, and price in
  major units. The description is HTML sanitized with the CMS policy and stored as safe HTML.
- Orders: list and detail with provisioning operations.
- Entitlements: suspend, resume, reconcile and login-link.
- Billing servers: CRUD and "Test connection" (`GET /catalog`, lists the plans).
- Store settings.

**Health.** System Health reports the active mode's Stripe key (configured / authenticated /
failed / mismatch) and webhook (secret set / signed delivery observed) plus one line per billing
server; the capability report (`system.capabilities.modules`) uses the same three states as reasons
`stripe_secret_key_missing`, `stripe_webhook_secret_missing`, `stripe_key_mode_mismatch`,
`configured_not_authenticated`, `authenticated_delivery_unverified` and `verified`. Checkout itself
is proven only by a real order. Server reachability is cached for 5 minutes.

**REST.** All `/api/v1/commerce/*` operations need `commerce.manage`, except listing orders and
entitlements, which needs `commerce.view_orders`.

- `products`
- `orders`
- `entitlements`, with `/{id}/suspend`, `/{id}/resume` and `/{id}/reconcile`
- `servers`, with `/{id}/test`

**Audit.** Every mutation writes an audit entry: `commerce.product.save|delete`,
`commerce.order.create|paid|refunded`, `commerce.entitlement.*`, `commerce.server.save|delete|test`
and `commerce.settings.save`.

## Theme integration

| Kind | Templates (then `index.html`) | `.Extra` |
|---|---|---|
| `shop` | `shop.html` | `groups` (`[]commerce.ProductGroup`), `title` |
| `product` | `product-{slug}.html`, `product.html` | `product` (`*commerce.Product`), `checkout_action`, `checkout_error`, `email`, `image_url`, `title` |
| `account` | `account.html` | `login_form`, `sent`, `entitlements`, `customer`, `account_error`, `title` |
| `checkout-result` | `checkout-result.html` | `order`, `poll`, `status_html`, `title` |

Every commerce page also sets `Extra.commerce = true`.

**Template funcs:**
- `products` returns active products grouped like `/shop`.
- `product "slug"` returns an active product, or nil.

Only themes rendered with commerce booted may call these funcs, because funcs resolve at parse time.
The default theme therefore uses `.Extra` only. Forms must post `_csrf` (`{{csrf}}`). The public
routes run through `auth.CSRFMiddleware`, so the token exists on every commerce page.

## Testing with `fakebilling`

`internal/commerce/fakebilling` is an `httptest` billing server. It implements every endpoint above
with:

- signature verification using the same scheme (client id, ±5-minute timestamp, HMAC);
- `Idempotency-Key` replay on mutations, with a `400` when the key is missing;
- failure injection: `srv.FailNext("POST", "/accounts", 503, 2)`, where a trailing `*` matches a
  path prefix; `ClearFailures()` drops pending ones;
- `POST /accounts` requiring the PHP body (`external_customer_id`, `plan_code`, `period_end`);
- `Calls()[i].RawBody`, which holds the exact signed bytes for golden-body assertions;
- `SetPeriodEndAnswer(v)`, which answers an odd `period_end` (an epoch number or garbage).

```go
srv := fakebilling.New("client-1", "secret")
defer srv.Close()
// register it: Server{BaseURL: srv.URL, ClientID: "client-1", Secret: "secret"} (needs AllowPrivateNet)
srv.Account(customerID) // inspect remote state; srv.Calls() / srv.CallCount(method, path)
```

The commerce tests boot the full stack with Stripe Checkout and mail faked. They stop the job
workers right after boot and run the job handlers directly, which keeps them deterministic. They
deliver real signed webhooks built with `webhook.GenerateTestSignedPayload`.
