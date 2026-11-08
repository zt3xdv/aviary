# WebSocket

Connect to `ws://<agent-host>:8080/ws?token=<TOKEN>` for real-time operations. Use `wss://` if TLS enabled.

All messages are JSON. The connection stays open until closed by either side.

## Message Types

### Logs

Request:
```json
{
  "type": "logs",
  "container_id": "abc123",
  "follow": true,
  "timestamps": true,
  "tail": "100",
  "since": "2024-01-15T10:00:00Z",
  "until": "2024-01-15T11:00:00Z"
}
```

Fields:
- `container_id` - required
- `follow` - stream new logs (default: true)
- `timestamps` - include timestamps (default: true)
- `tail` - lines from end (default: "100")
- `since` - RFC3339 timestamp
- `until` - RFC3339 timestamp

Response: binary frames with raw log bytes (stdout/stderr multiplexed). Each frame is a log line with Docker's 8-byte header (stream type, size).

### Stats

Request:
```json
{
  "type": "stats",
  "container_id": "abc123",
  "stream": true
}
```

Fields:
- `container_id` - required
- `stream` - continuous stream (default: true)

Response: JSON objects with Docker stats:
```json
{
  "type": "stats",
  "data": {
    "cpu_stats": {...},
    "memory_stats": {...},
    "networks": {...},
    "pids_stats": {...}
  }
}
```

Sent every second when streaming.

### Exec (Real-Time Terminal)

Request:
```json
{
  "type": "exec",
  "container_id": "abc123",
  "cmd": ["/bin/sh"],
  "attach_stdin": true,
  "attach_stdout": true,
  "attach_stderr": true,
  "tty": true,
  "user": "root",
  "working_dir": "/",
  "env": ["TERM=xterm"]
}
```

Fields:
- `container_id` - required
- `cmd` - command array (required)
- `attach_stdin` - attach stdin (default: true)
- `attach_stdout` - attach stdout (default: true)
- `attach_stderr` - attach stderr (default: true)
- `tty` - allocate pseudo-TTY (default: true)
- `user` - user to run as (default: container user)
- `working_dir` - working directory (default: container working dir)
- `env` - environment variables

Response:
```json
{
  "type": "exec",
  "exec_id": "exec-abc123",
  "success": true
}
```

After this response, the WebSocket becomes a bidirectional stream for the exec session.

#### Streaming Behavior

- **Stdout/stderr**: Streamed as binary frames in real time. No buffering. No waiting for newlines. Each frame is raw bytes from the container.
- **Stdin**: Send via `stdin` message type or raw binary data after exec is established.
- **Connection lifecycle**: Stays open until the exec process exits or the WebSocket closes.

#### Stdin Message

```json
{
  "type": "stdin",
  "exec_id": "exec-abc123",
  "data": "ls -la\n"
}
```

Or send raw binary data directly on the WebSocket connection.

#### Resize

```json
{
  "type": "resize",
  "exec_id": "exec-abc123",
  "height": 40,
  "width": 120
}
```

Updates terminal size. Required for interactive apps (vim, top, etc).

## Error Responses

```json
{
  "type": "error",
  "error": "description"
}
```

Sent when a request fails. Connection stays open.

## Example Usage

### Python (websockets library)

```python
import asyncio
import websockets
import json

async def run():
    uri = "ws://agent:8080/ws?token=YOUR_TOKEN"
    async with websockets.connect(uri) as ws:
        # Start exec
        await ws.send(json.dumps({
            "type": "exec",
            "container_id": "abc123",
            "cmd": ["/bin/sh"],
            "tty": True
        }))
        
        # Wait for exec response
        resp = json.loads(await ws.recv())
        exec_id = resp["exec_id"]
        
        # Send command
        await ws.send(json.dumps({
            "type": "stdin",
            "exec_id": exec_id,
            "data": "echo hello\n"
        }))
        
        # Read output
        while True:
            msg = await ws.recv()
            if isinstance(msg, bytes):
                print(msg.decode(), end="")

asyncio.run(run())
```

### JavaScript (browser)

```javascript
const ws = new WebSocket("ws://agent:8080/ws?token=YOUR_TOKEN");

ws.binaryType = "arraybuffer";

ws.onopen = () => {
    ws.send(JSON.stringify({
        type: "exec",
        container_id: "abc123",
        cmd: ["/bin/sh"],
        tty: true
    }));
};

ws.onmessage = (event) => {
    if (event.data instanceof ArrayBuffer) {
        // Binary output from container
        const text = new TextDecoder().decode(event.data);
        console.log(text);
    } else {
        // JSON response
        const msg = JSON.parse(event.data);
        if (msg.exec_id) {
            window.execId = msg.exec_id;
        }
    }
};

function sendCommand(cmd) {
    ws.send(JSON.stringify({
        type: "stdin",
        exec_id: window.execId,
        data: cmd + "\n"
    }));
}
```

### wscat (command line)

```bash
# Logs
wscat -c "ws://agent:8080/ws?token=TOKEN" \
  -x '{"type":"logs","container_id":"abc123","follow":true,"tail":"50"}'

# Exec
wscat -c "ws://agent:8080/ws?token=TOKEN" \
  -x '{"type":"exec","container_id":"abc123","cmd":["/bin/sh"],"tty":true}'
# Then type commands, resize with:
# {"type":"resize","exec_id":"exec-...","height":40,"width":120}
```

## Notes

- One WebSocket connection handles one operation at a time. For multiple exec sessions, open multiple connections.
- Binary frames for logs and exec output. JSON frames for requests, responses, stats.
- Ping/pong handled automatically by gorilla/websocket (30s interval).
- Max message size: 32MB (configured in upgrader).