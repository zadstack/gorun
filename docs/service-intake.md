# Service request intake

Products may define an optional `intake` array through the authenticated commerce products API. Each field has a unique lowercase `key`, a plain-text `label`, `required`, and `max_length` (1–4000 Unicode characters). A schema allows at most 12 fields. Existing products default to an empty schema; saving through the native product editor preserves API-configured fields.

`POST /api/v1/commerce/requests` accepts an `intake` map keyed by product ID, then field key. The native cart checkout generates corresponding form inputs. Missing required values, unknown product/field keys, and excessive lengths return validation errors before recording an order. Item quantity shares the same brief; separate briefs require separate requests.

The order line freezes both labels and answers alongside the price snapshot. Reusing an idempotency key with different answers is rejected. Later product changes cannot rewrite submitted briefs. Authenticated staff can read them through order details, and customers can read their own through `/account`; another customer's account cannot access them.

This is a **manual request**, not payment or automatic delivery. The payment endpoint without an intake payload rejects intake-configured products. Fulfillment transitions remain operator actions and never set payment status. No AI job is automatically launched by intake. A workflow integration must separately require genuine verified payment when applicable, enforce ownership and provider budgets, and require human approval before delivery.

Acceptance: `go test -race -count=1 ./internal/commerce/...`. `TestServiceIntakeValidationSnapshotAndOwner` covers required/length/unknown-field validation, snapshot immutability, replay, owner isolation, payment bypass rejection and absence of email/payment calls. `TestServiceIntakeHTMLAndSchema` covers form generation and malformed schema rejection. Existing commerce tests cover HTTP price tampering, CSRF, request replay, fulfillment state, and digital download payment/ownership boundaries.
