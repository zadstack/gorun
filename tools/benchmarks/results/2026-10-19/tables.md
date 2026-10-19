Median (min–max), three repetitions per deployment and target.

## Target 50 requests/s

| Deployment | Issued req/s | Successful req/s | p95 latency, ms | Peak working set, MiB |
| --- | ---: | ---: | ---: | ---: |
| GoRun | 50.03 (50.03–50.03) | 50.03 (50.03–50.03) | 2.66 (2.66–2.71) | 114.54 (114.40–116.13) |
| WordPress + MariaDB | 50.03 (50.03–50.03) | 50.01 (50.01–50.01) | 16.85 (16.54–17.02) | 203.78 (203.45–204.39) |
| Astro static + Nginx | 50.03 (50.03–50.03) | 50.03 (50.03–50.03) | 0.66 (0.61–0.66) | 4.09 (3.95–4.09) |

## Target 200 requests/s

| Deployment | Issued req/s | Successful req/s | p95 latency, ms | Peak working set, MiB |
| --- | ---: | ---: | ---: | ---: |
| GoRun | 200.03 (200.03–200.03) | 200.02 (200.02–200.02) | 2.61 (2.55–2.80) | 117.38 (117.11–117.40) |
| WordPress + MariaDB | 91.74 (91.54–96.26) | 90.24 (90.10–94.67) | 1208.95 (1157.52–1244.49) | 779.48 (779.10–780.86) |
| Astro static + Nginx | 200.03 (200.03–200.04) | 200.03 (200.03–200.03) | 0.44 (0.40–0.45) | 4.08 (3.96–4.15) |
