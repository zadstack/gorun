# Shared Core operations: requirements and status

This document tracks the Shared Core operations work after v0.14.1. The first
PR (#114) added site information and the capability report. The second PR
implements the staged update lifecycle and durable background observations and
records, per requirement, whether it is **complete**, **partial** or
**deferred**, with the test that proves it. Documentation or an announcement is
never evidence; only the listed tests and code are.

Status words: **مكتمل / complete** — implemented and covered by the named
tests; **جزئي / partial** — implemented with a stated limit; **مؤجل /
deferred** — not in this PR, listed for the next one.

## Reused components

| Component | Reused for | Change in this PR |
| --- | --- | --- |
| `internal/jobs` queue (idempotency keys, retries, heartbeats, stale recovery) | Observation refreshes (`observe.<kind>`), the lifecycle runner (`update.run`) | Workers claim only registered kinds; `Park`/`Unpark`, `Parked`, `Unregistered`, `Get`, `InFlight`, `RunOne`; `parked_reason` column (additive migration `0002`). |
| `internal/backup` + `verifiedBackup` | Backup phase | Unchanged; called from the admin evidence provider. |
| `internal/admin/updates.go` (Coolify dispatch) | `update.CoolifyAdapter` | Same validation of origin/token/resource and acceptance rule; adds `/api/v1/deployments/{id}` status. |
| `internal/admin/health.go` | Health phase evidence; saved observations and parked counts on the page/API | Reads only; no external call added. |
| `kernel.SchemaInspector`, `schema_migrations` | Compatibility and migration-state evidence | Unchanged. |
| `kernel.OptionalModule` boot gate | Disabled capability policy | Unregistered kinds are parked, not failed. |

## 3. Staged update lifecycle

| # | Requirement | Status | Evidence |
| --- | --- | --- | --- |
| 3.1 | Phases: compatibility, release verification (immutable artifact + digest), backup, activation, health and links, final acceptance | complete | `internal/update/update_test.go: TestCompleteUpdateAcrossProcessRestart`, `TestPhaseFailuresStopBeforeSideEffects`; `internal/admin/updates_test.go: TestStagedUpdateLifecycleAcrossRestartThroughAdmin` |
| 3.2 | Record status, start/end, duration, progress (null when unmeasurable), result, retryability and remediation per phase | complete | same tests (phase fields asserted); OpenAPI `Phase` schema |
| 3.3 | Distinguish requested, installed/activated, actually running and accepted versions | complete | `Operation.requested_version / running_version / activated_version / accepted_version`; `TestCompleteUpdateAcrossProcessRestart` asserts a queued deployment has neither activated nor accepted |
| 3.4 | Sending a deployment request is not success | complete | `TestCompleteUpdateAcrossProcessRestart` (`deployment_ref` set, activation still running); `TestRequestReconcilesAcceptedDeploymentBeforeDispatching` |
| 3.5 | Durable conflict protection: ownership token, heartbeat, recovery; structured `409` with operation reference; covers backup/restore | complete | `TestLeaseBlocksConflictingWorkAndFencesExpiredOwners`, `TestConcurrentRequestsAdmitExactlyOneOperation`, `TestRunnerStopsWhenLeaseIsStolen`; admin `TestUpdateRequestValidationAndLeaseConflicts`, backup refusal in `TestStagedUpdateLifecycleAcrossRestartThroughAdmin` |
| 3.6 | Retries reconcile accepted deployments before dispatching again; no duplicate dispatch | complete | `TestRequestReconcilesAcceptedDeploymentBeforeDispatching`; single `Activate` call asserted after restart |
| 3.7 | Interrupted process: terminal `interrupted`, never permanently “in progress”/successful | complete | `TestRestartDuringEarlyPhaseIsInterruptedNotSuccessful`; admin `TestInterruptedUpdateIsReportedNotAssumedAfterRestart` |
| 3.8 | Restart during activation resumes from persisted state; identity confirmed by the serving process | complete | `TestCompleteUpdateAcrossProcessRestart` (`serving_identity_after_restart`), boot `Reconcile` in `internal/admin/module.go` |
| 3.9 | Runtime rollback of a compatible prior artifact on current data; never restore an old snapshot over newer writes; stop when compatibility is uncertain | complete | `TestHealthFailureRollsBackToPriorArtifactOnCurrentData`, `TestRollbackRefusedWhenActivationAppliedMigrations`, `TestHealthFailureWithoutPriorArtifactFailsWithGuidance`; admin test asserts no `restores/` directory |
| 3.10 | Adapter contract for a local supervisor or configured provider; no hosts/credentials from requests | complete | `update.Adapter`/`update.Source` interfaces, `ManualAdapter`, `CoolifyAdapter`, `ReleaseIndex`; `TestCoolifyAdapterNeverLeaksCredentialsAndRequiresResourceAcceptance`, `TestReleaseIndexResolvesVerifiedArtifacts` |
| 3.11 | Compute “latest” from the serving identity and a verified release observation with observed time/source; unknown for dev builds or missing source | complete | `internal/admin/updates.go: updateStatus`; admin `TestUpdatesDisabledUntilOperatorConfiguration` (unknown), `TestStagedUpdateLifecycleAcrossRestartThroughAdmin` (fresh + `update_available`) |
| 3.12 | Incompatible schema/theme/module | complete (data-dependent) | Schema-ahead and development builds fail (`updateCompatibility`, `TestPhaseFailuresStopBeforeSideEffects`). With release-index `migrations`/`themes`, the target is checked before backup (`TestReleaseCompatibilityDataGuardsSchemaAndTheme`); without them the phase records `release_data: unknown`. |
| 3.13 | Downgrade requires review | complete | `TestPhaseFailuresStopBeforeSideEffects/downgrade`, `TestDigestMismatchAndDowngradeConsent` (operator consent recorded) |
| 3.14 | Secrets redacted; optional provider absent | complete | admin tests assert the source token and URL never appear in pages or API bodies; manual adapter is the default (`TestNotConfiguredAndInvalidTargets`) |
| 3.15 | Rehearse old/new binaries on the same migrated fixture | partial | Simulated by two services with different `RunningVersion` on one database; a real two-binary rehearsal is manual (`docs/UPGRADE.md` §5). |
| 3.16 | Coolify adapter pins the requested artifact or refuses | complete | Docker image resources are pinned (`PATCH docker_registry_image_name/tag`, confirmed by read-back) before deploy; git-built resources are refused at verification before any backup (`TestCoolifyAdapterPinsImageTagOrRefuses`, `TestUnpinnableTargetIsRefusedBeforeBackup`). |

## 4. Heavy reads in the background

| # | Requirement | Status | Evidence |
| --- | --- | --- | --- |
| 4.1 | Pages serve the last saved result immediately with read time and state | complete | `internal/observe/observe_test.go: TestRefreshReturnsAtOnceAndReadsServeSavedResults`; commerce `TestHealthBillingServerUsesBackgroundObservation` (no catalog call during health) |
| 4.2 | Expensive external checks move to durable background jobs | complete | `update.release` and `commerce.billing_server` kinds; `observe.Tick` cron; `TestObservationsAPIRefreshesInBackgroundOnly` |
| 4.3 | Timeouts, bounded retries, deduplication, concurrency limits | complete | `TestTimeoutAndRetryExhaustionShowFailureWithTime`, `TestConcurrentRefreshesAdmitOneJob` (100 → 1 job), `TestDisabledCapabilityIsNeitherScheduledNorRun` (`MaxInFlight`) |
| 4.4 | Discard stale results when configuration changed during the check | complete | `TestConfigurationEditsRejectLateResults` (before and during the run; removed key) |
| 4.5 | Show errors and stale results clearly; no fake success; no zeros for unavailable data | complete | failed attempt keeps `saved` with age; null `duration_ms`/`age` until measured; `status` enum in OpenAPI |
| 4.6 | Process interruption recovers work | complete | `TestInterruptedRefreshRecoversAndCompletes` |
| 4.7 | Disabled modules do not run their work; queued work is parked and preserved for re-enable | complete | `internal/jobs/queue_test.go: TestParkHoldsQueuedWorkAndUnparkReleasesItUnchanged`, `TestPanicCountsAsFailureAndMissingHandlerWaits`; observe `TestUnregisteredKindJobsAreParkedThenReleasedOnReenable` (boot) and `TestDisabledCapabilityParksQueuedWorkWhileWorkersRun` (runtime disable/enable with live workers) |
| 4.8 | Unauthorized callers cannot refresh or read | complete | admin `TestObservationsAPIRefreshesInBackgroundOnly` (editor 403), `TestUpdatesDisabledUntilOperatorConfiguration` |
| 4.9 | Connection probes (AI/MCP/S3) as background observations | deferred | The explicit connection test POST still runs in the request (25 s timeout) and saves its evidence as before; moving it behind an observation kind is the next PR together with operation-level tests (section 2). |
| 4.10 | Health hook checks of other modules (commerce) | complete | billing-server reachability moved; Stripe presence checks remain local reads |

## 2. Capability evidence (next PR)

| # | Requirement | Status | Note |
| --- | --- | --- | --- |
| 2.1 | Separate available / enabled / configured / connected / operation-tested | partial | Available, enabled, configured and connectivity exist (#114). Operation-tested evidence is not produced yet. |
| 2.2 | Optional, safe operation tests with explicit permissions and isolated samples | deferred | Requires per-integration test operations (send a test mail to the operator, write/delete a probe object, dry-run a tool) with a cleanup policy. |
| 2.3 | Bind evidence to configuration revision and test time; invalidate on change/expiry | partial | Connection probes carry `test_version` (#114); observations carry revision and freshness (this PR). Capability report does not yet surface observations. |
| 2.4 | Module configuration report with failure cause and repair step | partial | Observations and update phases carry `remediation`; module-level configuration requirements are still not published by every module. |
| 2.5 | Disabled modules stop consuming their jobs safely | complete | See 4.7. |

## 1. Identity and appearance (next PR)

| # | Requirement | Status | Note |
| --- | --- | --- | --- |
| 1.1 | Theme logos: primary, dark, light, icon with site-setting precedence | deferred | `GET /api/v1/site/info` reports the existing logo only. |
| 1.2 | Logo available through the API for every site that renders one | deferred | Needs the resolution order used by templates exposed through the same service. |
| 1.3 | Declared vs effective colours and fonts after site customisation | deferred | — |
| 1.4 | Setting overrides and custom CSS with per-value provenance; `unavailable` when undecidable | deferred | — |
| 1.5 | Keep requests light; no browser rendering | complete by construction | Nothing in the current API renders a page. |

## Verification baseline

Commands run for this PR and their results are recorded in the pull request
description. The expectation stays: focused success/failure/concurrency tests,
then `go test ./...`, `CGO_ENABLED=0 go build ./...`, `make build`; CI adds the
race-enabled suite, vet, staticcheck, vulnerability checks and Playwright.
Fixtures and fake upstream servers only; no real provider credentials.
