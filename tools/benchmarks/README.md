# Reproducible CMS comparison (measurement harness)

The [validated 19 October 2026 results](../../docs/BENCHMARKS.md) include raw evidence and an executed notebook. GoRun Full and WordPress are CMS deployments. Astro is a separately
labelled static-delivery reference. GoRun Edge is unavailable for measurement:
this repository currently contains permission contracts, not a runnable Edge CMS.

The runner seeds the same 100 synthetic articles, serves minimal article HTML,
checks each URL for the expected body, and records immutable image digests and
host information. No production site, content, credentials or traffic is used.

Each server gets the first two logical CPUs and a total 2 GiB memory budget.
WordPress includes Apache/PHP (1536 MiB) and MariaDB (512 MiB) on those same CPUs.
The load generator uses the remaining CPUs. Results require at least four
logical CPUs. Images run sequentially with three rotated repetitions to reduce
ordering bias. No TLS, CDN, plugins or full-page cache is enabled. Astro's output
is served by Nginx and has no CMS editing or database workload.

Nominal target rates of 50 and 200 HTTP requests/second each run for 30 seconds
after warmup. Achieved throughput is not maximum server capacity. Raw Vegeta
reports retain per-run percentiles and errors; do not average request
percentiles across runs. Report medians across repetitions with their range,
not a confidence interval. Memory is Docker working set (usage minus inactive
file cache), not process RSS. WordPress CPU and memory include its database.
Startup records readiness with existing data and cached images; it is not an
end-to-end installation-time measurement. Download/build/provisioning times
remain separate and are affected by registry and network conditions.

Run from the repository root on a disposable Linux Docker host:

```sh
go install github.com/tsenart/vegeta/v12@v12.13.0
python3 tools/benchmarks/run.py --output work/benchmark-results
```

Dependencies and conditions, raw reports, restart timings and sampled Docker
statistics are saved in the output folder. The workflow uploads this folder on
success and failure. Incomplete runs must remain labelled incomplete.

The 100-worker limit can reduce issued traffic below the target. Publish actual issued rate and successful throughput separately; the `offered_rps` metadata field is only the configured target. Do not rank deployments at unequal delivered rates.
