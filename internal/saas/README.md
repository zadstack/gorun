# SaaS subscriptions

This module stores migrated application subscriptions separately from commerce server provisioning. It does not call external APIs, send mail, initiate payments or replay historical tasks.

Register `saas.New()` in the kernel module list and add `saas` after `commerce` in `kernel.BootOrder`. It imports auth/api and shares only kernel infrastructure. The service is available as `kernel.Get[*saas.Service](d, "saas")`.

Authenticated routes:

- `/saas`: current account applications, subscriptions and usage.
- `/admin/saas`: management overview; `saas.manage` required.
- `GET /api/v1/saas`: owner-scoped JSON overview.
- `GET /api/v1/saas/manage`: management overview.
- `POST /api/v1/saas/entitlements/{id}/consume`: owner-scoped atomic quota consumption. Body: `{"metric":"tasks","quantity":1,"idempotency_key":"unique-request-at-least-16-characters"}`.
- `PATCH /api/v1/saas/entitlements/{id}`: management status compare-and-swap. Body: `{"expected_status":"active","status":"suspended"}`.

Import uses `saas_` table names; exact field definitions are in the migration. All native IDs are independent strings, source IDs unique and nullable. Source app references must resolve to native IDs. `owner_id` resolves the original user to a native account and remains null when unknown; never default unresolved subscriptions to a random or bootstrap user. `external_id` and usage `subject_id` retain the original subject identifier, so imported quota totals match subscriptions. Public IDs retain original values. Manifest/features/limits preserve JSON objects; source byte/schema records remain separately encrypted as compatibility input.

Native management APIs:

- `POST /api/v1/saas/apps`: owner_id, name, slug, optional base_url, status(active/disabled), mode(subscription/external), idempotency_key (16–200 chars).
- `PATCH /api/v1/saas/apps/{id}`: same configuration plus expected_version from overview.
- `POST /api/v1/saas/entitlements`: app_id, owner_id, plan_id, status, features(boolean map), limits(integer metric map), optional valid_until(RFC3339), idempotency_key.
- `PUT /api/v1/saas/entitlements/{id}`: same subscription configuration plus expected_version; app and owner remain fixed and usage persists.
- `DELETE /api/v1/saas/apps/{id}` or `/entitlements/{id}`: unused native objects only; imported or referenced history must be disabled/revoked instead.
- `/admin/saas/new`: CSRF-protected app and subscription creation forms. Management overview includes app editing and subscription state changes.

The module refreshes role permissions after seeding customer read/use scopes. Local subscription mode requires no external provisioning connection; external mode still reports missing fulfillment configuration.

Key version ciphertext, IV, tag and KDF context are retained privately, never included in API/UI structures. Legacy transferred keys are retained as archived migration data; use existing GoRun API keys with `saas.read`, `saas.use` and `saas.manage` for native functionality. Imported key protocols default to `legacy-unconfigured`; nonce validation refuses these keys. The original plugin's signature/KDF source implementation was not present in the transferred executable plugin manifests, so accepting those external client signatures requires a separately verified adapter. `ConsumeNonce` guards replay for a configured protocol; it does not verify a signature itself. `AcceptEvent` is an internal owner-scoped ledger helper and is not exposed as an unauthenticated webhook.

Quotas are numeric metric-to-limit JSON objects. Missing, negative or invalid quotas fail closed. Active subscription and unexpired validity are required. Database IMMEDIATE transactions serialize quota writes and ledger operations; identical idempotency keys replay the stored result and changed payloads return conflict. Historical event/nonce rows remain preserved.

Tests: `go test -race ./internal/saas` verifies ownership, disabled/anonymous accounts, public UI content, quota atomicity/concurrency, idempotent replay/conflicts, expiry, inactive status, replay ledgers, unconfigured/revoked keys and management CAS. Production migration and live UI/API acceptance are separate operator steps.

### Recorded owner services

`PATCH /api/v1/saas/entitlements/{id}/services` requires `saas.manage` and
`expected_version` from the latest overview (including a legacy empty version).
Example: `{"expected_version":"current-version","renew_months":1,"central_outbound_mail":true,"existing_domain_linked":true}`.
Optional `status` accepts the existing subscription states. Renewal accepts 1–36
calendar months; omitted/zero leaves expiry unchanged. It extends the later of
now and current expiry, clamps the day at month-end, and does not activate a
suspended subscription unless `status` is explicitly supplied. At least one
change is required. A stale version returns 409; refresh before another change.
Ownership, plan, quotas, usage, unrelated feature fields and payment markers are
preserved. Changes are audited. These flags record service entitlements only:
this endpoint does not provision mailboxes, register domains, change DNS or
charge/record payment.

### Renewal visibility

Both overview APIs include `entitlements[].renewal`: `state`, nullable
`remaining_seconds` and `remaining_days`, `reminder_days` (7), and `observed_at`
(UTC RFC3339). `valid_until` remains the original expiry field. Empty expiry is
`unscheduled`; malformed expiry is `invalid`; both return null remaining values.
Expiry at or before the observation time is `expired` with zero remaining.
Positive remaining seconds and days are rounded up; at most seven days until
expiry is `due_soon`, otherwise `current`. One observation time is shared by all
subscriptions in a response. This read-only metadata does not send messages,
renew a subscription, or activate suspended/revoked subscriptions: always show
`status` separately. OpenAPI schemas are generated from these response types.

Both overview endpoints accept optional `renewal_state`: `needs_renewal`
(`due_soon` or `expired`), `due_soon`, `expired`, `current`, `unscheduled`, or
`invalid`. Example: `GET /api/v1/saas/manage?renewal_state=needs_renewal`.
Unknown or repeated values return 422; omitted/empty preserves the existing
response. Filtering narrows the authorized overview snapshot (the existing
limit is 500 subscriptions) to matching entitlements and their related apps
and usage subjects. It does not change subscription status, send reminders,
charge renewal, or expose other owners' records. Expiry filtering includes
suspended/revoked entitlements when their dates match; clients must also display
the independent subscription status. No-match results use empty arrays.
