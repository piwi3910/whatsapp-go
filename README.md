# whatsapp-go — WhatsApp library, CLI & API

A **Go library** for WhatsApp, plus a command-line tool and REST API server built on it. Send and receive messages, manage groups, handle media — embed it in your own program, or run the `wa` binary.

Built on [whatsmeow](https://github.com/tulir/whatsmeow) (WhatsApp multi-device protocol). No CGo required.

## Quick Start

```bash
# Build
go build -o wa ./cmd/wa/

# Link your WhatsApp account
./wa login

# Send a message
./wa send text +1234567890 "Hello from wa!"

# Start the REST API server
./wa serve
```

## Installation

Requires Go 1.25+.

```bash
git clone https://github.com/piwi3910/whatsapp-go.git
cd whatsapp-go
go build -o wa ./cmd/wa/
```

The binary is self-contained — no external dependencies, no C compiler needed.

## CLI Usage

### Authentication

```bash
wa login                    # Scan QR code to link WhatsApp device
wa logout                   # Unlink device
wa auth status              # Show connection state and phone number
```

### Sending Messages

```bash
wa send text <jid> <message>                # Text message
wa send text <jid> -                        # Read message from stdin
wa send image <jid> <file> [-c caption]     # Image with optional caption
wa send video <jid> <file> [-c caption]     # Video
wa send audio <jid> <file>                  # Audio
wa send document <jid> <file>               # Document
wa send sticker <jid> <file>                # Sticker (WebP)
wa send location <jid> <lat> <lon> [-n name]# Location pin
wa send contact <jid> <contact-jid>         # Contact card
wa send reaction <message-id> <emoji>       # React to a message
```

**JID formats:** `+1234567890`, `1234567890`, `1234567890@s.whatsapp.net`, or `groupid@g.us`.

### Messages

```bash
wa message list <jid> [--limit 20] [--before timestamp]
wa message info <message-id>
wa message delete <jid> <message-id> [--for-everyone]
```

### Groups

```bash
wa group create <name> <jid>...     # Create group with participants
wa group list                       # List all groups
wa group info <group-jid>           # Group details + participants
wa group join <invite-link>         # Join via invite link
wa group leave <group-jid>
wa group invite <group-jid>         # Get invite link
wa group add <group-jid> <jid>...   # Add participants
wa group remove <group-jid> <jid>...
wa group promote <group-jid> <jid>...  # Make admin
wa group demote <group-jid> <jid>...   # Remove admin
```

### Contacts

```bash
wa contact list
wa contact info <jid>
wa contact block <jid>
wa contact unblock <jid>
```

### Media

```bash
wa media download <message-id> [-o output-path]
```

### Events

```bash
wa event listen [--types message.received,group.created]  # Stream NDJSON
```

### Global Flags

```
--output json    Machine-readable JSON output (default: human-friendly)
--config <path>  Override config file location
--db <path>      Override database file location
```

## Use as a Go library

```bash
go get github.com/piwi3910/whatsapp-go
```

```go
import "github.com/piwi3910/whatsapp-go/whatsapp"

c, err := whatsapp.Open(whatsapp.Options{StateDir: "/var/lib/wa"})
if err != nil {
    return err
}
defer c.Close()

// First run only: pair by scanning the QR code.
if !c.IsLoggedIn() {
    qr, err := c.Login(ctx)
    if err != nil {
        return err
    }
    for evt := range qr {
        if evt.Done {
            break
        }
        fmt.Println("scan this:", evt.Code)
    }
}

c.OnEvent(func(e whatsapp.Event) {
    if e.Type == whatsapp.EventMessageReceived {
        log.Println("inbound:", e.Payload)
    }
})

if err := c.Connect(ctx); err != nil {
    return err
}
_, err = c.SendText(ctx, "+31612345678", "hello from Go")
```

Everything the CLI and REST server can do is available on the `*whatsapp.Client`, and the `whatsapp.Service` interface makes it easy to fake in your own tests. Every operation that performs I/O takes a `context.Context` first, so calls can be cancelled and deadlined.

Types you need (`Message`, `Event`, `Contact`, `Group`, `SendResponse`) are exported from the same package — one import, nothing internal leaks.

### Receiving messages

Two options:

|                  | `OnEvent`                                               | `Events`                                            |
| ---------------- | ------------------------------------------------------- | --------------------------------------------------- |
| delivery         | push, in-process                                        | pull, cursor-based                                  |
| survives restart | no                                                      | yes                                                 |
| use when         | you react live and can afford to miss events while down | you must not miss messages across your own downtime |

`Events(ctx, after, limit)` returns log entries with ID greater than `after`. Persist the last ID you handled and resume from it. **Delivery is at-least-once** — the same WhatsApp message can reappear after a reconnect or history sync, so deduplicate on the message ID in the payload.

### State and lifetime

A `Client` owns two SQLite databases under `Options.StateDir`: the whatsmeow device store (the pairing — deleting it forces a new QR scan) and the message/event store. Because that state is local files, **one process per linked account**: a `Client` is single-instance by nature and cannot be horizontally scaled.

Runnable examples: [`whatsapp/example_test.go`](whatsapp/example_test.go).

## REST API

Start the server:

```bash
wa serve [--port 8080] [--host localhost] [--api-key KEY]
```

All endpoints require `Authorization: Bearer <api-key>` (except health).

### Endpoints

| Method   | Path                                       | Description                                                                           |
| -------- | ------------------------------------------ | ------------------------------------------------------------------------------------- |
| `GET`    | `/api/v1/health`                           | Health check (no auth)                                                                |
| `POST`   | `/api/v1/auth/login`                       | QR code login (returns first QR; re-issued codes and the outcome are tracked)         |
| `GET`    | `/api/v1/auth/login`                       | Poll the in-progress pairing attempt (current QR code, attempt count, terminal state) |
| `POST`   | `/api/v1/auth/logout`                      | Logout                                                                                |
| `GET`    | `/api/v1/auth/status`                      | Connection status                                                                     |
| `POST`   | `/api/v1/messages/send`                    | Send message (JSON or multipart)                                                      |
| `GET`    | `/api/v1/messages?jid=...`                 | List messages (cursor pagination)                                                     |
| `GET`    | `/api/v1/messages/:id`                     | Get message                                                                           |
| `DELETE` | `/api/v1/messages/:id`                     | Delete message                                                                        |
| `POST`   | `/api/v1/messages/:id/react`               | React to message                                                                      |
| `POST`   | `/api/v1/messages/:id/read`                | Mark as read                                                                          |
| `POST`   | `/api/v1/groups`                           | Create group                                                                          |
| `GET`    | `/api/v1/groups`                           | List groups                                                                           |
| `GET`    | `/api/v1/groups/:jid`                      | Group info                                                                            |
| `POST`   | `/api/v1/groups/:jid/leave`                | Leave group                                                                           |
| `GET`    | `/api/v1/groups/:jid/invite-link`          | Get invite link                                                                       |
| `POST`   | `/api/v1/groups/join`                      | Join via link                                                                         |
| `POST`   | `/api/v1/groups/:jid/participants/add`     | Add members                                                                           |
| `POST`   | `/api/v1/groups/:jid/participants/remove`  | Remove members                                                                        |
| `POST`   | `/api/v1/groups/:jid/participants/promote` | Promote to admin                                                                      |
| `POST`   | `/api/v1/groups/:jid/participants/demote`  | Demote from admin                                                                     |
| `GET`    | `/api/v1/contacts`                         | List contacts                                                                         |
| `GET`    | `/api/v1/contacts/:jid`                    | Contact info                                                                          |
| `POST`   | `/api/v1/contacts/:jid/block`              | Block                                                                                 |
| `POST`   | `/api/v1/contacts/:jid/unblock`            | Unblock                                                                               |
| `POST`   | `/api/v1/media/upload`                     | Upload media (multipart)                                                              |
| `GET`    | `/api/v1/media/:message-id`                | Download media                                                                        |
| `POST`   | `/api/v1/webhooks`                         | Register webhook                                                                      |
| `GET`    | `/api/v1/webhooks`                         | List webhooks                                                                         |
| `DELETE` | `/api/v1/webhooks/:id`                     | Delete webhook                                                                        |
| `GET`    | `/api/v1/events?after=0&limit=50`          | Poll events (cursor-based)                                                            |

### Consuming events reliably

`GET /api/v1/events?after=<cursor>&limit=<n>` is the restart-safe way to
receive messages. Persist the last event ID you processed and pass it as
`after`.

- **`payload` is a nested JSON object**, not a string — read
  `event.payload.id` directly. (This matches webhook deliveries, which
  always embedded it as JSON.)
- **Delivery is at-least-once.** WhatsApp redelivers after a reconnect or
  history sync. Events carry a dedupe key derived from the message
  identity, which suppresses the common case, but consumers should still
  key on `payload.id`.
- **`410 Gone` means you fell behind retention** — the log was pruned past
  your cursor. The body carries a safe cursor to resume from; reconcile the
  gap from `GET /api/v1/messages` (the message table is not pruned), then
  resume. Silently skipping ahead is impossible by design.
- **A malformed cursor is a `400`, not a reset.** Bad input never silently
  replays the whole buffer.
- An empty page echoes your cursor back, so storing the returned cursor is
  always safe. `limit` is clamped to 500.

### Response Format

```json
// Success
{"ok": true, "data": { ... }}

// Error
{"ok": false, "error": {"code": "NOT_FOUND", "message": "Message not found"}}
```

### Sending Messages via API

**JSON:**

```bash
curl -X POST http://localhost:8080/api/v1/messages/send \
  -H "Authorization: Bearer wa_xxx" \
  -H "Content-Type: application/json" \
  -d '{"to": "+1234567890", "type": "text", "content": "Hello!"}'
```

**Media (two-step):**

```bash
# 1. Upload
curl -X POST http://localhost:8080/api/v1/media/upload \
  -H "Authorization: Bearer wa_xxx" \
  -F "file=@photo.jpg"
# Returns: {"ok": true, "data": {"media_id": "med_xxx"}}

# 2. Send
curl -X POST http://localhost:8080/api/v1/messages/send \
  -H "Authorization: Bearer wa_xxx" \
  -H "Content-Type: application/json" \
  -d '{"to": "+1234567890", "type": "image", "media_id": "med_xxx", "caption": "Check this out"}'
```

**Media (inline):**

```bash
curl -X POST http://localhost:8080/api/v1/messages/send \
  -H "Authorization: Bearer wa_xxx" \
  -F "to=+1234567890" -F "type=image" -F "file=@photo.jpg" -F "caption=Hello"
```

### Webhooks

Register a webhook to receive real-time events:

```bash
curl -X POST http://localhost:8080/api/v1/webhooks \
  -H "Authorization: Bearer wa_xxx" \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com/webhook", "events": ["*"], "secret": "mysecret"}'
```

Webhook payload:

```json
{"event": "message.received", "timestamp": 1234567890, "data": { ... }}
```

Events are signed with HMAC-SHA256 via the `X-Wa-Signature: sha256=<hex>` header. Delivery retries: 3 attempts with exponential backoff (1s, 5s, 15s).

**Event types:** `message.received`, `message.sent`, `message.deleted`, `message.reaction`, `message.read`, `group.created`, `group.updated`, `group.participant_added`, `group.participant_removed`, `group.participant_promoted`, `group.participant_demoted`, `contact.updated`, `presence.updated`, `connection.connected`, `connection.disconnected`, `connection.logged_out`

## Configuration

Configuration is resolved **defaults → config file → environment**, with
environment variables winning. The server **never writes the config file on
its own**; pass `wa serve --write-config` if you want the current settings
(including a generated API key) persisted.

Config file: `~/.config/wa/config.yaml` (not created automatically).

```yaml
api_key: "wa_xxxxxxxxxxxxx" # Auto-generated, used for API auth
server:
  host: "localhost"
  port: 8080
  max_upload_size: 104857600 # 100MB
  tls_cert: "" # optional: with tls_key, the API serves HTTPS
  tls_key: ""
database:
  path: "~/.config/wa/wa.db"
events:
  max_buffer: 10000 # Max events in polling buffer
webhooks: [] # Pre-configured webhooks
allow_private_webhook_targets: false
```

### Environment variables

| Variable                                   | Overrides                                                                      |
| ------------------------------------------ | ------------------------------------------------------------------------------ |
| `WA_API_KEY`                               | `api_key`                                                                      |
| `WA_HOST`, `WA_PORT`                       | `server.host`, `server.port`                                                   |
| `WA_DB_PATH`                               | `database.path`                                                                |
| `WA_MAX_UPLOAD_SIZE`                       | `server.max_upload_size`                                                       |
| `WA_EVENTS_MAX_BUFFER`                     | `events.max_buffer`                                                            |
| `WA_ALLOW_PRIVATE_WEBHOOK_TARGETS`         | `allow_private_webhook_targets`                                                |
| `WA_RATE_LIMIT_RPS`, `WA_RATE_LIMIT_BURST` | API rate limit (`0` disables)                                                  |
| `WA_CONTAINER`                             | container mode: bind `0.0.0.0`, DB under `/data`, skip the PID file, JSON logs |
| `WA_LOG_FORMAT`, `WA_LOG_LEVEL`            | log output                                                                     |

A malformed value is a startup error rather than a silently ignored setting.

## Running in Kubernetes

`deploy/` holds a ready manifest set and `Dockerfile` builds a distroless,
non-root image. Three constraints are structural, not preferences:

- **One replica, `strategy: Recreate`.** The WhatsApp pairing and the
  message store are local SQLite files on a PVC; two pods would fight over
  them, and a rolling update would deadlock on the ReadWriteOnce volume.
- **`securityContext.fsGroup` must match the image's GID (65532).** A fresh
  PVC mounts root-owned, and the image runs non-root, so without it every
  write fails.
- **Losing the volume unlinks the device** and requires a new QR scan.

Probes: `/api/v1/healthz` for liveness (process alive), `/api/v1/readyz` for
readiness (200 only while actually connected to WhatsApp, 503 otherwise).
Metrics in Prometheus text format at `/metrics`.

### Webhook targets and SSRF

Webhook URLs are supplied by API callers and the server POSTS to them
unattended, so by default it refuses targets that resolve to **loopback or
private** addresses — otherwise anyone able to register a webhook could aim
the server at services on its own network. Addresses that reach
infrastructure rather than applications (**link-local**, including the
`169.254.169.254` cloud metadata endpoint, plus multicast and the
unspecified address) are refused **always**, whatever the setting.

Every target is checked twice: once at registration, for a clear error, and
again at the moment of the TCP dial, which is what actually closes the hole
— a hostname can pass registration and resolve somewhere else afterwards.
Redirects are never followed, since following one re-opens the hole a hop
later.

If your receiver is deliberately on a private network — an agent platform
in the same Kubernetes cluster, a service on your LAN — set
`allow_private_webhook_targets: true`. Only do this when you trust everyone
who can call the API.

## Architecture

```
┌──────────────────────────────────────────────┐
│         your program │      wa binary          │
├──────────────────────┼───────────┬────────────┤
│  imports whatsapp/   │ CLI(cobra)│ REST(chi)  │
│                      │ cmd/wa/   │ internal/  │
├──────────────────────┴───────────┴────────────┤
│         whatsapp/ (public Go library)         │
│     Client + Service — core operations        │
├──────────────────────────────────────────────┤
│             internal/store/ (SQLite)          │
│      messages + events + webhooks + media     │
├──────────────────────────────────────────────┤
│               whatsmeow library               │
│       (protocol, encryption, transport)       │
└──────────────────────────────────────────────┘
```

**CLI-Server Proxy:** When `wa serve` is running, CLI commands automatically detect it via PID file and forward through the REST API instead of creating a separate WhatsApp connection.

## CLI vs Server Mode

- **CLI mode:** Each command opens a temporary WhatsApp connection, executes, and disconnects. Sent messages are stored locally. Received messages are NOT captured.
- **Server mode (`wa serve`):** Persistent connection. All incoming messages, events, and state changes are captured, stored, and delivered via webhooks/polling. For full message history, run the server.

## Exit Codes

| Code | Meaning                                                      |
| ---- | ------------------------------------------------------------ |
| 0    | Success                                                      |
| 1    | General error                                                |
| 2    | Authentication error                                         |
| 3    | Not found                                                    |
| 4    | Resource in use (another `wa` process holds the device lock) |

## License

Apache 2.0
