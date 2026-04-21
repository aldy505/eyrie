# AGENTS: Project Learnings for Eyrie

## Project Summary
- **Eyrie** is a work-in-progress uptime monitoring system with a distributed architecture.
- Backend is a **Go** monolith that runs in different **modes**.
- Frontend is a **React 19 + TypeScript + Vite + TailwindCSS** SPA embedded into the Go server at build time.

## Repository Layout
- Go sources live in the repository root (e.g., `main.go`, `server.go`, `checker.go`).
- Frontend lives under `frontend/` (Vite project).
- Example YAML configs live under `example_configurations/`.
- DuckDB migrations live under `migrations/`.

## Runtime Modes & Components
### Modes (entrypoint: `main.go`)
- **server** (default): runs HTTP API + embedded SPA + worker processes.
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
  - **IngesterWorker**: ingests raw `monitor_historical` and performs daily aggregation.
  - **ProcessorWorker**: analyzes recent submissions, updates incident state/records, and decides if an alert should fire.
  - **AlerterWorker**: receives alert messages (currently logs; TODO for real delivery).

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
  - `database.path` (DuckDB)
  - `task_queue.*` for processor/ingester/alerter pubsub addresses
  - `dataset` thresholds (lookback minutes, failure thresholds, retention)
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
  - `groups` (group monitors for status page aggregation)

### Database Monitor Notes
- PostgreSQL uses `postgres.dsn`
- MySQL uses `mysql.dsn`
- SQL Server uses `mssql.dsn`
- ClickHouse uses `clickhouse.dsn`
  - native TCP example: `clickhouse://default:@127.0.0.1:9000/default`
  - HTTP example: `clickhouse://default:@127.0.0.1:8123/default?protocol=http`

### Example Configs
See `example_configurations/` for:
- `server.example.yaml`
- `checker.example.yaml`
- `monitor.example.yaml`

## Data Storage & Migrations
- Uses **DuckDB** via `duckdb-go`.
- Migration logic lives in `database_migration.go` and `migrations/`.
- Server mode runs migrations on startup.
- Incident storage currently spans:
  - `monitor_incident_state` for current derived health
  - `monitor_incidents` for active/resolved incident records
  - `monitor_incident_events` for timeline/history entries

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
### Backend (from `CONTRIBUTING.md`)
```bash
go run . -mode=server -config=./example_configurations/server.example.yaml -monitor=./example_configurations/monitor.example.yaml
```
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
- `go test ./...`
- `go build -o eyrie .`

### Frontend (in `frontend/`)
- `npm run lint` (oxlint)
- `npm run format` (oxfmt)
- `npm run build` (tsc + Vite)

## Observability & Logging
- Uses **slog** for structured logging.
- **Sentry** is wired into server, checker, and workers for error reporting, traces, logs, and custom metrics.
- Metric coverage currently includes checker cycles, monitor checks, submission receipt/send/ingestion, incident transitions, and alert delivery outcomes.

## Notes for Agents
- Server mode starts workers and runs migrations—be mindful of side effects when testing locally.
- The SPA is embedded; missing `frontend/dist` will break builds/tests that compile `server.go`.
- Task queues are configured via addresses; `mem://` is used in example configs for local dev.
- Programmatic incidents are the current source of truth. The schema now preserves machine-derived fields separately from user-facing fields so manual adjustment can be layered in later without losing the original automatic context.
