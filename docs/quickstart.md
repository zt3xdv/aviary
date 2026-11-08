# Quick Start

## Prerequisites

- Go 1.21+ (for building from source)
- Docker daemon running
- Network access to Docker socket

## 1. Generate Keys

On the panel machine (or wherever you'll generate tokens):

```bash
go run cmd/keygen/main.go -output keys
```

This creates:
- `keys/panel_public.key` - Public key (base64) for agent config
- `keys/panel_private.key` - Private key for token generation

## 2. Create Docker Network for Daemon Communication

The agent requires a dedicated Docker network for communicating with container daemons:

```bash
docker network create aviary-network
```

## 3. Build Daemon Image

The in-container daemon must be baked into your container images:

```bash
# Build static daemon binary
CGO_ENABLED=0 go build -o /tmp/aviary-daemon ./cmd/daemon

# Build base image with daemon
cat > /tmp/Dockerfile.daemon <<'DOCKERFILE'
FROM alpine:latest
COPY aviary-daemon /usr/local/bin/aviary-daemon
RUN chmod +x /usr/local/bin/aviary-daemon
DOCKERFILE

docker build -t alpine-with-daemon -f /tmp/Dockerfile.daemon /tmp
```

Use `alpine-with-daemon` (or your custom image) as the base for containers.

## 4. Configure Agent

Create `config.yaml`:

```yaml
server:
  address: "0.0.0.0"
  port: 8080
  read_timeout: "30s"
  write_timeout: "30s"
  idle_timeout: "120s"
  tls:
    enabled: false
    cert_file: ""
    key_file: ""

docker:
  host: "unix:///var/run/docker.sock"
  api_version: "1.44"
  timeout: "60s"
  max_concurrent: 10
  default_memory_mb: 512
  default_cpu_cores: 1.0
  default_pids: 100
  default_disk_mb: 10240
  daemon_network: "aviary-network"  # REQUIRED

auth:
  panel_public_key: "BASE64_PUBLIC_KEY_FROM_STEP_1"
  token_expiry: "1h"
  allowed_ips:
    - "10.0.0.0/8"
    - "192.168.0.0/16"

metrics:
  enabled: true
  address: "0.0.0.0"
  port: 9090
  path: "/metrics"

logging:
  level: "info"
  format: "json"
  output: "stdout"
```

## 5. Run Agent

```bash
# Build agent
go build -o aviary-agent ./cmd/aviary

# Run
./aviary-agent -config config.yaml
```

Agent starts on port 8080. Health check: `GET /health`

## 6. Generate Token

```bash
TOKEN=$(go run cmd/keygen/main.go -token -panel-id "panel-1" -private-key keys/panel_private.key -expiry 1h)
```

## 7. Test API

```bash
# Create container (uses DAEMON_COMMAND env for app command)
curl -H "Authorization: Bearer $TOKEN" \
  -H "X-Real-IP: 127.0.0.1" \
  -X POST -H "Content-Type: application/json" \
  -d '{"name":"test","image":"alpine-with-daemon","env":["DAEMON_COMMAND=sleep infinity"],"network":"aviary-network"}' \
  http://localhost:8080/api/v1/containers/create

# Start container
curl -H "Authorization: Bearer $TOKEN" -H "X-Real-IP: 127.0.0.1" -X POST \
  "http://localhost:8080/api/v1/containers/<CONTAINER_ID>?action=start"

# List files
curl -H "Authorization: Bearer $TOKEN" -H "X-Real-IP: 127.0.0.1" \
  "http://localhost:8080/api/v1/files?container_id=<CONTAINER_ID>&path=/"

# Write file
curl -H "Authorization: Bearer $TOKEN" -H "X-Real-IP: 127.0.0.1" \
  -X POST -H "Content-Type: application/octet-stream" \
  --data "hello" \
  "http://localhost:8080/api/v1/files/app/test.txt?container_id=<CONTAINER_ID>&action=write"

# Read file
curl -H "Authorization: Bearer $TOKEN" -H "X-Real-IP: 127.0.0.1" \
  "http://localhost:8080/api/v1/files/app/test.txt?container_id=<CONTAINER_ID>&action=read"

# Process management
curl -H "Authorization: Bearer $TOKEN" -H "X-Real-IP: 127.0.0.1" \
  "http://localhost:8080/api/v1/files/process?container_id=<CONTAINER_ID>"
curl -X POST -H "Authorization: Bearer $TOKEN" -H "X-Real-IP: 127.0.0.1" \
  "http://localhost:8080/api/v1/files/process/stop?container_id=<CONTAINER_ID>"
```

## Next Steps

- [API Reference](api.md) - Full endpoint documentation
- [Configuration](config.md) - All config options
- [File Manager](file-manager.md) - File operations
- [WebSocket](websocket.md) - Logs, stats, exec streaming
