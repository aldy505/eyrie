# AGENTS: Project Learnings for Eyrie

## Project Summary
- **Eyrie** is a work-in-progress uptime monitoring system with a distributed architecture.
- Backend is a **Go** monolith that runs in different **modes**.
- Frontend is a **React 19 + TypeScript + Vite + TailwindCSS** SPA embedded into the Go server at build time.
- Uptime data is stored in **PostgreSQL 18+** using plain PostgreSQL (no TimescaleDB or other extensions).

## Repository Layout
- Go sources live in the repository root (e.g., `main.go`, `server.go`, `checker.go`).
- Frontend lives under `frontend/` (Vite project).
- Example YAML configs live under `example_configurations/`.
- PostgreSQL migrations live under `migrations/postgres/`.
- Worker code (`worker_*.go`) handles async processing via pubsub queues.

## Runtime Modes & Components
### Modes (entrypoint: `main.go`)
- **server** (default): runs HTTP API + embedded SPA + worker processes + partition maintenance.
- **checker**: regional agent that registers with the server and performs monitor checks.

### Server Components
- **HTTP API** (see `server.go`):
  - `GET /config` → serve monitor configuration to checkers.
  - `GET /uptime-data` → aggregated uptime data for the SPA.
  - `GET /uptime-data-by-region` → region-specific latency and downtime breakdown.
  - `GET /monitor-incidents` → current monitor health plus active incident metadata.
  - `POST /checker/register` → checker registration + config fetch.
  - `POST /checker/submit` → checker submissions.
  - `/` → SPA handler for frontend.
- **Workers** (spawned in server mode):
  - **IngesterWorker**: batches raw `monitor_historical` inserts and builds hourly and daily rollups.
  - **ProcessorWorker**: analyzes recent submissions, updates incident state/records, and decides if an alert should fire.
  - **AlerterWorker**: receives alert messages (currently logs; TODO for real delivery).
  - **CleanupWorker**: creates future partitions, drops expired partitions, and deletes expired incident records/events.

### Checker Behavior
- Registers with server, receives `MonitorConfig`, then runs checks on schedule.
- Limits concurrent checks to 10 (weighted semaphore).
- Submits results to server using API key auth.

## Configuration & Environment
### Server Config (`ServerConfig` in `config_server.go`)
- YAML-driven with defaults + envconfig support.
- Includes:
  - `server.host`, `server.port`, `server.log_level`
  - `metadata` for status page title and last-updated flag
  - `registered_checkers` with region + API key
  - `database.url` (PostgreSQL DSN), `database.max_connections`, `database.min_connections`
  - `DATABASE_URL` env override for `database.url`
  - `task_queue.*` for processor/ingester/alerter pubsub addresses
  - `dataset.retention_days`, `dataset.partition_lookahead_days`, processing thresholds
  - `alerting.webhook` settings
  - `sentry` options

### Checker Config (`CheckerConfig` in `config_checker.go`)
- YAML + envconfig with explicit env variables:
  - `UPSTREAM_URL`, `REGION`, `API_KEY`
  - `SENTRY_*` env vars for tracing and error reporting

### Monitor Config (`MonitorConfig` in `config_monitor.go`)
- YAML file containing:
  - `monitors` with support for:
    - `http`
    - `tcp`
    - `icmp`
    - `redis`
    - `postgres`
    - `mysql`
    - `mssql`
    - `clickhouse`
    - `grpc` (standard `grpc.health.v1.Health/Check` health protocol)
  - `groups` (group monitors for status page aggregation)

### Database Monitor Notes
- PostgreSQL uses `postgres.dsn`
- MySQL uses `mysql.dsn`
- SQL Server uses `mssql.dsn`
- ClickHouse uses `clickhouse.dsn`
  - native TCP example: `clickhouse://default:@127.0.0.1:9000/default`
  - HTTP example: `clickhouse://default:@127.0.0.1:8123/default?protocol=http`
- gRPC uses `grpc.address`, optional `grpc.service`, and optional TLS (`use_tls`, `skip_tls_verify`)

### Example Configs
See `example_configurations/` for:
- `server.example.yaml`
- `checker.example.yaml`
- `monitor.example.yaml`

## Data Storage & Migrations
- Uses **PostgreSQL** via `jackc/pgx/v5/pgxpool`.
- Server mode runs migrations and `EnsurePartitions` on startup.
- Migrations use Go + Goose-style markers with embedded files via `//go:embed migrations/postgres/*.sql`.
- Migration versions are tracked in `schema_migrations`.

### Database Schema
- `monitor_historical`: raw per-probe results, partitioned by UTC day on `created_at`
- `monitor_historical_hourly_aggregate`: hourly per-monitor rollups, partitioned by UTC day
- `monitor_historical_hourly_region_aggregate`: hourly per-monitor+region rollups, partitioned by UTC day
- `monitor_historical_daily_aggregate`: daily per-monitor rollups used by the UI, partitioned by date
- `monitor_historical_region_daily_aggregate`: daily per-monitor+region rollups, partitioned by date
- `monitor_incident_state`: current incident state per monitor
- `monitor_incidents`: incident records (lifecycle tracking)
- `monitor_incident_events`: event timeline for incidents
- `schema_migrations`: applied migration versions

### Retention & Partitioning
- Raw, hourly, and daily tables are partitioned by UTC day.
- The service creates current and future partitions; it never creates past partitions.
- `dataset.retention_days` is a global retention window for raw, rollups, incidents, and events.
- Retention drops expired partitions instead of row-by-row deletes.
- Incidents are deleted by `started_at`; events are deleted by `created_at` and when their incident is deleted. `monitor_incident_state` is kept because it represents current health.
- Submissions older than the current UTC day are acknowledged but not inserted; the ingester logs time drift.
- Hourly rollups are built from raw data with `INSERT ... SELECT ... GROUP BY date_trunc('hour', ...) ON CONFLICT`.
- Daily rollups are derived from hourly rollups using weighted averages and counts.
- Ingestion uses batched multi-row `INSERT ... ON CONFLICT DO NOTHING` through pgx.

## Messaging / Task Queue
Uses `gocloud.dev/pubsub` with pluggable backends:
- In-memory (`mem://`)
- Kafka, NATS, RabbitMQ (imported drivers)
Task queues drive worker pipelines:
1. Checker submissions → ingester + processor queues
2. Processor decisions → alerter queue

## Frontend
- Vite app under `frontend/`.
- Built output goes to `frontend/dist` and is embedded by Go:
  - `//go:embed frontend/dist` in `server.go`.
- This means **`frontend/dist` must exist before `go test` / `go build`** unless build tags/embedding change.

## Local Development Commands
### PostgreSQL
```bash
docker run -d --rm --name eyrie-postgres \
  -e POSTGRES_USER=eyrie \
  -e POSTGRES_PASSWORD=eyrie \
  -e POSTGRES_DB=eyrie \
  -p 5432:5432 \
  postgres:18-alpine

docker exec eyrie-postgres createdb -U eyrie eyrie_test
```

### Backend (from `CONTRIBUTING.md`)
```bash
go run . -mode=server -config=./example_configurations/server.example.yaml -monitor=./example_configurations/monitor.example.yaml
```
Set `DATABASE_URL` to override the example PostgreSQL DSN.

Checker nodes (per region):
```bash
UPSTREAM_URL="http://localhost:8600" REGION="us-east-1" API_KEY="us-east-1-api-key-here" go run . -mode=checker
```

### Frontend
```bash
cd frontend
npm install
npm run dev
```

## Build / Lint / Test
### Go
- `TEST_DATABASE_URL="postgres://eyrie:eyrie@localhost:5432/eyrie_test?sslmode=disable" go test ./...`
- `go build -o eyrie .`
- Without `TEST_DATABASE_URL`, database integration tests skip and pure unit tests still run.

### Frontend (in `frontend/`)
- `npm run lint` (oxlint)
- `npm run format` (oxfmt)
- `npm run build` (tsc + Vite)

## Observability & Logging
- Uses **slog** for structured logging.
- **Sentry** is wired into server, checker, and workers for error reporting, traces, logs, and custom metrics.
- Metric coverage currently includes checker cycles, monitor checks, submission receipt/send/ingestion, incident transitions, partition maintenance, and alert delivery outcomes.

## Notes for Agents
- Server mode starts workers, runs migrations, and manages partitions—be mindful of side effects when testing locally.
- The SPA is embedded; missing `frontend/dist` will break builds/tests that compile `server.go`.
- Task queues are configured via addresses; `mem://` is used in example configs for local dev.
- Programmatic incidents are the current source of truth. The schema preserves machine-derived fields separately from user-facing fields so manual adjustment can be layered in later without losing the original automatic context.
- Workers use Sentry transactions for distributed tracing of processing pipelines.
- Database migrations follow a timestamp-based naming convention and use Goose-style markers for up/down scripts.
- Check probes implement various SQL and network checks with proper timeout and error handling.
- Severity levels for incidents include: healthy, degraded, down.
- Impact levels include: unknown, operational, maintenance, degraded_performance, partial_outage, major_outage.
- Event types for incident lifecycle: created, updated, resolved.
- DuckDB is no longer supported; existing DuckDB data is not imported.
