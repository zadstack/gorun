# Native customer and business workflows

GoRun provides password-based customer accounts, persistent agents and app subscriptions independently of the old WordPress plugin protocols. GoRun does not restore WordPress, automatically replay imported jobs, send test emails or claim that a pending service request is paid.

## Accounts

Enable `commerce.registration_enabled` as a JSON boolean through the settings store for sites that permit public customer registration. The default is false. Registration at `/account/register` is CSRF protected, bounded and rate limited; the server always chooses `customer`, does not grant an entitlement, and does not infer email ownership. `/account/password-login` uses native rate-limited authentication and starts a native session. `/account` shows only the signed-in customer's orders and commerce entitlements. Existing magic-link login remains available when mail is configured.

Migrated WordPress `$wp$` bcrypt and phpass passwords are verified with bounded costs, and upgraded to Argon2id on successful login with a compare-and-swap. Never replace customer passwords with a common password or grant all imported accounts administrator privileges. Matching an imported email requires deliberate source ownership and role mapping; the bootstrap account must remain separate.

## Service checkout

Service and digital products use `/cart/items`, `/cart/remove`, `/cart/clear` and `/checkout`. Prices and currency come from the database, not form input. Checkout requests are idempotent and create pending manual requests. A request does not charge a card, mark an order paid or grant a download. Provisioning products retain their separate Stripe and billing-server flow; configure and independently test the selected gateway before enabling it. Digital downloads still require actual payment, ownership and expiry checks.

Default theme product/cart/checkout/account templates support these routes; child themes may override them. Native API operations remain documented in `docs/API.md` and OpenAPI.

## Agents and subscriptions

See `internal/workagents/README.md` for the bounded execution registry, queue, owner-scoped API and persistent scheduling. See `internal/saas/README.md` for app and entitlement administration, quota consumption, optimistic edits, existing GoRun API-key scopes and preserved import history. Encrypted keys imported from an earlier WordPress subscriptions plugin are migration inputs only; native clients use GoRun API keys. Subscription ownership is explicit and unknown owners fail closed.

Import historical runs/artifacts/approvals with source identifiers and historical flags, without enqueuing work. Imported automations should remain paused until their action is mapped to a native execution kind. These are visible operator decisions, not silent background tasks.

## Verifying a deployment

Run the full Go suite, changed-package race checks, vet, API documentation checks and build. On a deployed site, use marked synthetic accounts and objects to verify password upgrade, registration role isolation, cart totals/idempotency, owned orders, subscription quotas/replay/conflicts and cross-user denial. Execute all configured agent types and a saved schedule with no open browser, and verify durable results. Test real gateways only with test-mode credentials; do not send real email or perform a real payment as a smoke check. Clean only test-owned objects after saving redacted proof.
