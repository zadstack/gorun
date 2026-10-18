# GoRun: observability (thin)

Optional **Prometheus text** scrape endpoint and notes on shipping structured logs.
This is not a bundled Grafana/OTEL stack.

Also see [PRODUCTION.md](PRODUCTION.md), [ERROR-INVENTORY.md](ERROR-INVENTORY.md),
[UPGRADE.md](UPGRADE.md).

---

## Metrics scrape (`GET /metrics`)

Off by default. Enable with env or TOML:

| Setting | Env / TOML | Default |
| --- | --- | --- |
| Enable | `GORUN_METRICS` / `metrics` | `false` |
| Bearer token | `GORUN_METRICS_TOKEN` / `metrics_token` | empty (open like `/healthz`) |

When enabled, the root mux serves Prometheus text `0.0.4` at `/metrics`
(sibling of `/healthz` / `/readyz`; not under Public middleware). Redirects
cannot claim `/metrics`.

### Series

| Metric | Type | Meaning |
| --- | --- | --- |
| `gorun_http_requests_total{code_class}` | counter | Responses since process start: `2xx` / `3xx` / `4xx` / `5xx` (includes scrapes) |
| `gorun_jobs{status}` | gauge | `queued`, `running`, `failed` (queued awaiting retry), `dead`, `done` |
| `gorun_backup_last_success_timestamp_seconds` | gauge | Unix time of newest successful **local** backup (max of `backup.schedule.last` and newest complete on-disk pair); `0` if none |

No per-path labels. No OpenTelemetry SDK.

### Scrape examples

```sh
# Private listener / sidecar (no token)
export GORUN_METRICS=true
curl -fsS http://127.0.0.1:8080/metrics

# Optional Bearer (Prometheus scrape_configs authorization.credentials)
export GORUN_METRICS=true
export GORUN_METRICS_TOKEN='long-random-secret'
curl -fsS -H "Authorization: Bearer long-random-secret" http://127.0.0.1:8080/metrics
# or: -H "X-Metrics-Token: long-random-secret"
```

Prometheus fragment:

```yaml
scrape_configs:
  - job_name: gorun
    metrics_path: /metrics
    static_configs:
      - targets: ["127.0.0.1:8080"]
    # authorization:
    #   credentials: long-random-secret
```

### Network advice

- Prefer scraping on the private Docker/Coolify network or localhost, not the public hostname.
- If `/metrics` is reachable from the internet, set `GORUN_METRICS_TOKEN` **or** ACL it at the reverse proxy.
- Maintenance mode does not affect `/metrics` (root route).

### Smoke after enable

| Check | Expect |
| --- | --- |
| `curl -fsS http://127.0.0.1:8080/metrics` (or with Bearer) | HTTP 200, body contains `gorun_jobs` |
| Without token when `GORUN_METRICS_TOKEN` set | HTTP 401 |
| With `GORUN_METRICS` unset / false | HTTP 404 |

---

## Log shipping (no new stack)

In `GORUN_ENV=prod`, the process logger writes **JSON** lines to stderr (Coolify /
systemd / Docker capture this). Forward that stream with your host’s collector
(Vector, Fluent Bit, CloudWatch agent, etc.).

- Request lines include method, redacted path, status, `dur_ms`, IP, `request_id`.
- Classified runtime aggregates stay in **Error Inventory** (admin + bounded DB);
  that is not a full log archive — see [ERROR-INVENTORY.md](ERROR-INVENTORY.md).

Do not expect GoRun to push to a remote OTEL collector in v0.1.

---

## Arabic — ملخص سريع

- `GORUN_METRICS=true` يفعّل `GET /metrics` بصيغة Prometheus نصية خفيفة.
- اختياري: `GORUN_METRICS_TOKEN` لـBearer.
- العدادات: HTTP حسب فئة الحالة، مهام (dead/failed…)، ووقت آخر نسخة محلية ناجحة.
- في الإنتاج السجلات JSON على stderr — اربطها بمُجمّع المضيف؛ لا يوجد OTEL مدمج.
