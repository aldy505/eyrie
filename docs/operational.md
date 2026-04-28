# Operations

This guide covers monitoring, observability, troubleshooting, and recovery procedures for running Eyrie in production.

## Sentry Observability

Eyrie integrates with Sentry for error reporting, distributed tracing, custom metrics, and profiling. Configuration is available for both server and checker modes.

### Server Configuration

Add to `server.yaml`:

```yaml
sentry:
  dsn: "https://examplePublicKey@o0.ingest.sentry.io/0"
  error_sample_rate: 1.0           # Sample rate for errors (0.0-1.0)
  traces_sample_rate: 0.1          # Sample rate for traces (0.0-1.0)
  profiling_sample_rate: 0.1       # Sample rate for continuous profiling
  debug: false                      # Enable debug logging
  trace_outgoing_requests: false    # Trace requests to checkers
```

Or via environment variable:
```bash
export SENTRY_DSN="https://examplePublicKey@o0.ingest.sentry.io/0"
```

### Checker Configuration

Add to `checker.yaml`:

```yaml
sentry:
  dsn: "https://examplePublicKey@o0.ingest.sentry.io/0"
  error_sample_rate: 1.0
  traces_sample_rate: 1.0          # Sample all checker traces
  profiling_sample_rate: 0.1
  debug: false
  trace_outgoing_requests: true    # Trace requests to server
```

Or via environment variables:
```bash
export SENTRY_DSN="https://examplePublicKey@o0.ingest.sentry.io/0"
export SENTRY_TRACES_SAMPLE_RATE=1.0
export SENTRY_TRACE_OUTGOING_REQUESTS=true
```

### Metrics

Sentry custom metrics are emitted for:

- **Checker cycles**: Check execution time and results per monitor
- **Monitor submissions**: Submission receipt, processing, and ingestion
- **Incident transitions**: Monitor state changes (healthy -> degraded -> down)
- **Alert delivery**: Webhook, Slack, Discord, Teams, and ntfy delivery outcomes

Access metrics in Sentry's Dashboards and Metrics Explore interfaces.

### Tracing

Distributed traces include:

- Checker registration and configuration fetch
- Monitor execution spans for each probe type
- Server submission processing and incident updates
- Worker pipelines (ingester, processor, alerter)

Use Sentry's Trace Details to debug multi-step failures across checkers and the server.

## DuckDB Recovery

### WAL (Write-Ahead Log) Issues

If the server fails to start with an error like:

```
Failure while replaying WAL file ... GetDefaultDatabase with no default database set
```

DuckDB is recovering from an incomplete transaction in the WAL file before Eyrie can run migrations.

### Recovery Steps

1. Back up both files before proceeding:
   ```bash
   cp /var/lib/eyrie/eyrie.db /backups/eyrie.db.backup
   cp /var/lib/eyrie/eyrie.db.wal /backups/eyrie.db.wal.backup
   ```

2. If recent data loss is acceptable:
   ```bash
   rm /var/lib/eyrie/eyrie.db.wal
   systemctl start eyrie-server
   ```

3. If the WAL may contain important data, use an export/import approach:
   ```bash
   # On a development machine or recovery instance with a newer DuckDB:
   duckdb -c "ATTACH '/backups/eyrie.db' AS old; CREATE DATABASE new_db; .backup new_db /var/lib/eyrie/eyrie.db.recovered"
   ```

   Then copy the recovered database:
   ```bash
   cp /var/lib/eyrie/eyrie.db.recovered /var/lib/eyrie/eyrie.db
   systemctl start eyrie-server
   ```

### Prevention

- Use `systemctl stop eyrie-server` or `kill -SIGTERM <pid>` for clean shutdown
- Eyrie handles both SIGINT and SIGTERM for graceful termination
- Monitor disk space to ensure WAL writes don't fail

## Health Checks

### Server Health

Check the status endpoint:

```bash
curl -s http://localhost:8600/config | jq .
```

Should return a 200 OK with metadata. If this fails, the server is not responding.

### Monitor Incidents

Query current monitor health:

```bash
curl -s http://localhost:8600/monitor-incidents | jq .
```

This returns all monitors with their current incident status (healthy, degraded, down).

### Checker Registration

View registered checkers by checking the server logs:

```bash
journalctl -u eyrie-server -f | grep register
```

Or check the server configuration to see expected regions.

## Logging

### Server Logs

Configure log level in `server.yaml`:

```yaml
server:
  log_level: "INFO"    # DEBUG, INFO, WARN, ERROR
```

Or via environment variable:
```bash
export LOG_LEVEL=INFO
```

View logs with systemd:

```bash
sudo journalctl -u eyrie-server -f          # Follow logs
sudo journalctl -u eyrie-server --since=-1h  # Last hour
sudo journalctl -u eyrie-server -n 100      # Last 100 lines
```

### Checker Logs

Same configuration approach:

```yaml
upstream_url: "http://localhost:8600"
region: "us-east-1"
api_key: "api-key-here"
# Log level is inherited from default (INFO)
```

View checker logs:

```bash
sudo journalctl -u eyrie-checker-us-east-1 -f
```

### Log Levels

- **DEBUG**: Verbose output including probe details, timing, and request/response bodies
- **INFO**: General operational messages (registration, submission, incident transitions)
- **WARN**: Unexpected conditions that don't prevent operation
- **ERROR**: Failures requiring attention

## Troubleshooting

### Checker Not Registering

1. Verify network connectivity:
   ```bash
   curl -v http://<server>:8600/config
   ```

2. Check API key:
   - Ensure `API_KEY` environment variable matches server's `registered_checkers[].api_key`
   - Verify case sensitivity

3. Check server logs:
   ```bash
   journalctl -u eyrie-server | grep register
   ```

4. Increase log level for detail:
   ```bash
   systemctl stop eyrie-checker-*
   CHECKER_NAME="us-east-1" LOG_LEVEL=DEBUG /usr/local/bin/eyrie -mode=checker -config=/etc/eyrie/checker.yaml
   ```

### Monitors Not Executing

1. Verify configuration syntax:
   ```bash
   go run . -mode=server -config=/etc/eyrie/server.yaml -monitor=/etc/eyrie/monitor.yaml 2>&1 | head -20
   ```

2. Check monitor interval:
   - All monitors must have an `interval` field or defaults to `1m`
   - Minimum interval is typically 1 second (use caution)

3. View checker submissions:
   ```bash
   curl -s http://localhost:8600/uptime-data | jq '.data[] | {id, latest_status}'
   ```

### High Memory or Disk Usage

1. Check database size:
   ```bash
   ls -lh /var/lib/eyrie/eyrie.db*
   ```

2. Reduce data retention if too large:
   ```yaml
   dataset:
     retention_days: 30  # Reduce from default 90
   ```

3. Monitor WAL file growth:
   ```bash
   watch -n 1 'ls -lh /var/lib/eyrie/eyrie.db*'
   ```

   If WAL grows continuously, there may be long-running transactions. Restart the server if safe.

### Incident State Mismatch

Incidents are programmatically derived. If the state appears incorrect:

1. Check threshold configuration:
   ```yaml
   dataset:
     per_region_failure_threshold_percent: 40   # % of checks that must fail
     failure_threshold_percent: 50               # Global threshold
     degraded_threshold_minutes: 10              # How long before degraded
     failure_threshold_minutes: 15               # How long before failed
   ```

2. View recent submissions:
   ```bash
   curl -s "http://localhost:8600/uptime-data?monitor_id=<id>" | jq '.data'
   ```

3. Check processor logs for incident decisions:
   ```bash
   journalctl -u eyrie-server | grep -i "incident\|processor"
   ```

## Monitoring and Alerting

### Key Metrics to Watch

- **Checker registration**: Has each region registered successfully?
- **Submission latency**: How long between monitor execution and server receipt?
- **Incident transition count**: Unexpected state changes may indicate threshold misconfiguration
- **Alert delivery success**: Are webhooks, Slack messages, etc. being sent?
- **Database size**: Is retention policy keeping data in check?

### Recommended Alerts

Set up alerts on these conditions:

1. Checker offline for 5+ minutes
2. Database file growing faster than expected
3. Server restarts (check WAL recovery issues)
4. Failed submissions (via Sentry error tracking)
5. Incident churn (many state transitions in short time)

## Backup and Recovery

### Data Backup Strategy

Backup the DuckDB file and WAL regularly:

```bash
#!/bin/bash
BACKUP_DIR=/backups/eyrie
mkdir -p $BACKUP_DIR

# Daily full backup (at off-peak time)
0 2 * * * cp /var/lib/eyrie/eyrie.db* $BACKUP_DIR/eyrie.db.$(date +\%Y-\%m-\%d).backup

# Rotate backups older than 30 days
30 2 * * * find $BACKUP_DIR -name "eyrie.db.*" -mtime +30 -delete
```

### Configuration Backup

Store configurations in version control:

```bash
git add server.yaml monitor.yaml checker.yaml
git commit -m "Configuration snapshot"
```

This allows quick recovery if files are accidentally modified.

### Full Recovery Procedure

1. Stop the server:
   ```bash
   sudo systemctl stop eyrie-server
   ```

2. Restore the database:
   ```bash
   cp /backups/eyrie.db.YYYY-MM-DD.backup /var/lib/eyrie/eyrie.db
   rm -f /var/lib/eyrie/eyrie.db.wal
   sudo chown eyrie:eyrie /var/lib/eyrie/eyrie.db
   ```

3. Restart:
   ```bash
   sudo systemctl start eyrie-server
   ```

4. Verify:
   ```bash
   curl -s http://localhost:8600/config | jq .
   ```

## Next Steps

- See `docs/configuration.md` for all configuration options
- See `docs/deployment.md` for deployment instructions
- See `docs/architecture.md` for system design and components
- See `docs/api.md` for endpoint reference
