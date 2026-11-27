# Migration Platform

## Run the full demo

The full Docker Compose stack includes PostgreSQL, the Go backend, the React
frontend, Grafana, Prometheus, Loki, Tempo, and the OpenTelemetry Collector.
From the project root, run:

```powershell
New-Item -ItemType Directory -Force var/log/migration-platform | Out-Null
docker compose up --build
```

Open <http://localhost:5173>. The default administrator is `admin` with the
password `change-me`. Override it before starting the stack if needed:

```powershell
$env:ADMIN_USERNAME = "admin"
$env:ADMIN_PASSWORD = "your-password"
docker compose up --build
```

Service URLs:

- Application: <http://localhost:5173>
- Backend: <http://localhost:8080>
- Grafana: <http://localhost:3000> (`admin` / `admin`)
- Prometheus: <http://localhost:9090>
- Tempo: <http://localhost:3200>
- Loki: <http://localhost:3100>

The default Docker database credentials are `platform` / `platform`.

The demo Compose file connects the backend to the OpenTelemetry Collector at
`otel-collector:4317`, writes JSON logs to the shared
`var/log/migration-platform` directory for Loki, and configures Prometheus to
scrape the backend at `backend:8080/metrics`. Grafana is provisioned with the
Prometheus, Loki, and Tempo data sources and the migration dashboard.

### Seed demo data

After the full stack is running, run the seed command from a second PowerShell
window:

```powershell
docker compose exec backend /app/seed-demo
```

The seed is idempotent and loads the JSON fixtures in
[`seed/demo`](seed/demo):

- a separate `demo_target` PostgreSQL database;
- three customers in `demo_customers`;
- the `Demo PostgreSQL` managed database connection;
- `engineering` (edit) and `audit` (view) groups;
- a draft migration that adds a `notes` column to `demo_customers`.

Demo accounts:

| Username | Password | Role |
| --- | --- | --- |
| `demo_editor` | `demo-editor` | Engineering editor; submits migrations |
| `demo_leader` | `demo-leader` | Engineering group leader; approves |
| `demo_auditor` | `demo-auditor` | Audit group leader; approves |

The seed command is intended for local demonstrations only. The passwords and
database credentials in the JSON fixtures must not be used in production.

### Populate batch observability panels

To create visible migration-duration, rows-processed, and batches-processed
data, run this after seeding:

```powershell
.\scripts\run-batch-observability-demo.ps1
```

The helper adds 250 demo rows, creates a 25-row batch migration, submits both
required approvals, enqueues the job, and waits for it to succeed. Open the
Grafana dashboard immediately afterward; the rate panels show the activity for
several minutes:
<http://localhost:3000/d/migration-platform/migration-platform-observability>.
Use `-ExtraRows` and `-BatchSize` to change the workload, for example
`.\scripts\run-batch-observability-demo.ps1 -ExtraRows 1000 -BatchSize 50`.

### Run tests

Unit tests:

```powershell
go test ./...
```

Integration tests:

```powershell
docker compose -f docker-compose.test.yml up -d --wait
$env:TEST_DATABASE_URL = "postgres://test:test@localhost:5433/platform_test?sslmode=disable"
go test -p 1 -race -tags=integration ./...
```

## Observability

The application exposes Prometheus metrics at `GET /metrics` and exports traces
to the OpenTelemetry Collector. JSON logs include `trace_id`, `span_id`, and
`correlation_id`. The full demo stack configures these connections
automatically. Open Grafana at <http://127.0.0.1:3000> with `admin` / `admin`.
The provisioned dashboard covers job state, queue wait, lock wait, migration
duration, batch and row throughput, active/idle pool connections, and pool
acquire wait.

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

The repository does not require k6 to be installed on the host. Run it in the
official Docker image and send its metrics to the Prometheus remote-write
receiver:

```powershell
New-Item -ItemType Directory -Force .\k6\results | Out-Null
docker run --rm -i `
  -v "${PWD}:/work" `
  -w /work `
  grafana/k6:latest run `
  -o experimental-prometheus-rw `
  -e K6_PROMETHEUS_RW_SERVER_URL=http://host.docker.internal:9090/api/v1/write `
  -e BASE_URL=http://host.docker.internal:8080 `
  -e K6_USERNAME=demo_editor `
  -e K6_PASSWORD=demo-editor `
  -e K6_DATABASE_IDS=2 `
  -e K6_REVIEW_IDS=approved-review-id `
  -e K6_MAX_VUS=20 `
  load/k6_migrations.js
```

Replace `approved-review-id` with the ID returned after creating and approving
a migration review. `docker-compose.yml` enables Prometheus's remote-write
receiver, so k6 metrics appear in the provisioned Grafana dashboard.

The default stages ramp to 20 VUs for two minutes, then ramp down to observe
recovery. Override `K6_RAMP_UP`, `K6_HOLD`, and `K6_RAMP_DOWN` for the team's
normal load and step levels. Thresholds are applied to HTTP error rate, API p95,
enqueue p95, and migration job failure rate. Enqueue response success is not
counted as migration success, only the terminal job state is counted.

The script writes `k6/results/summary.json`. The following results were
collected on 2026-11-27 against two isolated PostgreSQL targets. API latency
is reported separately for the concurrent table read and enqueue request.
Throughput is total HTTP requests per second. Each run used fresh approved
reviews, and a terminal job state—not an accepted enqueue response—was counted
as migration success.

| Level | VUs | API read p50/p95 (ms) | Enqueue p50/p95 (ms) | Throughput (req/s) | HTTP errors | Queue wait p95 (s) | Job duration p95 (s) | Jobs / failures | Recovery notes |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- |
| baseline | 2 | 98.37 / 462.57 | 100.95 / 508.35 | 6.45 | 0% | 11.40 | 11.62 | 4 / 0% | Ramp-down completed without errors |
| ramp | 5 | 73.52 / 183.10 | 82.36 / 206.13 | 16.74 | 0% | 8.01 | 8.24 | 16 / 0% | Ramp-down completed without errors |
| peak | 10 | 90.41 / 129.31 | 108.70 / 157.89 | 28.38 | 0% | 17.17 | 17.42 | 23 / 0% | Ramp-down completed without errors |
| recovery | — | phase-specific values not emitted | phase-specific values not emitted | — | 0% observed | — | — | 0% observed | All three runs reached zero active VUs and finished cleanly |

Analysis: the peak level increased total request throughput to 28.38 req/s
and raised queue wait and job duration p95 to 17.17 s and 17.42 s. API p95
remained below the configured 1,000 ms read and 2,000 ms enqueue thresholds,
while HTTP and terminal migration failure rates stayed at 0%. The recovery
phase was observed as the ramp-down portion of each scenario.

The saved raw summaries are `k6/results/baseline-summary.json`,
`k6/results/ramp-summary.json`, and `k6/results/peak-summary.json`.

## Security SAST

CodeQL SAST runs automatically on both the Go backend and the TypeScript/React frontend.
The workflow configuration, analysis scope, SARIF evidence, and findings register are documented in [`CODEQL.md`](CODEQL.md).
The initial local scan identified 7 Go alerts and 0 JavaScript/TypeScript alerts. After fixing the integer conversion issue, the subsequent scan reported 6 Go alerts and 0 JavaScript/TypeScript alerts.
Details of each rule, severity, code location, potential impact, and remediation decision are documented in [`CODEQL.md`](CODEQL.md), with supporting evidence available in the [`codeQLresults`](codeQLresults) directory.
