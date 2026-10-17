# Local memberships

Memberships are explicit local grants, separate from payment processing and SaaS external signatures. No payment, mail, automatic renewal or historical purchase replay occurs. The module ships with **no policies or plans**; other sites keep their existing behavior until an operator configures them.

## Data and state

- `membership_plans`: source-defined plan IDs and numeric content-type limits. Missing limits never create unlimited access.
- `membership_grants`: immutable owner, source reference, plan, status, period, grace end and optimistic version. Preserve source timestamps during migration; do not reset them to migration time.
- `membership_policies`: explicitly scoped content types, free allowance and explicit bypass permission. Standard role permissions are not broadened by a grant. Reviewers require an explicitly assigned `membership.unlimited` permission; administrators already hold wildcard permissions.
- Active grants work from `period_start` until `period_end`, then through explicit `grace_until`. `pending-renewal` follows the same bounds. `cancelled` means renewal cancellation, retaining prepaid access through the recorded period/grace. `suspended` revokes immediately, and `expired` never grants paid capacity. No timestamp is extended automatically. An active grant with no end is explicitly lifetime; cancellation/pending-renewal require an end.
- Effective status is calculated on every read/check. An expired grant can retain its historical stored status without displaying or enforcing active benefits.

## Routes

`GET /membership` displays the signed-in account's grants, effective state, quotas and submissions. `GET /admin/membership` requires `membership.manage` and provides version-checked editing. Admin forms require CSRF.

APIs under `/api/v1/membership`:

- `GET /`: owned grants and quotas; API keys need `membership.read` or management scope.
- `GET /manage`: management overview.
- `POST /plans`: explicit plan configuration; management only.
- `POST /grants`, `PUT /grants/{id}`: explicit grant changes, not a payment assertion. Updates require `expected_version`; owners and source references cannot be reassigned.
- `GET /submissions`: only the actor's policy-scoped content.
- `POST /submissions`: `{type,title,body}` only, with `membership.submit`; owner is forced to the actor and status to `pending`. Unknown author/status fields are rejected by JSON validation.
- HTML `POST /membership/submissions` provides the same restricted operation with CSRF. Customers receive no general `content.create` or `content.publish` permission.

## Atomic enforcement

`content.authorize_save` runs inside the native SQLite write transaction after content normalization and before insertion. Membership counts all non-trash content of the target owner/type, including draft/pending/private. Concurrent insertion, translation creation, restoring trash and author reassignment cannot bypass a quota. Existing occupied slots can still be edited after expiry or after exceeding a reduced limit. No-policy content types retain existing behavior; an anonymous/system write to a configured type has no automatic unlimited exemption. Trusted migration code should deliberately provide a real authorized administrator context.

The hook payload `content.SaveAuthorization` exposes the transaction and normalized item to trusted modules only. It supplements native ownership/permissions. Modules must not retain or commit its transaction.

## Verification

`go test -race ./internal/membership ./internal/content ./internal/kernel` verifies real SQLite concurrent quotas, restore, paid/grace/expired states, explicit bypass, owner isolation, disabled accounts, management CAS, API scope checks, CSRF and restricted sanitized customer submission. Site acceptance still must check actual migrated ownership/periods and customer-facing routes before activation.
