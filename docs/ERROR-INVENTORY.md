# Application error inventory

`/admin/system/errors` is a read-only owner view. It requires the administrator role and `system.health`; API-key actors and delegated staff are denied. Its navigation entry follows the same restriction.

## What is covered

- Runtime `Error` log observations classified by a fixed component allowlist, recovered HTTP panics, and numeric HTTP 500–599 responses. Unrecognized errors use `application / runtime_error`. This is a coarse inventory, not a detailed debugger.
- Current failed, exhausted, or awaiting-retry job counts, with first/last update dates. It does not select job payloads, raw last errors, names, or credentials.
- Selected failure audit actions, including failed sign-in, temporarily blocked requests, denied setup, form notification failure, extension failure, service suspension failure, and unsuccessful MCP calls. Only action/count/date projections are returned; no audit metadata, private content, arguments, or actor details enter the inventory.

One incident can create several observations. Counts are not unique incidents. “Recorded” does not mean resolved. Successful or informational activity and unlogged application errors are outside this view. Runtime observations start when this feature starts; no historical raw-log import occurs. Existing jobs and audit retention remains unchanged.

Filters support source, fixed component, state, numeric 5xx status, and UTC dates. Links open the existing job/audit/health screens, whose own permissions and visibility still apply. The inventory never retries jobs, resolves events, or makes provider requests.

## Privacy and retention

The diagnostic table has only day, component, stable code, HTTP status, first/last time, and count. The collector discards raw log message text and attributes before persistence: no URL/query, request body, headers, stack, user name, private report, or provider response. Existing console logging remains unchanged; this feature does not claim to sanitize the entire process log stream.

Runtime storage is limited to 1,000 daily groups, with a 30-day retention target. Pruning happens on startup, each collected batch, and hourly during idle periods. Reads show at most 200 aggregate groups. A nonblocking queue holds at most 128 observations; overflow and storage failures have visible process-lifetime counters. Observations may be dropped under load or storage failure, so the UI explicitly does not promise an exhaustive inventory.

The collector uses its own bounded asynchronous transaction and never writes to the database while inside a log call. Storage failure cannot recursively log. Normal shutdown drains the queue before the database closes. Failed module registration closes the database without starting a collector goroutine. Kernel migration namespace `diagnostics` is reserved; migration 0001 is additive and does not alter existing tables.

## Validation

Synthetic tests cover classified persistence without secrets, queue overflow, retention/capacity, logging from an active write transaction, shutdown and unavailable storage, failed boot cleanup, owner-only route/nav, audit projection privacy, filter validation, and action links. Native browser acceptance verifies a real local HTTP502 observation, filters, EN/AR desktop/mobile layout, keyboard scrolling, and 44px form controls. No external provider or production mutation is needed.
