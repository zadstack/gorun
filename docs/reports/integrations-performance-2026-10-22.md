# Integrations performance measurements — 2026-10-22

Measured locally on Apple M4 Pro, macOS arm64, Go 1.27.2, benchmark default GOMAXPROCS=12. These are development-machine measurements, not production latency guarantees. Baseline was taken before HTML inventory pagination and shared-snapshot Worker revision loading; final measurements include those changes and the revised integrations UI. Other development activity on the machine can affect timings.

## Method

`internal/admin/integrations_performance_test.go` boots the actual admin module with SQLite in a fresh temporary directory and job workers disabled. It saves synthetic R2, D1 and Workers inventories directly, with a fake encrypted token. The identity scenario also saves 1000 Worker identity observations. A guard fails if GET constructs a Cloudflare client; the regression test also checks that GET enqueues no Cloudflare work.

`BenchmarkIntegrationsGETSavedInventory` runs the GET handler, saved-data loading and template rendering into an HTTP recorder. Warm-up and fixture setup are outside the timed loop. It excludes login, HTTP middleware, transport, TLS, browser DOM/layout and provider latency. `BenchmarkIntegrationsReportSavedInventory` isolates report building from HTML rendering. Each scenario was measured three times with a one-second benchmark target. No timing threshold is asserted by tests.

Reproduce from the repository root:

```sh
go test ./internal/admin -run '^$' -bench '^BenchmarkIntegrations(GET|Report)SavedInventory$' -benchmem -benchtime=1s -count=3 -timeout=120s
go test ./internal/admin -run 'TestIntegrations(LargeSaved|InventoryPresentation|InventorySearch|CorruptCloudflare)' -count=1 -timeout=90s
```

## Results

Ranges are the observed minimum and maximum across three runs; milliseconds per operation. “Per kind” means that count in each of R2, D1 and Workers.

| Handler scenario | Baseline ms | Final ms | Baseline response bytes | Final response bytes |
|---|---:|---:|---:|---:|
| Empty inventory | 1.760–1.798 | 1.847–2.240 | 38,064 | 40,920 |
| 100 per kind | 4.946–5.014 | 5.095–5.159 | 241,919 | 243,579 |
| 1000 per kind | 32.325–33.010 | 6.396–6.518 | 2,053,628 | 244,218 |
| 1000 per kind + 1000 identities | 55.158–55.603 | 8.781–8.905 | 2,247,628 | 263,872 |

| Report-only scenario | Baseline ms | Final ms |
|---|---:|---:|
| Empty inventory | 0.320–0.324 | 0.324–0.356 |
| 1000 per kind | 1.644–1.943 | 1.640–1.743 |
| 1000 per kind + 1000 identities | 21.248–21.333 | 3.921–4.250 |

For the large identity scenario, GET allocated approximately 37.86 MB/op and 762,781 allocations/op before; final measurements were 9.76–9.77 MB/op and 115,497–115,504 allocations/op. Report-only allocations fell from approximately 9.95 MB/op and 130,477 allocations/op to 5.40 MB/op and 28,527–28,528 allocations/op. MB here is decimal bytes divided by 1,000,000.

The small scenarios show modest additional UI/report overhead, not a universal speedup. The largest gains apply to populated accounts: the page renders at most 100 items per kind, and Worker identity revision checks reuse the report's Cloudflare snapshot through `observe.Service.ListWithRevision` instead of rereading configuration for every identity. The report/API still loads the complete saved inventory; pagination only shapes HTML presentation.

The full sweep above was measured after pagination and shared revisions landed. The largest identity scenario was rerun three times against the final code after the remaining correctness changes; its row and allocation figures reflect that final rerun. Earlier small-case rows are checkpoint measurements and may differ slightly in response size on the final code.

## Acceptance and limits

Focused tests passed after the UI update. They verify first/last-page navigation and cross-page server search; first-page HTML below 500,000 bytes for the 3000-resource identity fixture; negative, past-end and overflow page inputs; Arabic and accented search; URL and HTML escaping; provider count/completeness retained independently of local pagination; and HTTP 200 with storage/mail retained when Cloudflare configuration contains an invalid stored type. Invalid stored values and the fake token are not rendered.

Remaining costs scale with full saved inventory decoding and identity loading. Response-size reduction does not by itself establish browser responsiveness or production network timing. Browser smoke checks, full-suite results and deployment evidence belong in the PR handoff, not these microbenchmark results. No production credentials, provider calls or deployments were used for these measurements.
