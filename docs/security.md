# Security

## Authentication Model

The agent uses Ed25519-signed binary tokens for authentication. The panel holds the private key and generates tokens. The agent holds the public key and validates tokens. No shared secrets or password auth.

Token contents:
- `panel_id` - identifies the panel
- `issued_at` - Unix timestamp
- `expires_at` - Unix timestamp
- `nonce` - random 16 bytes (prevents replay)

Tokens are validated on every request. Expired tokens are rejected. The panel controls token expiry.

## IP Allowlist

Configure `auth.allowed_ips` with CIDR ranges. Requests from non-allowed IPs get 403. Empty list means allow all (not recommended).

## Container Isolation

All containers run with:
- Non-root user (UID 1000, GID 1000)
- `no-new-privileges: true` - prevents privilege escalation
- All capabilities dropped (`CapDrop: ALL`)
- Read-only root filesystem (optional, set `read_only: true` in create request)
- Resource limits enforced (memory, CPU, pids, disk)
- User namespace not used (containers see real UIDs)

The agent does not support privileged containers. The `privileged` field in create request is ignored.

## File Manager Security

File operations are restricted by:
- Path allowlist (default: `/app`, `/data`, `/home`, `/tmp`, `/var/www`, `/opt`)
- Path traversal detection (checks for `..` before path cleaning)
- Max file size limit (default 100MB)
- Operations require valid container ID
- All operations run as container user (UID 1000)

The allowlist can be configured in the container manager initialization.

## Network

The agent listens on configured address/port. No mTLS between panel and agent by default. Enable TLS in config for encrypted communication. WebSocket connections use same auth as HTTP.

## What This Does Not Have

- Rate limiting (add at reverse proxy)
- Audit logging
- Request/response logging
- Certificate pinning
- Automatic token rotation
- Multi-panel support (single public key)
- Role-based access (all tokens have full access)
- Secrets management
- Image scanning
- Network policies
- Compliance reporting

## Threat Model

This agent assumes:
- The panel is trusted (holds private key)
- The network between panel and agent is trusted (or TLS enabled)
- The Docker daemon is trusted (agent has full Docker socket access)
- The host OS is trusted

If any of these assumptions break, the security model breaks.

## Production Checklist

If you ignore the warning and use this in production anyway, at minimum:

1. Enable TLS with valid certificates
2. Restrict `allowed_ips` to panel IPs only
3. Put agent behind a reverse proxy with rate limiting
4. Monitor `/metrics` for anomalies
5. Audit Docker socket access (who else has it?)
6. Set resource limits lower than host capacity
7. Use read-only containers where possible
8. Regularly rotate panel keys
9. Log all panel-to-agent traffic
10. Have an incident response plan

This list is not exhaustive.