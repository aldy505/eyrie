# API Reference

Eyrie exposes a REST API for status page data, checker registration, and result submission. The SPA and remote checkers communicate through these endpoints.

## Base URL

The server API is exposed at `http://<host>:<port>` (default port: 8600).

## Public Endpoints

These endpoints are typically exposed publicly or to both internal and remote checkers.

### GET /config

Returns status page metadata and configuration.

**Response:**

```json
{
  "title": "Status Page",
  "show_last_updated": true
}
```

**Status Code:** 200 OK

---

### GET /uptime-data

Returns aggregated uptime data for all monitors and groups.

**Query Parameters:**

- `monitor_id` (optional): Filter to a specific monitor ID

**Response:**

```json
{
  "data": [
    {
      "id": "example-monitor-1",
      "name": "Example Monitor 1",
      "type": "http",
      "status": "healthy",
      "uptime_percent": 99.8,
      "avg_latency_ms": 245,
      "last_check_at": "2026-04-28T17:00:00Z",
      "recent_incidents": [
        {
          "id": "INC-001",
          "started_at": "2026-04-28T16:00:00Z",
          "resolved_at": "2026-04-28T16:15:00Z"
        }
      ]
    }
  ]
}
```

**Status Code:** 200 OK

---

### GET /uptime-data-by-region

Returns region-specific uptime breakdown for a monitor.

**Query Parameters:**

- `monitor_id` (required): Monitor ID to query

**Response:**

```json
{
  "monitor_id": "example-monitor-1",
  "regions": [
    {
      "region": "us-east-1",
      "uptime_percent": 99.9,
      "avg_latency_ms": 120,
      "last_check_at": "2026-04-28T17:00:00Z"
    },
    {
      "region": "eu-west-1",
      "uptime_percent": 99.7,
      "avg_latency_ms": 280,
      "last_check_at": "2026-04-28T17:00:00Z"
    }
  ]
}
```

**Status Code:** 200 OK

---

### GET /monitor-incidents

Returns current monitor health status and active incidents.

**Response:**

```json
{
  "data": [
    {
      "id": "example-monitor-1",
      "name": "Example Monitor 1",
      "status": "healthy",
      "severity": "operational",
      "active_incident": null,
      "last_incident_at": "2026-04-28T16:15:00Z"
    },
    {
      "id": "example-monitor-2",
      "name": "Example Monitor 2",
      "status": "down",
      "severity": "major_outage",
      "active_incident": {
        "id": "INC-002",
        "started_at": "2026-04-28T16:30:00Z"
      },
      "last_incident_at": "2026-04-28T16:30:00Z"
    }
  ]
}
```

**Status Code:** 200 OK

---

## Checker Endpoints

These endpoints are used by checker nodes for registration and result submission. Authentication is via API key in the request body.

### POST /checker/register

Checker registration and monitor configuration fetch.

**Request Body:**

```json
{
  "region": "us-east-1",
  "api_key": "us-east-1-api-key-here",
  "name": "us-east-1-public-checker"
}
```

**Response (200 OK):**

```json
{
  "monitors": [
    {
      "id": "example-monitor-1",
      "name": "Example Monitor 1",
      "type": "http",
      "interval": "1m",
      "http": {
        "method": "GET",
        "url": "https://example.com",
        "expected_status_codes": [200],
        "timeout_seconds": 30
      }
    }
  ]
}
```

**Error Responses:**

- 401 Unauthorized: Invalid API key
- 400 Bad Request: Missing required fields

---

### POST /checker/submit

Submit monitor check results.

**Request Body:**

```json
{
  "region": "us-east-1",
  "api_key": "us-east-1-api-key-here",
  "submissions": [
    {
      "monitor_id": "example-monitor-1",
      "timestamp": "2026-04-28T17:00:00Z",
      "latency_ms": 245,
      "status_code": 200,
      "success": true,
      "error_message": null,
      "details": {}
    }
  ]
}
```

**Response (200 OK):**

```json
{
  "acknowledged": 1
}
```

**Error Responses:**

- 401 Unauthorized: Invalid API key
- 400 Bad Request: Malformed submission

---

## Status Codes

| Code | Meaning |
| --- | --- |
| 200 | Success |
| 400 | Bad Request (missing/invalid fields) |
| 401 | Unauthorized (invalid API key) |
| 404 | Not Found |
| 500 | Internal Server Error |

## Authentication

Checker endpoints (`/checker/*`) use API key authentication. The key is passed in the request body as `api_key`.

API keys must match the `api_key` field in the server's `registered_checkers` configuration.

## Rate Limiting

Currently, no rate limiting is implemented. Excessive requests may impact performance.

## SPA Frontend

The embedded SPA is served at `/` and communicates with the above endpoints to display status, history, and incident information. No additional API calls are needed beyond what's documented here.

## Next Steps

- See `docs/configuration.md` for configuration details
- See `docs/architecture.md` for system design and data flow
