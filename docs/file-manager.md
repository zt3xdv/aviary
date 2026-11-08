# File Manager API

The file manager lets you read, write, and manage files inside containers. **Unlike the old daemon-based architecture, file operations now use a host filesystem bind mount (like Pterodactyl Wings)** - the agent operates directly on `/var/lib/aviary/volumes/<container-name>` on the host. This means **files work even when the container is stopped**.

## Agent API Endpoints

All endpoints are relative to the agent's base URL (default port 8080 on host).

**All endpoints require:**
- `Authorization: Bearer <token>` header
- `container_id` query parameter (container ID or name)

### File Operations

Base path: `/api/v1/files/`

#### List Files

**GET** `/api/v1/files?container_id=<id>&path=/path`

Query parameters:
- `container_id` - container ID or name (required)
- `path` - path in container (default: `/`)

Response: array of file entries

```json
[
  {
    "name": "app.go",
    "path": "/app/app.go",
    "size": 1024,
    "mode": "644",
    "mod_time": "2024-01-15T10:30:00Z",
    "is_dir": false
  }
]
```

Mode is octal string (e.g. "644" = regular file, "755" = directory). Results sorted: directories first, then alphabetically.

#### Stat File (Metadata)

**GET** `/api/v1/files/<path>?container_id=<id>&action=stat`

Query parameters:
- `container_id` - container ID or name (required)
- `action=stat` - required to get metadata instead of directory listing

Response: single file entry object (same format as list).

#### Read File (Raw Content)

**GET** `/api/v1/files/<path>?container_id=<id>&action=read`

Or with query parameters:
- `?raw=true`
- `?download=true`

Response: raw file content with `Content-Type: application/octet-stream` and `Content-Disposition: attachment`.

#### Write File (Raw)

**POST** `/api/v1/files/<path>?container_id=<id>&action=write`

Request body: raw file content
Headers: `Content-Type: application/octet-stream` (or any)
Content-Length: required

Response: `{"status": "written"}`

Creates parent directories if needed. Overwrites existing file. Streams directly to disk (no full buffering).

#### Create Directory

**POST** `/api/v1/files/<path>?container_id=<id>&action=mkdir`

Query parameters:
- `mode` - octal mode (default: 0755)

Request body: optional `{"mode": 493}`

Response: `{"status": "created"}`

Creates parent directories recursively.

#### Copy File/Directory

**POST** `/api/v1/files/<src-path>?container_id=<id>&action=copy&dest=/dest/path`

Query parameters:
- `dest` - destination path (required)

Response: `{"status": "copied"}`

Copies file or directory recursively. Overwrites destination if exists.

#### Move/Rename File/Directory

**PUT** `/api/v1/files/<src-path>?container_id=<id>&dest=/dest/path`

Query parameters:
- `dest` - destination path (required)

Response: `{"status": "moved"}`

Moves/renames file or directory.

#### Change Permissions (chmod)

**POST** `/api/v1/files/<path>/chmod?container_id=<id>&action=chmod`

Request body: `{"mode": 493}` (octal, default: 0644)

Response: `{"status": "changed"}`

Note: The URL path includes `/chmod` suffix which is stripped by the agent.

#### Delete File/Directory

**DELETE** `/api/v1/files/<path>?container_id=<id>`

Response: `{"status": "deleted"}`

Recursively removes directories. **Cannot delete the volume root** (containment protection).

#### Update File (Text Editor)

**POST** `/api/v1/files/<path>?container_id=<id>&action=update`

Same as write but semantically for text editor updates.

Request body: raw file content
Content-Length: required

Response: `{"status": "updated"}`

---

### Process Management

Base path: `/api/v1/files/process` (handled directly by agent via Docker API)

#### Get Process Info

**GET** `/api/v1/files/process?container_id=<id>`

Response:
```json
{
  "pid": 123,
  "state": "running",
  "exit_code": 0,
  "started_at": "2024-01-15T10:30:00Z"
}
```

States: `running`, `stopped`, `exited`, `failed`, `created`

#### Start Process

**POST** `/api/v1/files/process/start?container_id=<id>`

Response: `{"status": "started"}`

Starts the container (equivalent to `docker start`).

#### Stop Process

**POST** `/api/v1/files/process/stop?container_id=<id>&timeout=10s`

Query parameters:
- `timeout` - duration to wait before SIGKILL (default: 10s)

Response: `{"status": "stopped"}`

Stops the container (equivalent to `docker stop`).

#### Restart Process

**POST** `/api/v1/files/process/restart?container_id=<id>&timeout=10s`

Response: `{"status": "restarted"}`

Restarts the container (equivalent to `docker restart`).

#### Send Signal

**POST** `/api/v1/files/process/signal?container_id=<id>`

Request body: `{"signal": "TERM"}`

Signals: TERM, KILL, INT, HUP, USR1, USR2

Response: `{"status": "signal sent"}`

Sends signal to container's PID 1 (equivalent to `docker kill --signal`).

---

## Examples

### Using via Agent Proxy

```bash
# List files in /app
curl -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files?container_id=abc123&path=/app"

# Stat a file (metadata)
curl -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/app/main.go?container_id=abc123&action=stat"

# Read a file (raw)
curl -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/app/main.go?container_id=abc123&action=read" \
  -o main.go

# Write a file
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/octet-stream" \
  --data-binary @local-file.txt \
  "http://agent:8080/api/v1/files/app/remote.txt?container_id=abc123&action=write"

# Create directory
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": 493}' \
  "http://agent:8080/api/v1/files/app/newdir?container_id=abc123&action=mkdir"

# Copy file
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/app/file.txt?container_id=abc123&action=copy&dest=/app/backup.txt"

# Move file
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/app/old.txt?container_id=abc123&dest=/app/new.txt"

# Change permissions
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": 509}' \
  "http://agent:8080/api/v1/files/app/script.sh/chmod?container_id=abc123&action=chmod"

# Delete file
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/app/temp.txt?container_id=abc123"

# Update file (text editor)
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: text/plain" \
  --data "new content" \
  "http://agent:8080/api/v1/files/app/file.txt?container_id=abc123&action=update"

# Process management (now via Docker API, no daemon)
curl -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/process?container_id=abc123"
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/process/stop?container_id=abc123"
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/process/start?container_id=abc123"
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"signal": "TERM"}' \
  "http://agent:8080/api/v1/files/process/signal?container_id=abc123"
```

---

## Offline File Access

A key advantage of the host filesystem approach: **file operations work while the container is stopped**.

```bash
# 1. Stop container
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/process/stop?container_id=abc123"

# 2. Read file (still works!)
curl -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/data/config.json?container_id=abc123&action=read"

# 3. Write file (still works!)
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data '{"debug": true}' \
  "http://agent:8080/api/v1/files/data/config.json?container_id=abc123&action=write"

# 4. Start container again
curl -X POST -H "Authorization: Bearer $TOKEN" \
  "http://agent:8080/api/v1/files/process/start?container_id=abc123"
```

Files are stored at `/var/lib/aviary/volumes/<container-name>/` on the host.

---

## Security

- **Path containment**: All paths resolved relative to container's volume root; `..` traversal blocked
- **Symlink protection**: Symlinks resolved and verified to stay within volume root
- **Volume isolation**: Each container gets its own directory; no cross-container access
- **Non-root containers**: Default UID/GID 10000 (configurable via `storage.container_uid/gid`)
- **Mount allowlist**: User bind mounts restricted to `docker.allowed_mounts` paths (default: `/data`, `/mnt`, `/media`)
- **Docker hardening**: `no-new-privileges`, `CapDrop: ALL`, `ReadonlyRootfs` option

---

## Error Responses

- 400 - bad request (missing params, invalid JSON)
- 401 - unauthorized (missing or invalid token)
- 403 - forbidden (IP not allowed)
- 404 - file not found
- 409 - conflict (process already running)
- 500 - internal server error

---

## Configuration

### Storage Config (`config.yaml`)

```yaml
storage:
  root: "/var/lib/aviary/volumes"    # Host directory for container volumes
  container_uid: 10000                # UID for container processes
  container_gid: 10000                # GID for container processes
```

### Docker Config (Mount Allowlist)

```yaml
docker:
  allowed_mounts: ["/data", "/mnt", "/media"]  # Allowed host paths for bind mounts
```

The agent must run as root to create volumes and chown them to the container UID/GID.