# Contribution Guide

Thanks for contributing to Eyrie.

## Toolchain

- Go 1.25+
- Node.js 22+
- PostgreSQL 18+

The frontend is a Vite app. If you prefer Bun or another JS runtime for local frontend work, that is fine as long as the committed output matches the existing project structure.

## Repository layout

- Go application code lives in the repository root
- `frontend/` contains the SPA
- `example_configurations/` contains starter YAML files
- `migrations/postgres/` contains the embedded PostgreSQL schema migrations

## Development workflow

### 1. Start PostgreSQL

```bash
docker run -d --rm --name eyrie-postgres \
  -e POSTGRES_USER=eyrie \
  -e POSTGRES_PASSWORD=eyrie \
  -e POSTGRES_DB=eyrie \
  -p 5432:5432 \
  postgres:18-alpine

docker exec eyrie-postgres createdb -U eyrie eyrie_test
```

The example server config points at `postgres://eyrie:eyrie@localhost:5432/eyrie?sslmode=disable`. Set `DATABASE_URL` to override it.

### 2. Start the server

```bash
go run . -mode=server -config=./example_configurations/server.example.yaml -monitor=./example_configurations/monitor.example.yaml
```

### 3. Start one or more checker nodes

You can run several checkers on one machine while developing:

```bash
UPSTREAM_URL="http://localhost:8600" REGION="us-east-1" API_KEY="us-east-1-api-key-here" go run . -mode=checker -config=./example_configurations/checker.example.yaml

UPSTREAM_URL="http://localhost:8600" REGION="us-west-1" API_KEY="us-west-1-api-key-here" go run . -mode=checker -config=./example_configurations/checker.example.yaml

UPSTREAM_URL="http://localhost:8600" REGION="eu-west-1" API_KEY="eu-west-1-api-key-here" go run . -mode=checker -config=./example_configurations/checker.example.yaml
```

### 4. Run the frontend

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
TEST_DATABASE_URL="postgres://eyrie:eyrie@localhost:5432/eyrie_test?sslmode=disable" go test ./...
go build -o eyrie .
```

Without `TEST_DATABASE_URL`, database integration tests are skipped and pure unit tests still run.

### Frontend

```bash
cd frontend
npm run lint
npm run build
```

## Notes for contributors

- The server embeds `frontend/dist`, so Go builds/tests expect frontend build artifacts to exist.
- Server mode starts workers, runs migrations, and manages partitions; use example configs with care when testing side effects.
- Partition creation and retention use UTC day boundaries.
- Incident persistence now includes `monitor_incident_state`, `monitor_incidents`, and `monitor_incident_events`.
- Sentry tracing and metrics are wired into server, checker, and worker flows; keep new instrumentation low-cardinality and behavior-safe.
