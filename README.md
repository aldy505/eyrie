# Eyrie

Eyrie is a distributed uptime monitoring system built for region-aware checks, lightweight deployment, and a status page that reflects both current health and recent history.

The backend is a Go monolith that can run as a **server** or a **checker**. The frontend is a React 19 + Vite SPA embedded into the server binary at build time.

## Current capabilities

- Region-aware monitor execution with checker registration and authenticated result submission
- HTTP, TCP, ICMP, Redis, PostgreSQL, MySQL, Microsoft SQL Server, and ClickHouse monitors
- ClickHouse support over both native TCP and HTTP transport through DSN configuration
- Raw submission ingestion plus daily uptime aggregation in DuckDB
- Programmatic incidents with persisted lifecycle records and event timeline groundwork
- Webhook, Slack, Discord, Teams, and ntfy alert delivery targets
- Sentry error monitoring, tracing, logs, and custom metrics around checks, ingestion, processing, and alerting

## Supported monitor types

| Type | Config block | Notes |
| --- | --- | --- |
| HTTP | `http` | Supports headers, expected status codes, TLS verification toggle |
| TCP | `tcp` | Optional TLS, request payload, and substring assertion |
| ICMP | `icmp` | Uses the system `ping` binary |
| Redis | `redis` | Optional auth, database selection, and TLS |
| PostgreSQL | `postgres` | Uses a DSN via `pgx` |
| MySQL | `mysql` | Uses a DSN via `go-sql-driver/mysql` |
| Microsoft SQL Server | `mssql` | Uses a `sqlserver://...` DSN |
| ClickHouse | `clickhouse` | Use `clickhouse://...` for native TCP and `?protocol=http` for HTTP |

See `example_configurations/monitor.example.yaml` for sample definitions.

## Runtime model

### Server mode

`go run . -mode=server ...`

The server process:

- runs DuckDB migrations
- exposes the HTTP API and embedded SPA
- receives checker submissions
- fans submissions out to ingester and processor queues
- runs ingester, processor, and alerter workers

### Checker mode

`go run . -mode=checker ...`

Each checker:

- registers to the server with an API key, region, and optional checker name
- downloads the current monitor configuration
- executes checks on schedule
- submits structured probe results back to the server

## API surface

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/config` | Returns status page metadata |
| `GET` | `/uptime-data` | Aggregated monitor/group uptime data |
| `GET` | `/uptime-data-by-region` | Region breakdown for a monitor |
| `GET` | `/monitor-incidents` | Current monitor health plus active incident metadata |
| `POST` | `/checker/register` | Checker registration and config fetch |
| `POST` | `/checker/submit` | Checker submission ingestion entrypoint |

If a checker runs outside your private network and the server sits behind a reverse proxy, the minimum paths to expose are:

- `POST /checker/register`
- `POST /checker/submit`

Everything else can remain private if you only need remote checker registration and result submission.

## Checker targeting

You can optionally pin a monitor to specific checkers with `checker_names`. These names match `registered_checkers[].name` in the server config. If a checker name is omitted, Eyrie falls back to the checker `region` as its effective name for backward compatibility.

```yaml
registered_checkers:
  - name: "us-east-1-public-checker"
    region: "us-east-1"
    api_key: "us-east-1-api-key-here"

monitors:
  - id: "primary-postgres"
    name: "Primary PostgreSQL"
    type: "postgres"
    checker_names:
      - "us-east-1-public-checker"
    postgres:
      dsn: "postgres://postgres:postgres@10.0.0.5:5432/postgres?sslmode=disable"
```

Monitors without `checker_names` are still sent to every checker.

## Incident model

Eyrie currently creates **programmatic incidents** from processor-evaluated monitor state.

Today that means:

- `monitor_incident_state` stores the current derived health per monitor
- `monitor_incidents` stores the active/resolved incident records
- `monitor_incident_events` stores timeline events for creation, updates, and resolution

The storage model is intentionally shaped so incidents can remain automatically generated first, then be manually adjusted later without losing the machine-derived values that created them.

## Sentry observability

Server, checker, and workers all initialize Sentry with tracing and logs enabled. The codebase also emits custom Sentry metrics for:

- checker cycles and monitor checks
- submission receipt, send, and ingestion
- processor incident transitions
- alert delivery outcomes

Example Sentry configuration is included in `example_configurations/server.example.yaml` and `example_configurations/checker.example.yaml`.

## Local development

### Prerequisites

- Go 1.25+
- Node.js 22+

### Run the server

```bash
go run . -mode=server -config=./example_configurations/server.example.yaml -monitor=./example_configurations/monitor.example.yaml
```

### Run one or more checkers

```bash
UPSTREAM_URL="http://localhost:8600" CHECKER_NAME="us-east-1-public-checker" REGION="us-east-1" API_KEY="us-east-1-api-key-here" go run . -mode=checker -config=./example_configurations/checker.example.yaml
```

### Run the frontend in dev mode

```bash
cd frontend
npm install
npm run dev
```

## Build, test, and lint

### Backend

```bash
go test ./...
go build -o eyrie .
```

### Frontend

```bash
cd frontend
npm run lint
npm run build
```

## Repository layout

- Go sources live at the repository root
- `frontend/` contains the SPA
- `example_configurations/` contains starter YAML files
- `migrations/` contains DuckDB schema migrations

## License

```text
Copyright 2025 Reinaldy Rafli <github@reinaldyrafli.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
