# Installation Scripts

Containers can run a bash script during creation. This runs before the container's main command and replaces the entrypoint/cmd for that run.

## How It Works

When you include `install_script` in the create request, the agent:
1. Creates the container with a modified entrypoint that runs your script first
2. The script runs as the container user (UID 1000, non-root)
3. After script completes, the original entrypoint/cmd runs
4. If script fails (non-zero exit), container creation fails

## Usage

Include in create request:

```json
{
  "name": "my-container",
  "image": "nginx:latest",
  "install_script": "#!/bin/bash\napt-get update && apt-get install -y curl jq\nnpm install --prefix /app\n",
  "cmd": ["nginx", "-g", "daemon off;"]
}
```

The script:
- Must be valid bash
- Runs with `set -e` (exits on error)
- Has access to container environment variables
- Runs in the container's working directory
- Runs as UID 1000 (non-root)

## Security

- Script runs unprivileged (same as container user)
- No access to host filesystem (only mounted volumes)
- No Docker socket access
- Cannot install system packages requiring root (use `apk add --no-cache` in Alpine, or build a custom image instead)
- Script content is not logged (only success/failure)

## Examples

### Install runtime dependencies

```bash
#!/bin/bash
# For Debian/Ubuntu based images
apt-get update && apt-get install -y --no-install-recommends \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*
```

### Install language packages

```bash
#!/bin/bash
# Node.js
npm ci --prefix /app --production

# Python
pip install --no-cache-dir -r /app/requirements.txt

# Go
go mod download
```

### Setup configuration

```bash
#!/bin/bash
# Generate config from env
cat > /app/config.yaml <<EOF
database:
  host: ${DB_HOST}
  port: ${DB_PORT}
  name: ${DB_NAME}
EOF

# Run migrations
/app/migrate up
```

### Download assets

```bash
#!/bin/bash
# Download and extract
curl -L https://example.com/assets.tar.gz | tar -xz -C /app/public
```

## Limitations

- Script runs once at container creation, not on restart
- No root access - cannot modify system files, bind mount privileged ports, etc
- No interactive input - script must be fully automated
- Timeouts apply (Docker default create timeout)
- Large scripts increase container creation time
- Script failures leave container in created state (not running)

## Best Practices

1. Keep scripts small and fast
2. Use `set -e` (automatic) and `set -o pipefail`
3. Clean up package caches in same RUN layer
4. Prefer building custom images for complex setups
5. Test scripts locally first with `docker run --rm -it image bash -c "script"`
6. Use environment variables for configuration, not hardcoded values

## Alternative

For complex initialization, consider:
- Building a custom Docker image with all dependencies
- Using an init container pattern (sidecar that prepares volume)
- Running setup as part of the application startup (in entrypoint script)