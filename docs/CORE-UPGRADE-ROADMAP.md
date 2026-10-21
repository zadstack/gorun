# Shared Core operations: follow-up work

The first PR covers site information and an accurate capability report. It does
**not** implement a staged updater, move existing expensive reads to background
jobs, or certify external operations from connectivity checks. This document
records the existing building blocks and acceptance criteria for those next PRs.
No deployment or data restoration is performed by the discovery APIs.

## Existing components to reuse

| Component | Current behavior | Remaining gap |
| --- | --- | --- |
| `cmd/gorun/main.go`, `pkg/cli/options.go` | Build version flows through `cli.WithVersion` into `api.CoreVersion`. | Latest-release comparison must use the serving process identity, never a selected artifact, saved target or feature-consent flag. |
| `internal/admin/updates.go` | Serializes backup/dispatch in one handler, persists a ten-minute cooldown, verifies a backup, then requests an operator-configured HTTPS Coolify deployment. | No durable phase history, artifact verification, activation confirmation, health acceptance or runtime rollback. Upstream acceptance only means queued. |
| `internal/backup` and `internal/admin/backups*.go` | Backup, inspection, confined restore and backup scheduling already exist. | Coordinate update, backup and restore operations with durable ownership; restoring a snapshot must never be an automatic runtime rollback. |
| `internal/jobs` | SQLite queue, unique idempotency keys, bounded exponential retries, atomic claims, worker heartbeats and abandoned-job recovery. | Add refresh-specific deadlines, refresh generations, persisted result freshness and a policy for paused/disabled capability jobs. |
| `internal/admin/connections_*.go` | Saved probe time/status; AI model listing, MCP tool discovery and S3 bucket probes; safe clients and response limits; stale probe writes reject changed/removed configuration. | The explicit test POST waits for external work. Probe success is discovery/connectivity evidence, not permission to generate, invoke tools or write objects. |
| `internal/admin/health.go` | Runtime and local storage/queue checks plus module health hooks. | Reads and hooks run in the request. Expensive checks need cached results with their own timestamps and errors. |
| `internal/db` and `docs/ARCHITECTURE.md` | Additive migration enforcement, checksums and schema-ahead inspection. | Compatibility decisions must include feature, theme and security behavior, not only additive SQL. |

The existing queue's idempotency key remains attached to completed jobs. A
refresh job therefore needs a configuration revision and refresh generation (or
an equivalent atomic in-flight uniqueness scheme); a fixed key would suppress
all subsequent refreshes. Workers currently claim jobs without filtering to
registered handlers, so merely unregistering a disabled module's handler does
not preserve its queued work unchanged. Address that behavior explicitly before
using the queue for optional probes.

## Follow-up PR: cached background observations

1. Persist an observation per capability/configuration revision with last attempt,
   last successful read, duration, evidence scope, result and sanitized failure
   reason/remediation. Represent never-read, waiting, running, stale and failed
   independently from the last saved result. Missing numeric values are null or
   unavailable, never an invented zero.
2. Page and read-only API requests serve bounded local reads of saved observations.
   A refresh request or scheduler enqueues work and returns immediately with a
   task reference. Keep the last successful result visible with its age when a
   newer attempt fails; never label the newer attempt successful.
3. Reuse the queue with configurable, bounded timeout, retry count and freshness
   interval. Deduplicate concurrent refreshes atomically. Fence result writes by
   configuration revision so a late result cannot certify edited credentials,
   recreate a removed profile or alter feature consent.
4. Distinguish read-only connectivity/discovery tests from explicit operation
   tests. Record precisely what ran and its result. Tests with external side
   effects require the relevant operation authorization and a cleanup policy;
   ordinary page loads must never send mail, charge money or mutate remote data.
5. Disable scheduling and worker registration for disabled capabilities; retain
   their configuration, data and last observations. Park already queued jobs
   without burning retries, and define safe re-enablement behavior.
6. Document refresh and observation endpoints under `/api/v1`, including OpenAPI,
   scopes, structured errors, examples, timestamps and stale-result semantics.
   External services remain optional; local-only installations work without them.

Acceptance tests: cold-cache pages make no outbound calls; stale-cache pages
return promptly during a blocked upstream request; timeout/retry exhaustion
shows a failure with its time; one hundred concurrent refresh requests create
one active job; process interruption recovers work; configuration edits reject
late results; disable/re-enable preserves rows and prevents execution while off;
unauthorized callers cannot refresh or read private details; no secrets appear
in responses, logs or persisted errors. Test upgrades from existing saved probe
records and distinguish unknown historical fields from successful evidence.

## Follow-up PR: staged update lifecycle

Implement a persisted state machine with these independently reported phases:

| Phase | Completion evidence |
| --- | --- |
| Compatibility | Target supports the current schema, configuration, modules, themes and runtime; downgrade constraints are recorded. |
| Release verification | Resolved immutable artifact identity and verified digest against a trusted, operator-configured release source. |
| Backup | Existing backup machinery returns an inspected manifest and retained recovery material; failure blocks activation. |
| Activation | Deployment adapter confirms the target process/artifact became active. A queued deployment is insufficient. |
| Health and links | The new process reports its expected identity; readiness, protected checks and configured public/media links pass. |
| Final acceptance | All required checks pass and the operation is durably committed. Feature consent and migration acceptance remain separate fields. |

Record phase status, start/end, elapsed duration, meaningful progress (unknown
when unmeasurable), result, retryability and sanitized remediation. Include a
terminal interrupted/failed state; do not leave a restarted process appearing
permanently successful or in progress without reconciliation.

Use durable conflict protection covering activation, rollback and conflicting
backup/restore/cutover work, with an ownership token, heartbeat and recovery
rules. Keep the data-directory lock. Reject a conflicting operation with a
structured `409` and operation reference; a process-local mutex or time cooldown
alone is insufficient. Retries must reconcile accepted deployments before
dispatching again.

Provide an adapter contract for a local supervisor or configured deployment
provider. Do not require Coolify or a public release service. Configure trusted
sources, supported targets, verification policy, health/link checks and timeouts;
do not accept arbitrary deployment hosts or credentials from request parameters.

Automatic failure handling may restore a **compatible prior runtime artifact on
current data**. It must never restore an old database/media snapshot over writes
made after the backup. If compatibility is uncertain, stop activation, retain
current data and require recovery or a forward repair. Disaster recovery restores
to a separate directory and requires explicit reconciliation/cutover. See
[UPGRADE.md](UPGRADE.md#6-rollback).

Compute “latest” from the actual serving identity and the most recent verified
release observation, with observed time and source. Report unknown when the
release source is unavailable, stale, untrusted or cannot compare a development
build. Do not conflate latest, feature acceptance, migration state, queued target
or completed deployment request.

Acceptance tests: complete success; incompatible schema/theme/module; unknown
target/digest mismatch; missing backup or corrupt manifest; backup disk failure;
dispatch timeout after upstream acceptance; failed readiness or broken media
link; restart during every phase; duplicate and conflicting requests; expired
owner fencing; runtime rollback preserving writes made after backup; unsupported
downgrade requiring repair; retained site/theme overrides and disabled-module
data; secrets redacted; optional provider absent. Test additive migrations and
rehearse old/new binaries on the same migrated fixture without restoring stale
data. Document OpenAPI, permissions, upgrade/recovery procedures and limitations.

## Verification baseline

Use repository fixtures and fake upstream servers; acceptance must not require
real provider credentials. Run focused success/failure/concurrency tests, then
`go test ./...`, `CGO_ENABLED=0 go build ./...` and `make build`. CI additionally
runs the race-enabled full suite with coverage, migration checks, vet,
staticcheck, vulnerability checks and Playwright. Record the commands actually
run and their results in each PR; this roadmap does not claim that future tests
or lifecycle stages have been implemented.
