# Aviary

A lightweight Docker management agent for panel-to-agent control.

**Purpose**: Provide a minimal, secure agent that a panel can use to manage containers, images, and files on Docker hosts.

**Why it exists**: Most Docker agents are either too heavy or tied to specific panels (like Pterodactyl Wings). This one is standalone and minimal.

---

> [!IMPORTANT]
> This was not designed for production use.

## Quick Start

See [Quick Start Guide](docs/quickstart.md) for step-by-step setup.

## Documentation

All detailed documentation is in the `docs/` folder:

- [Quick Start](docs/quickstart.md) - Step-by-step setup guide
- [API Reference](docs/api.md) - Complete endpoint documentation
- [Configuration](docs/config.md) - All config options
- [Security](docs/security.md) - Security model and limitations
- [WebSocket](docs/websocket.md) - Real-time logs, stats, exec streaming
- [File Manager](docs/file-manager.md) - File operations in containers
- [Installation Scripts](docs/install-scripts.md) - Running scripts during container creation