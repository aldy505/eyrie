# Deployment

This guide covers deploying Eyrie using Docker and as a static binary with systemd integration.

## Requirements

- For server: DuckDB data directory (write permissions required)
- For checkers: Network access to the server's registration and submission endpoints
- Process manager (systemd recommended) for production deployments

## Docker Deployment

### Server with Docker Compose

Here is a basic Docker Compose setup:

```yaml
version: '3.8'

services:
  eyrie-server:
    build: .
    ports:
      - "8600:8600"
    volumes:
      - ./data:/var/lib/eyrie
      - ./server.yaml:/etc/eyrie/server.yaml:ro
      - ./monitor.yaml:/etc/eyrie/monitor.yaml:ro
    command: |
      -mode=server
      -config=/etc/eyrie/server.yaml
      -monitor=/etc/eyrie/monitor.yaml
    environment:
      - TZ=UTC
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8600/config"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s
```

### Checker with Docker Compose

Run multiple checkers in different regions:

```yaml
version: '3.8'

services:
  checker-us-east:
    build: .
    command: -mode=checker -config=/etc/eyrie/checker.yaml
    volumes:
      - ./checker.yaml:/etc/eyrie/checker.yaml:ro
    environment:
      - UPSTREAM_URL=http://eyrie-server:8600
      - REGION=us-east-1
      - API_KEY=us-east-1-api-key-here
      - TZ=UTC
    restart: unless-stopped
    depends_on:
      - eyrie-server

  checker-eu-west:
    build: .
    command: -mode=checker -config=/etc/eyrie/checker.yaml
    volumes:
      - ./checker.yaml:/etc/eyrie/checker.yaml:ro
    environment:
      - UPSTREAM_URL=http://eyrie-server:8600
      - REGION=eu-west-1
      - API_KEY=eu-west-1-api-key-here
      - TZ=UTC
    restart: unless-stopped
    depends_on:
      - eyrie-server
```

### Building the Docker Image

```bash
docker build -t eyrie:latest .
```

The Dockerfile includes:
- Go build stage to compile the backend
- Node.js stage to build the frontend SPA
- Final stage with embedded frontend and Go binary
- No root user execution (security best practice)

## Static Binary Deployment

### Building the Binary

```bash
go build -o eyrie .
```

The binary includes the embedded frontend, so no separate asset deployment is needed.

### Server with systemd

Create `/etc/systemd/system/eyrie-server.service`:

```ini
[Unit]
Description=Eyrie Uptime Monitor - Server
Documentation=https://github.com/aldy505/eyrie
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=eyrie
Group=eyrie
WorkingDirectory=/var/lib/eyrie

ExecStart=/usr/local/bin/eyrie \
  -mode=server \
  -config=/etc/eyrie/server.yaml \
  -monitor=/etc/eyrie/monitor.yaml

Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

# Security
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/eyrie

# Process limits
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable eyrie-server
sudo systemctl start eyrie-server
sudo systemctl status eyrie-server
```

View logs:

```bash
sudo journalctl -u eyrie-server -f
```

### Checker with systemd

Create `/etc/systemd/system/eyrie-checker-us-east-1.service`:

```ini
[Unit]
Description=Eyrie Uptime Monitor - Checker (us-east-1)
Documentation=https://github.com/aldy505/eyrie
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=eyrie
Group=eyrie

ExecStart=/usr/local/bin/eyrie \
  -mode=checker \
  -config=/etc/eyrie/checker.yaml

Environment="UPSTREAM_URL=http://server.example.com:8600"
Environment="REGION=us-east-1"
Environment="API_KEY=us-east-1-api-key-here"

Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

# Security
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=yes

[Install]
WantedBy=multi-user.target
```

For each region, create a separate service file with the corresponding `REGION` and `API_KEY`.

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable eyrie-checker-us-east-1
sudo systemctl start eyrie-checker-us-east-1
```

### Pre-flight Checklist

Before deploying to production:

1. Create the eyrie system user:
   ```bash
   sudo useradd --system --home /var/lib/eyrie --shell /usr/sbin/nologin eyrie
   ```

2. Create data directories:
   ```bash
   sudo mkdir -p /var/lib/eyrie
   sudo chown eyrie:eyrie /var/lib/eyrie
   sudo chmod 750 /var/lib/eyrie
   ```

3. Copy configuration files:
   ```bash
   sudo mkdir -p /etc/eyrie
   sudo cp server.yaml /etc/eyrie/
   sudo cp monitor.yaml /etc/eyrie/
   sudo cp checker.yaml /etc/eyrie/
   sudo chown root:eyrie /etc/eyrie/*.yaml
   sudo chmod 640 /etc/eyrie/*.yaml
   ```

4. Copy the binary:
   ```bash
   sudo cp eyrie /usr/local/bin/eyrie
   sudo chmod 755 /usr/local/bin/eyrie
   ```

5. Verify permissions and paths in service files

## Data Persistence

The DuckDB database file is stored at the path specified in `database.path` in the server configuration (default: `eyrie.db`).

When using Docker:
- Mount the data directory as a volume: `-v ./data:/var/lib/eyrie`
- Backup regularly: `cp /var/lib/eyrie/eyrie.db /backups/eyrie.db.$(date +%s)`

When using systemd:
- Configure `database.path` to point to a mounted filesystem with appropriate space and I/O performance
- Use systemd service isolation to restrict access

## Reverse Proxy Configuration

If deploying behind a reverse proxy (nginx, Apache, Traefik):

For the server to accept checker registration and submissions from remote networks, expose:
- `POST /checker/register`
- `POST /checker/submit`

The remaining endpoints can be private:
- `GET /config`
- `GET /uptime-data`
- `GET /uptime-data-by-region`
- `GET /monitor-incidents`
- `/` (SPA)

Example nginx configuration:

```nginx
upstream eyrie {
    server 127.0.0.1:8600;
}

server {
    listen 80;
    server_name status.example.com;

    location /checker/ {
        # Public for remote checkers
        proxy_pass http://eyrie;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location / {
        # Private for internal use
        allow 10.0.0.0/8;
        deny all;
        
        proxy_pass http://eyrie;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## Next Steps

- See `docs/configuration.md` for configuration file reference and environment variables
- See `docs/operational.md` for monitoring, troubleshooting, and recovery procedures
- See `docs/api.md` for endpoint details
