# Migration Platform

## Observability

The application exposes Prometheus metrics at `GET /metrics` and exports traces
to an OpenTelemetry Collector when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. JSON
logs include `trace_id`, `span_id`, and `correlation_id`. Set `LOG_FILE` to a
JSONL path so the Collector's `filelog` receiver can forward those logs to Loki.

Start the reproducible local observability stack:

```powershell
New-Item -ItemType Directory -Force var/log/migration-platform | Out-Null
docker compose -f docker-compose.observability.yml up -d
$env:OTEL_EXPORTER_OTLP_ENDPOINT = "127.0.0.1:4317"
$env:LOG_FILE = "var/log/migration-platform/app.jsonl"
$env:SESSION_SECURE_COOKIES = "false"
go run ./cmd/server
```

Open Grafana at <http://127.0.0.1:3000> with `admin` / `admin`. The provisioned
dashboard covers job state, queue wait, lock wait, migration duration, batch and
row throughput, active/idle pool connections, and pool acquire wait. Prometheus
scrapes the host application at `host.docker.internal:8080`.

Metric labels are intentionally bounded: route group, HTTP method, status class,
operation, mode, result, pool role, pool state, and job state. User IDs, job IDs,
database IDs, execution IDs, and SQL text are never labels.

Example correlation:

```text
log: {"event":"migration.attempt.start","trace_id":"...","span_id":"...","correlation_id":"...","job_id":42}
trace: migration.worker -> migration.lock -> migration.execute -> db.sql
metric: migration_platform_migration_duration_seconds{operation="apply",mode="batch",result="success"}
```

In Grafana, select the Tempo trace and use “Trace to logs”; the provisioned
Tempo datasource filters Loki by `trace_id` without putting that ID in metric
labels.

## k6 stress testing

The staged test in [`load/k6_migrations.js`](load/k6_migrations.js) concurrently
does a database API read and enqueues an already-approved migration review. Each
virtual user polls the resulting job until it reaches a terminal state. Set
`K6_DATABASE_IDS` and `K6_REVIEW_IDS` in matching order to exercise the same
database or distribute traffic across different databases.

Example:

```powershell
k6 run `
  -e BASE_URL=http://127.0.0.1:8080 `
  -e K6_USERNAME=loadtest `
  -e K6_PASSWORD='change-me' `
  -e K6_DATABASE_IDS=1,2 `
  -e K6_REVIEW_IDS=approved-review-id-1,approved-review-id-2 `
  -e K6_MAX_VUS=20 `
  load/k6_migrations.js
```

The default stages ramp to 20 VUs for two minutes, then ramp down to observe
recovery. Override `K6_RAMP_UP`, `K6_HOLD`, and `K6_RAMP_DOWN` for the team's
normal load and step levels. Thresholds are applied to HTTP error rate, API p95,
enqueue p95, and migration job failure rate. Enqueue response success is not
counted as migration success; only the terminal job state is counted.

The script writes `k6/results/summary.json`. Record the output for every load
level in the following table, including the Grafana queue-wait and migration
duration panels:

| Level | VUs | API p50/p95 | Throughput | HTTP errors | Queue wait p95 | Job duration p95 | Job failures | Recovery notes |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | --- |
| baseline |  |  |  |  |  |  |  |  |
| ramp |  |  |  |  |  |  |  |  |
| peak |  |  |  |  |  |  |  |  |
| recovery |  |  |  |  |  |  |  |  |
