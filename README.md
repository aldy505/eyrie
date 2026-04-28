# Eyrie

Eyrie is a distributed uptime monitoring system with region-aware checks, multi-database support, and an embedded status page.

The backend is a Go monolith that runs as a **server** (central hub) or **checker** (regional agent). The frontend is a React 19 SPA embedded in the binary at build time.

## Quick Start

### Server

```bash
go run . -mode=server \
  -config=./example_configurations/server.example.yaml \
  -monitor=./example_configurations/monitor.example.yaml
```

Visit http://localhost:8600 to view the status page.

### Checker

```bash
UPSTREAM_URL="http://localhost:8600" \
REGION="us-east-1" \
API_KEY="us-east-1-api-key-here" \
go run . -mode=checker -config=./example_configurations/checker.example.yaml
```

## Features

- Region-aware monitoring with distributed checker nodes
- Multi-database support: PostgreSQL, MySQL, SQL Server, ClickHouse, Redis
- HTTP, TCP, and ICMP probes
- DuckDB-backed data storage with daily aggregation
- Programmatic incident tracking with timeline
- Multiple alert channels: Webhook, Slack, Discord, Teams, ntfy
- Sentry integration for observability, tracing, and profiling

## Documentation

- **[Deployment](docs/deployment.md)** - Docker Compose, systemd, static binary
- **[Configuration](docs/configuration.md)** - All config options, environment variables, DSN examples
- **[Architecture](docs/architecture.md)** - Components, data flow, deployment models
- **[Operations](docs/operational.md)** - Monitoring, troubleshooting, DuckDB recovery, Sentry setup
- **[API Reference](docs/api.md)** - Endpoint details and request/response examples

## Development

### Requirements

- Go 1.25+
- Node.js 22+

### Build

```bash
# Backend
go build -o eyrie .

# Frontend (included in build)
cd frontend
npm run build
```

### Test

```bash
go test ./...
```

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
