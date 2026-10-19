# GoRun load harness / اختبار الحمل

Operators should **measure** RAM, CPU, latency, and concurrent readers against a
real local SQLite instance. A Go binary alone does not prove those numbers.

المُشغّلون يقيسون الذاكرة والمعالج وزمن الاستجابة والقراء المتزامنين على
نسخة SQLite محلية حقيقية. ثنائي Go وحده لا يضمن هذه الأرقام.

## Inventory (what already existed)

| Kind | Location | Notes |
| --- | --- | --- |
| Micro-benchmark | `internal/i18n.BenchmarkNormalizeArabic` | String fold only; not site load |
| Integration HTTP | `pkg/cli` `bootFull` + `httptest` | Correctness, not sustained concurrency |
| Browser e2e | `e2e/` (Playwright) | Functional journeys; serial workers |
| External load tools | — | None (no k6 / vegeta / hey in-tree) |

This harness is an **in-house** Go tool under `tools/loadtest` (stdlib only).

## Quick run

From the repository root:

```sh
make loadtest
# equivalent defaults:
go run ./tools/loadtest -concurrency 25 -duration 15s -posts 40
```

With a light write mix (PATCH one seeded post) and JSON output:

```sh
go run ./tools/loadtest -concurrency 40 -duration 20s -write-mix 0.05 -json
```

Against an already-running instance (seed + writes need an API token):

```sh
go run ./tools/loadtest -base-url http://127.0.0.1:8080 -token "$GORUN_API_TOKEN" \
  -concurrency 40 -duration 30s -posts 20 -write-mix 0.05
```

Keep the temp data directory for inspection:

```sh
go run ./tools/loadtest -keep-data -duration 10s
# or: -data /tmp/gorun-load
```

## What it does

1. Builds `bin/gorun` if needed (`CGO_ENABLED=0`).
2. Creates a temp data dir, admin user, and high-rate API key.
3. Starts `gorun serve` on a free localhost port (real SQLite).
4. Seeds N published posts via `POST /api/v1/content`.
5. Runs N concurrent clients for the given duration against:
   - `/`, `/healthz`, `/readyz`, `/sitemap.xml`, `/robots.txt`
   - each seeded `/post/loadtest-post-…`
   - optional PATCH writes (`-write-mix`) on one seeded item
6. Samples the **server process** RSS and approximate CPU% from `/proc` (Linux).
7. Prints p50 / p95 / p99 latency, error rate, throughput, RSS, CPU.

CI does **not** run this by default (host noise would flake). Use `make loadtest`
locally or on a dedicated runner when comparing builds.

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-concurrency` | `25` | Concurrent clients |
| `-duration` | `15s` | Steady-state window |
| `-write-mix` | `0` | Fraction of requests that PATCH (0–1) |
| `-posts` | `40` | Published posts to seed |
| `-bin` | `bin/gorun` | Binary path (built if missing) |
| `-themes` | `themes` | Themes directory |
| `-data` | temp | Data directory |
| `-keep-data` | false | Keep temp data after exit |
| `-base-url` | — | Skip spawn; hit existing server |
| `-token` | — | Bearer API key (seed/writes) |
| `-timeout` | `10s` | Per-request timeout |
| `-json` | false | Machine-readable report |

## Interpreting results

- **p50 / p95**: median and tail latency for **successful** responses. Rising p95
  under higher `-concurrency` usually means SQLite or Go scheduler contention,
  not “Go is fast.”
- **Error rate**: non-2xx/3xx and client timeouts. Above ~0 under pure reads is a
  regression signal; with writes, expect occasional conflicts only if you change
  the mix to create/delete.
- **Throughput (req/s)**: useful for relative comparisons on the **same** machine
  and flags. Do not treat a laptop number as production capacity.
- **RSS**: resident set of the `gorun serve` process during the run (Linux).
  Compare max RSS across concurrency steps (25 → 50 → 100).
- **CPU %**: approximate `(Δutime+Δstime) / elapsed` for that process. Above ~100
  means more than one core on average.

Suggested comparison ladder (same host, warm disk):

```sh
for c in 10 25 50 100; do
  go run ./tools/loadtest -concurrency "$c" -duration 20s -posts 40 -json \
    | tee "load-$c.json"
done
```

## Unit tests

Harness math and the HTTP runner are covered without booting GoRun:

```sh
go test ./tools/loadtest/
```

## Related docs

[PRODUCTION.md](PRODUCTION.md) · [OBSERVABILITY.md](OBSERVABILITY.md) · [E2E.md](E2E.md) · [ARCHITECTURE.md](ARCHITECTURE.md)
