# Architecture

Eyrie is a distributed uptime monitoring system. This document describes the overall system design, component responsibilities, data flow, and deployment models.

## System Overview

Eyrie consists of three main types of components:

1. **Server**: Central hub for configuration, data ingestion, incident management, and alerting
2. **Checkers**: Regional agents that execute monitors and report results to the server
3. **Embedded SPA**: React frontend served by the server for viewing status and history

The system is built as a Go monolith that runs in different modes (server or checker), with an embedded React frontend compiled into the binary at build time.

## Components

### Server Mode

The server is the central orchestrator. It runs continuously with the following responsibilities:

```
Server Process
├── HTTP API
│   ├── GET /config              - Returns status page metadata
│   ├── GET /uptime-data         - Aggregated uptime data for SPA
│   ├── GET /uptime-data-by-region - Per-region breakdown
│   ├── GET /monitor-incidents   - Current health and incidents
│   ├── POST /checker/register   - Checker registration
│   ├── POST /checker/submit     - Result submission
│   └── /                        - Embedded SPA
├── DuckDB Database
│   ├── monitor_historical       - Raw probe submissions
│   ├── monitor_historical_daily_aggregate
│   ├── monitor_historical_region_daily_aggregate
│   ├── monitor_incident_state   - Current incident state
│   ├── monitor_incidents        - Incident lifecycle records
│   └── monitor_incident_events  - Incident event timeline
└── Worker Processes (goroutines)
    ├── IngesterWorker           - Ingests raw submissions and aggregates daily
    ├── ProcessorWorker          - Analyzes submissions, updates incident state
    └── AlerterWorker            - Sends alerts via webhooks/messaging
```

### Checker Mode

Each checker is a regional agent that performs monitoring tasks independently:

```
Checker Process
├── Registration
│   └── POST /checker/register to server
│       └── Receives monitor configuration
├── Check Executor
│   ├── HTTP/TCP/ICMP probes
│   ├── Database connectivity checks
│   ├── Scheduled execution with weighted semaphore (max 10 concurrent)
│   └── Per-monitor timeouts and retries
└── Result Submission
    └── POST /checker/submit to server
        └── Includes timestamp, latency, status, details
```

### Workers

Workers process submissions through message queues (in-memory or external systems like Kafka/NATS/RabbitMQ).

#### IngesterWorker

- Consumes raw submissions from checkers
- Stores in `monitor_historical` table
- Runs daily aggregation jobs to compute statistics
- Populates `monitor_historical_daily_aggregate` and `monitor_historical_region_daily_aggregate`

#### ProcessorWorker

- Analyzes recent submissions against thresholds
- Detects state transitions (healthy -> degraded -> down)
- Updates `monitor_incident_state` and creates records in `monitor_incidents`
- Decides when to trigger alerts

#### AlerterWorker

- Consumes alert messages from the processor
- Formats and delivers alerts to:
  - Webhook (generic HTTP POST)
  - Slack
  - Discord
  - Microsoft Teams
  - ntfy.sh
- Records delivery outcomes (success/failure)

## Data Flow

### Checker to Server Submission

```
Checker                         Server
   |                              |
   | 1. Execute monitor probe    |
   |----.                        |
   |    \-->  measure latency    |
   |         record status       |
   |                              |
   | 2. POST /checker/submit      |
   |----------------------------->|
   |    {timestamp, latency,      | 3. Enqueue to
   |     status, region, ...}     |    ingester queue
   |                              |<----.
   |                              |    4. Enqueue to
   |  5. 200 OK                  |    processor queue
   |<-----------------------------|<----.
   |                              |
   |                              | 6. IngesterWorker
   |                              |    stores raw data
   |                              |
   |                              | 7. ProcessorWorker
   |                              |    evaluates thresholds
   |                              |
   |                              | 8. Decision: incident?
   |                              |<----.
   |                              |    9. Enqueue to
   |                              |    alerter queue
   |                              |
   |                              | 10. AlerterWorker
   |                              |     sends notification
```

### SPA to Server Data Request

```
Browser (SPA)                   Server
   |                              |
   | 1. GET /uptime-data         |
   |----------------------------->|
   |                              | 2. Query aggregates
   |                              |    from DuckDB
   |                              |
   | 3. JSON response             |
   |<-----------------------------|
   |    {monitors: [{             |
   |      id, status,             |
   |      uptime_percent,         |
   |      recent_incidents,       |
   |      ...                     |
   |    }]}                       |
   |                              |
   | 4. Render status page        |
   |<----.                        |
   |     \--> Show regions,       |
   |         group status,        |
   |         historical graphs    |
```

## Task Queue System

Eyrie uses `gocloud.dev/pubsub` for asynchronous processing. Task queues connect checker submissions to worker pipelines:

### Queue Backends

The system is pluggable and supports:
- **In-memory** (`mem://`): Default for local development
- **Kafka**: For distributed deployments
- **NATS**: Lightweight pub/sub
- **RabbitMQ**: Traditional message queue

Configuration example (server.yaml):

```yaml
task_queue:
  processor:
    producer_address: "mem://processor_tasks"
    consumer_address: "mem://processor_tasks"
  ingester:
    producer_address: "mem://ingester_tasks"
    consumer_address: "mem://ingester_tasks"
  alerter:
    producer_address: "mem://alerter_tasks"
    consumer_address: "mem://alerter_tasks"
```

Or with Kafka:

```yaml
task_queue:
  processor:
    producer_address: "kafka://broker1:9092?topic=processor_tasks"
    consumer_address: "kafka://broker1:9092?topic=processor_tasks"
  ingester:
    producer_address: "kafka://broker1:9092?topic=ingester_tasks"
    consumer_address: "kafka://broker1:9092?topic=ingester_tasks"
  alerter:
    producer_address: "kafka://broker1:9092?topic=alerter_tasks"
    consumer_address: "kafka://broker1:9092?topic=alerter_tasks"
```

## Data Storage

### DuckDB Database

Eyrie uses DuckDB for data persistence. DuckDB is a lightweight, embedded SQL database ideal for analytical workloads.

**Schema Overview:**

- **monitor_historical**: Every probe result
  - Columns: id, monitor_id, region, timestamp, status_code, latency_ms, success, error_message, ...
  - Purpose: Raw audit trail and detailed analysis

- **monitor_historical_daily_aggregate**: Daily statistics per monitor
  - Columns: monitor_id, date, uptime_percent, avg_latency_ms, p95_latency_ms, check_count, success_count
  - Purpose: Efficient SPA queries for historical graphs

- **monitor_historical_region_daily_aggregate**: Daily statistics per monitor+region
  - Columns: monitor_id, region, date, uptime_percent, avg_latency_ms, check_count, success_count
  - Purpose: Regional breakdown on status page

- **monitor_incident_state**: Current incident state (one per monitor)
  - Columns: monitor_id, state (healthy/degraded/down), started_at, updated_at, metadata
  - Purpose: Fast lookup of current status

- **monitor_incidents**: Incident lifecycle (one record per incident event)
  - Columns: id, monitor_id, started_at, resolved_at, incident_type, severity, user_message, machine_message, ...
  - Purpose: Incident history and timeline

- **monitor_incident_events**: Incident event timeline
  - Columns: id, incident_id, event_type (created/updated/resolved), timestamp, details
  - Purpose: Audit trail of state changes

### Migrations

Database schema is managed via Goose-style migrations:
- Location: `migrations/` directory
- Naming: Timestamp-based (e.g., `001_initial_schema.sql`)
- Markers: `-- +goose Up` and `-- +goose Down` blocks
- Embedded: Migrations are embedded in the binary via `//go:embed`

On server startup, Eyrie automatically runs pending migrations.

## Monitor Execution

### Probe Types

Each monitor type is implemented as a separate probe package:

- **HTTP**: GET/POST to URL, validate status codes, optional TLS verification
- **TCP**: Connect to host:port, optional payload send/response check
- **ICMP**: Use system `ping` binary
- **Redis**: Connect and PING
- **PostgreSQL**: Connect via DSN and validate connection
- **MySQL**: Connect via DSN and validate connection
- **MSSQL**: Connect via DSN and validate connection
- **ClickHouse**: Connect via DSN (native TCP or HTTP)

### Concurrency Control

Checkers limit concurrent checks to 10 using a weighted semaphore. This prevents resource exhaustion when monitors have many probes or tight intervals.

### Timeout and Retry

Each monitor has a configurable `timeout_seconds`. The checker respects this and cancels the probe if it exceeds the limit. There is no built-in retry per probe; failures are recorded and evaluated by the processor.

## Incident Model

### Programmatic Incidents

Incidents are automatically generated based on processor analysis:

1. **Detection**: Processor evaluates thresholds:
   - If X% of recent checks fail -> transition to "degraded" or "down"
   - Duration-based transitions (after N minutes in degraded state -> down)

2. **Lifecycle**:
   - `created`: Incident starts when thresholds are crossed
   - `updated`: State changes (degraded -> down)
   - `resolved`: All checks pass again

3. **Storage Model**: Machine-derived fields are stored separately from user-facing fields, allowing manual adjustments in the future without losing the original automatic context.

## Deployment Models

### Single Server (Development)

```
Developer Machine
  |
  ├── eyrie server (Port 8600)
  |   └── Embedded frontend
  |       DuckDB (in-memory or local)
  |
  └── eyrie checker (local)
      └── Registers and submits to localhost:8600
```

### Server + Remote Checkers

```
Internet

    Checker (us-east-1)           Checker (eu-west-1)
         |                              |
         | HTTPS                        | HTTPS
         v                              v
    [Reverse Proxy / Firewall]
         |
         v
    Eyrie Server (Private Network)
    ├── HTTP API (port 8600)
    ├── DuckDB (persistent storage)
    ├── SPA (static frontend)
    └── Workers (ingester, processor, alerter)
    
    Status Page Viewers (Browser)
         |
         v
    [Firewall/Load Balancer]
         |
         v
    GET /uptime-data (Public)
```

### High-Availability Deployment

```
    Checker (us-east-1)
    Checker (eu-west-1)
    Checker (ap-southeast-1)
         |
         | HTTPS
         v
    [Load Balancer / Health Checks]
         |
    +----+----+
    |         |
    v         v
Eyrie Server A --- Replication ---> Eyrie Server B
 (Primary)           (Standby)
    |                      |
DuckDB A                DuckDB B
    |                      |
    +----------+----------+
               |
         [Shared Storage]
             or
         [Replication Stream]
```

Note: Active-active replication is not currently implemented. This model shows potential future architecture.

## Scalability Considerations

### Current Limitations

- Single server instance (no clustering)
- DuckDB best suited for moderate data volumes (millions of records)
- In-memory task queues only work with single server instance

### Scaling Path

1. **Short term** (thousands of monitors, multiple regions):
   - Deploy single Eyrie server behind a load balancer (stateless API, persistent DB)
   - Run checkers in each region
   - Use external database (PostgreSQL via DuckDB FDW or migration)

2. **Long term** (tens of thousands of monitors):
   - Split into multiple Eyrie server instances
   - Use distributed task queue (Kafka, NATS)
   - Implement read replicas for analytics
   - Shard by monitor ID across servers

## Security Architecture

### Authentication

- **Checkers to Server**: API key in request body (over HTTPS recommended)
- **SPA to Server**: No authentication (public status page by default)
- **Reverse Proxy**: Can add authentication layer in front

### Data Protection

- **In Transit**: Use HTTPS for all remote checker communication
- **At Rest**: DuckDB file permissions (mode 640, owned by service user)
- **Secrets**: Store API keys in environment variables or secrets manager, never in config files

### Network Isolation

- Expose only `/checker/*` endpoints to the internet
- Keep internal API endpoints (`/config`, `/uptime-data`, etc.) private
- Use firewall rules or reverse proxy path-based routing

## Observability Integration

### Sentry

All components (server, checker, workers) emit traces and logs to Sentry:

- Error reporting for unhandled panics
- Distributed traces for request tracing across checkers and workers
- Custom metrics for monitoring throughput and latency
- Continuous profiling for performance analysis

See `docs/operational.md` for configuration details.

## Next Steps

- See `docs/deployment.md` for deployment instructions
- See `docs/configuration.md` for all configuration options
- See `docs/operational.md` for monitoring and troubleshooting
- See `docs/api.md` for endpoint reference
