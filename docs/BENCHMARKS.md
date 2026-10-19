# A reproducible minimal article workload

These measurements compare **GoRun**, **WordPress + MariaDB**, and **Astro static output served by Nginx**. Astro is a static-delivery reference, not a complete CMS deployment. **GoRun Edge is unavailable for measurement**: only permission contracts exist, not a runnable Edge CMS. No zero or estimated Edge result is substituted.

## What was measured

On 19 October 2026, one disposable GitHub-hosted Linux runner served 100 synthetic articles using minimal templates. Each response was 4,513 bytes; the runner checked every requested article's exact title and body before measuring. Three rotated repetitions per deployment used five seconds of warmup followed by 30 seconds at each nominal target, 50 then 200 requests/s. No customer data or production traffic was used.

Each server received two logical CPUs and a total 2 GiB container memory budget. On this runner those CPUs were SMT siblings of one physical core. The load generator used the remaining two logical CPUs on the other physical core. WordPress's budget included Apache/PHP (1,536 MiB) and MariaDB (512 MiB). CPU affinity and memory constraints do not eliminate shared-host scheduling or virtualization noise.

There was no TLS, CDN, plugin workload or full-page cache. WordPress used a minimal theme without content filters. Astro built the same articles ahead of time; no Node.js process ran while serving them. This measures warm minimal article serving, not full application equivalence, editorial work, installation time or production capacity.

## Equal-rate condition: target 50 requests/s

All three deployments issued approximately 50 requests/s. Values below are **median (observed minimum–maximum)** of three per-run measurements. These ranges are descriptive, not confidence intervals. Every recorded request returned HTTP 200; this applies to issued requests only.

| Deployment | Issued req/s | Successful req/s | p95 latency, ms | Peak working set, MiB |
| --- | ---: | ---: | ---: | ---: |
| GoRun | 50.03 (50.03–50.03) | 50.03 (50.03–50.03) | 2.66 (2.66–2.71) | 114.54 (114.40–116.13) |
| WordPress + MariaDB | 50.03 (50.03–50.03) | 50.01 (50.01–50.01) | 16.85 (16.54–17.02) | 203.78 (203.45–204.39) |
| Astro static + Nginx | 50.03 (50.03–50.03) | 50.03 (50.03–50.03) | 0.66 (0.61–0.66) | 4.09 (3.95–4.09) |

GoRun recorded lower latency and sampled working set than this uncached WordPress deployment under this workload. The static Astro reference recorded the lowest latency and working set. These observations do not establish a general CMS ranking or feature superiority.

## Higher nominal target: 200 requests/s

The load generator was capped at 100 concurrent workers. At this target WordPress's longer response times caused **issued traffic to fall below the nominal target**. Its latency was therefore measured under a different delivered load. Do not describe this as a matched 200 requests/s comparison, an error-free response to all desired requests, or maximum server capacity.

| Deployment | Issued req/s | Successful req/s | p95 latency, ms | Peak working set, MiB |
| --- | ---: | ---: | ---: | ---: |
| GoRun | 200.03 (200.03–200.03) | 200.02 (200.02–200.02) | 2.61 (2.55–2.80) | 117.38 (117.11–117.40) |
| WordPress + MariaDB | 91.74 (91.54–96.26) | 90.24 (90.10–94.67) | 1208.95 (1157.52–1244.49) | 779.48 (779.10–780.86) |
| Astro static + Nginx | 200.03 (200.03–200.04) | 200.03 (200.03–200.03) | 0.44 (0.40–0.45) | 4.08 (3.96–4.15) |

“Issued req/s” uses Vegeta's request-issue interval. “Successful req/s” includes the final response wait. The difference is material for the slower condition. The raw metadata field `offered_rps` names the **configured target**, not the actual issued rate.

## Versions and resource definitions

- GoRun was built from the exact checkout in the raw metadata, with version string `benchmark`. The measured application source matches the 0.4.0-era source; this is not a measurement of later canvas UI changes.
- WordPress 7.1.3, PHP 8.3 image and MariaDB 11.4; Astro 7.4.0, with its package lockfile; Nginx stable image. Exact pulled image identities and the host/kernel/CPU information are retained in the raw artifact.
- PHP version/INI were queried using the CLI. Apache SAPI's actual opcache configuration was not independently verified; this is not a tuned WordPress comparison. Apache MPM configuration is retained separately.
- Vegeta installation was pinned to 12.13.0 in the workflow. Its embedded version fields were blank, so metadata does not independently attest that version.
- Peak working set is the maximum sampled aggregate Docker memory usage minus inactive file cache, including before/after samples. It is **not process RSS**, full host memory, or a continuous exact peak. WordPress includes its database.
- CPU time is the sum of container CPU counter differences, divided by issued request count; the notebook exposes this calculation. Startup values describe three cached-image restarts per product and repeat across the two rate entries; they are not six separate observations. The `idle` snapshot is taken immediately after warmup, not stabilized idle memory.

## Evidence and reproduction

- [Executed analysis notebook](../tools/benchmarks/results/2026-10-19/analysis.ipynb), including figures, every rate and calculation.
- [Complete compressed raw evidence](../tools/benchmarks/results/2026-10-19/raw.tar.gz), including request-level Vegeta binaries, JSON reports, resource samples, synthetic fixtures, dependency lockfile and metadata; [SHA-256](../tools/benchmarks/results/2026-10-19/SHA256SUMS).
- [Validated summary](../tools/benchmarks/results/2026-10-19/summary.json) and [controlled runner](../tools/benchmarks/run.py).
- [Successful original measurement run](https://github.com/zadstack/gorun/actions/runs/39233436452). The checked-in raw archive preserves evidence beyond workflow artifact expiry.

On a disposable Linux Docker host with at least four logical CPUs, follow the [measurement instructions](../tools/benchmarks/README.md). To recalculate existing evidence, extract the archive into a new directory and run the standard-library analyzer:

```sh
python3 tools/benchmarks/analyze.py /path/to/extracted-evidence --output work/recalculated-comparison
```

Public use should retain these conditions and limitations beside the figures. Installation friction, languages, approvals, agent capabilities and hosting choices need separate product documentation; they cannot be inferred from HTTP latency.
