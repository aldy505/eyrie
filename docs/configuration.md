# Configuration

Eyrie supports configuration through command-line flags, YAML files, and environment variables. This document covers all configuration options for server mode, checker mode, and monitor definitions.

## Configuration Precedence

Configuration is applied in this order (highest to lowest priority):
1. Environment variables
2. Command-line flags
3. YAML file values
4. Built-in defaults

Example: `LOG_LEVEL=DEBUG` environment variable overrides `log_level: INFO` in the YAML file.

## Server Mode Configuration

### Command-Line Flags

```bash
eyrie -mode=server \
  -config=/etc/eyrie/server.yaml \
  -monitor=/etc/eyrie/monitor.yaml
```

- `-mode`: Set to `server` for server mode (required)
- `-config`: Path to server configuration YAML file
- `-monitor`: Path to monitor configuration YAML file

### YAML File Structure

See `example_configurations/server.example.yaml` for a complete example.

#### Server Settings

```yaml
server:
  host: "0.0.0.0"           # Bind address
  port: 8600                # Listening port
  log_level: "INFO"         # DEBUG, INFO, WARN, ERROR
```

#### Status Page Metadata

```yaml
metadata:
  title: "Status Page"      # Title shown in SPA
  show_last_updated: true   # Display last update timestamp
```

#### Registered Checkers

```yaml
registered_checkers:
  - name: "us-east-1-public-checker"      # Optional checker name
    region: "us-east-1"                   # Required region identifier
    api_key: "us-east-1-api-key-here"     # Required API key for auth
  - name: "eu-west-1-public-checker"
    region: "eu-west-1"
    api_key: "eu-west-1-api-key-here"
```

Each checker must have:
- `region`: Identifier for the geographic location
- `api_key`: Secret for authentication (shared between server and checker)
- `name`: Optional; if omitted, region is used as the checker's identity

#### Database Configuration

```yaml
database:
  path: "/var/lib/eyrie/eyrie.db"   # DuckDB file path
                                    # Default: eyrie.db (current directory)
```

The path must be writable. On first run, DuckDB creates the database and applies migrations.

#### Task Queue Configuration

Task queues connect checker submissions to worker processes. Format is backend-specific:

```yaml
task_queue:
  processor:
    producer_address: "mem://processor_tasks"    # In-memory (local only)
    consumer_address: "mem://processor_tasks"
  ingester:
    producer_address: "mem://ingester_tasks"
    consumer_address: "mem://ingester_tasks"
  alerter:
    producer_address: "mem://alerter_tasks"
    consumer_address: "mem://alerter_tasks"
```

Backend Options:

- **In-Memory** (development): `mem://queue_name`
- **Kafka**: `kafka://broker1:9092?topic=queue_name`
- **NATS**: `nats://nats.example.com?subject=queue_name`
- **RabbitMQ**: `amqp://user:password@rabbitmq:5672/queue_name`

#### Dataset Thresholds

Controls when incidents are created and incident severity levels:

```yaml
dataset:
  retention_days: 90                        # How long to keep raw data
  processing_lookback_minutes: 10           # Time window for incident detection
  per_region_failure_threshold_percent: 40  # Region fails if X% of checks fail
  failure_threshold_percent: 50             # Global fails if X% of checks fail
  degraded_threshold_minutes: 10            # Mark as degraded after X minutes of failures
  failure_threshold_minutes: 15             # Mark as failed after X minutes of failures
```

Example: If 50% or more checks fail for 15 minutes, the monitor transitions to "down".

#### Alerting Configuration

```yaml
alerting:
  webhook:
    enabled: false
    url: "https://example.com/webhook"
    hmac_secret: "your-hmac-secret-here"
    custom_headers:
      X-Environment: "production"
  
  slack:
    enabled: false
    webhook_url: "https://hooks.slack.com/services/XXX/YYY/ZZZ"
  
  discord:
    enabled: false
    webhook_url: "https://discord.com/api/webhooks/XXX/YYY"
  
  teams:
    enabled: false
    webhook_url: "https://example.webhook.office.com/webhookb2/..."
  
  ntfy:
    enabled: false
    topic_url: "https://ntfy.sh/eyrie-alerts"
    access_token: "ntfy-access-token"       # Optional
    username: "username"                    # For basic auth
    password: "password"                    # For basic auth
    priority: 4                             # 0-5, higher = more urgent
```

Multiple alerters can be enabled simultaneously. Alerts are sent to all enabled targets.

#### Sentry Configuration

```yaml
sentry:
  dsn: "https://examplePublicKey@o0.ingest.sentry.io/0"
  error_sample_rate: 1.0          # 0.0-1.0, sample rate for errors
  traces_sample_rate: 0.1         # 0.0-1.0, sample rate for traces
  profiling_sample_rate: 0.1      # 0.0-1.0, sample rate for profiling
  debug: false                    # Enable Sentry debug logging
  trace_outgoing_requests: false  # Trace HTTP requests to checkers
```

Leave `dsn` empty to disable Sentry.

### Environment Variables

All YAML configuration can also be set via environment variables. The mapping is hierarchical:

**Examples:**

```bash
# Server settings
export SERVER_HOST="0.0.0.0"
export SERVER_PORT="8600"
export SERVER_LOG_LEVEL="INFO"

# Metadata
export METADATA_TITLE="My Status Page"
export METADATA_SHOW_LAST_UPDATED="true"

# Database
export DATABASE_PATH="/var/lib/eyrie/eyrie.db"

# Dataset thresholds
export DATASET_RETENTION_DAYS="90"
export DATASET_PROCESSING_LOOKBACK_MINUTES="10"
export DATASET_PER_REGION_FAILURE_THRESHOLD_PERCENT="40"
export DATASET_FAILURE_THRESHOLD_PERCENT="50"
export DATASET_DEGRADED_THRESHOLD_MINUTES="10"
export DATASET_FAILURE_THRESHOLD_MINUTES="15"

# Alerting (Slack example)
export ALERTING_SLACK_ENABLED="true"
export ALERTING_SLACK_WEBHOOK_URL="https://hooks.slack.com/services/..."

# Sentry
export SENTRY_DSN="https://examplePublicKey@o0.ingest.sentry.io/0"
export SENTRY_TRACES_SAMPLE_RATE="0.1"
```

## Checker Mode Configuration

### Command-Line Flags

```bash
eyrie -mode=checker -config=/etc/eyrie/checker.yaml
```

- `-mode`: Set to `checker` for checker mode (required)
- `-config`: Path to checker configuration YAML file (optional)

### YAML File Structure

See `example_configurations/checker.example.yaml` for a complete example.

```yaml
upstream_url: "http://127.0.0.1:8600"     # Server URL to register with
name: "us-east-1-public-checker"          # Optional; defaults to region
region: "us-east-1"                       # Required region identifier
api_key: "us-east-1-api-key-here"         # Required API key

sentry:
  dsn: ""
  error_sample_rate: 1.0
  traces_sample_rate: 1.0
  profiling_sample_rate: 0.1
  debug: false
  trace_outgoing_requests: true            # Trace calls to server
```

### Environment Variables

Checker configuration is primarily environment-based for containerized deployments:

```bash
export UPSTREAM_URL="http://server.example.com:8600"
export CHECKER_NAME="us-east-1-public-checker"    # Optional
export REGION="us-east-1"
export API_KEY="us-east-1-api-key-here"

# Sentry
export SENTRY_DSN="https://examplePublicKey@o0.ingest.sentry.io/0"
export SENTRY_TRACES_SAMPLE_RATE="1.0"
export SENTRY_TRACE_OUTGOING_REQUESTS="true"
export SENTRY_PROFILING_SAMPLE_RATE="0.1"
export SENTRY_DEBUG="false"
```

## Monitor Configuration

### YAML Structure

See `example_configurations/monitor.example.yaml` for a complete example.

```yaml
monitors:
  - id: "unique-monitor-id"
    name: "Human Readable Name"
    description: "What this monitor checks"
    interval: "1m"                        # Check interval (Go duration format)
    type: "http"                          # Monitor type
    checker_names:                        # Optional; if empty, runs on all checkers
      - "us-east-1-public-checker"
    http:                                 # Type-specific config
      method: "GET"
      url: "https://example.com"
      # ... more fields below

groups:
  - id: "group-id"
    name: "Service Group"
    description: "Related monitors"
    monitor_ids:
      - "monitor-1"
      - "monitor-2"
```

### Monitor Types and Configuration

#### HTTP

```yaml
- id: "http-example"
  name: "Example Website"
  type: "http"
  http:
    method: "GET"                              # HTTP method
    url: "https://example.com"                 # Target URL
    headers:                                   # Optional custom headers
      User-Agent: "EyrieMonitor/1.0"
    expected_status_codes:                     # List of acceptable codes
      - 200
      - 301
      - 302
    skip_tls_verify: false                     # TLS verification
    timeout_seconds: 30
    jq_assertion: ".status == 'ok'"            # Optional JQ validation
```

#### TCP

```yaml
- id: "tcp-example"
  name: "Database Port"
  type: "tcp"
  tcp:
    address: "db.example.com:5432"             # host:port
    use_tls: false                             # Use TLS
    skip_tls_verify: false                     # Skip verification
    send: "PING\n"                             # Optional data to send
    expect_contains: "PONG"                    # Optional response check
    timeout_seconds: 15
```

#### ICMP

```yaml
- id: "icmp-example"
  name: "Ping Example"
  type: "icmp"
  icmp:
    host: "1.1.1.1"                            # Host to ping
    count: 1                                   # Ping packet count
    timeout_seconds: 5
```

#### Redis

```yaml
- id: "redis-example"
  name: "Redis Cache"
  type: "redis"
  redis:
    address: "redis.example.com:6379"          # host:port
    password: "optional-password"              # Optional
    database: 0                                # Database number
    use_tls: false                             # TLS support
    skip_tls_verify: false
    timeout_seconds: 10
```

#### PostgreSQL

```yaml
- id: "postgres-example"
  name: "Primary Database"
  type: "postgres"
  postgres:
    dsn: "postgres://user:password@localhost:5432/database?sslmode=disable"
    timeout_seconds: 10
```

DSN Format: `postgres://[user[:password]@][netloc][:port][/dbname][?param1=value1&...]`

Common parameters:
- `sslmode=disable` - No SSL
- `sslmode=require` - SSL required
- `sslmode=prefer` - Try SSL, fall back to non-SSL

#### MySQL

```yaml
- id: "mysql-example"
  name: "MySQL Database"
  type: "mysql"
  mysql:
    dsn: "user:password@tcp(localhost:3306)/database"
    timeout_seconds: 10
```

DSN Format: `[user[:password]@][protocol[(address)]]/dbname[?param1=value1&...]`

Common parameters:
- `tls=true` - Require TLS
- `tls=skip-verify` - TLS without certificate verification
- `charset=utf8mb4` - Character set

#### Microsoft SQL Server

```yaml
- id: "mssql-example"
  name: "SQL Server"
  type: "mssql"
  mssql:
    dsn: "sqlserver://sa:Password123!@localhost:1433?database=master"
    timeout_seconds: 10
```

DSN Format: `sqlserver://[user[:password]@][host][:port][?param1=value1&...]`

Common parameters:
- `database=name` - Database name
- `encrypt=true` - Require encryption
- `TrustServerCertificate=true` - Skip certificate verification

#### ClickHouse

```yaml
- id: "clickhouse-tcp-example"
  name: "ClickHouse (native)"
  type: "clickhouse"
  clickhouse:
    dsn: "clickhouse://default:@localhost:9000/default"
    timeout_seconds: 10

- id: "clickhouse-http-example"
  name: "ClickHouse (HTTP)"
  type: "clickhouse"
  clickhouse:
    dsn: "clickhouse://default:@localhost:8123/default?protocol=http"
    timeout_seconds: 10
```

DSN Format: `clickhouse://[user[:password]@][host][:port][/database][?param1=value1&...]`

- Native TCP (default): `clickhouse://user:password@host:9000/database`
- HTTP: `clickhouse://user:password@host:8123/database?protocol=http`

### Checker Targeting

Target monitors to specific checkers with `checker_names`:

```yaml
monitors:
  - id: "internal-db"
    name: "Internal Database"
    type: "postgres"
    checker_names:
      - "us-east-1-public-checker"          # Only run on this checker
    postgres:
      dsn: "postgres://internal.db:5432/prod?sslmode=require"

  - id: "public-api"
    name: "Public API"
    type: "http"
    # No checker_names = runs on all checkers
    http:
      url: "https://api.example.com/health"
```

Checker names must match the `registered_checkers[].name` in the server config, or the `region` if name is not set.

### Monitor Grouping

Group monitors for status page organization:

```yaml
groups:
  - id: "api-services"
    name: "API Services"
    monitor_ids:
      - "api-health"
      - "api-database"
      - "api-cache"

  - id: "background-jobs"
    name: "Background Jobs"
    monitor_ids:
      - "job-processor"
      - "job-queue"
```

Monitors not in any group still appear on the status page.

### Interval Format

Intervals use Go duration format:

```
h   - hours
m   - minutes
s   - seconds
ms  - milliseconds
```

Examples:
- `1s` - 1 second (not recommended, very frequent)
- `30s` - 30 seconds
- `1m` - 1 minute (common for critical services)
- `5m` - 5 minutes
- `1h` - 1 hour (for less critical checks)

## JSON Schemas

Machine-readable JSON schemas are available in the `docs/jsonschema/` directory:

- `docs/jsonschema/server.schema.json` - Server configuration schema
- `docs/jsonschema/checker.schema.json` - Checker configuration schema
- `docs/jsonschema/monitor.schema.json` - Monitor configuration schema

These schemas can be used by editors (VS Code, IDE) for validation and autocomplete.

### VS Code Setup

Add to `.vscode/settings.json`:

```json
{
  "yaml.schemas": {
    "./docs/jsonschema/server.schema.json": "server*.yaml",
    "./docs/jsonschema/checker.schema.json": "checker*.yaml",
    "./docs/jsonschema/monitor.schema.json": "monitor*.yaml"
  }
}
```

## Next Steps

- See `docs/deployment.md` for deployment instructions
- See `docs/operational.md` for monitoring and troubleshooting
- See `docs/architecture.md` for system design
- See `docs/api.md` for endpoint reference
