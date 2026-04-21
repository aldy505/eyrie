# Contribution Guide

Thanks for contributing to Eyrie.

## Toolchain

- Go 1.25+
- Node.js 22+

The frontend is a Vite app. If you prefer Bun or another JS runtime for local frontend work, that is fine as long as the committed output matches the existing project structure.

## Repository layout

- Go application code lives in the repository root
- `frontend/` contains the SPA
- `example_configurations/` contains starter YAML files
- `migrations/` contains DuckDB schema migrations

## Development workflow

### 1. Start the server

```bash
go run . -mode=server -config=./example_configurations/server.example.yaml -monitor=./example_configurations/monitor.example.yaml
```

### 2. Start one or more checker nodes

You can run several checkers on one machine while developing:

```bash
UPSTREAM_URL="http://localhost:8600" REGION="us-east-1" API_KEY="us-east-1-api-key-here" go run . -mode=checker -config=./example_configurations/checker.example.yaml

UPSTREAM_URL="http://localhost:8600" REGION="us-west-1" API_KEY="us-west-1-api-key-here" go run . -mode=checker -config=./example_configurations/checker.example.yaml

UPSTREAM_URL="http://localhost:8600" REGION="eu-west-1" API_KEY="eu-west-1-api-key-here" go run . -mode=checker -config=./example_configurations/checker.example.yaml
```

### 3. Run the frontend

```bash
cd frontend
npm install
npm run dev
```

## Supported monitor types

Eyrie currently supports:

- HTTP
- TCP
- ICMP
- Redis
- PostgreSQL
- MySQL
- Microsoft SQL Server
- ClickHouse (`clickhouse://...` for native TCP, `?protocol=http` for HTTP)

## Testing and validation

Run the existing checks before sending changes:

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

## Notes for contributors

- The server embeds `frontend/dist`, so Go builds/tests expect frontend build artifacts to exist.
- Server mode starts workers and runs migrations; use example configs with care when testing side effects.
- Incident persistence now includes `monitor_incident_state`, `monitor_incidents`, and `monitor_incident_events`.
- Sentry tracing and metrics are wired into server, checker, and worker flows; keep new instrumentation low-cardinality and behavior-safe.
