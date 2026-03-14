# wa — WhatsApp CLI & API Tool

A command-line tool and REST API server for WhatsApp. Send and receive messages, manage groups, handle media, and more — all through a single Go binary.

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

Requires Go 1.21+.

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

## REST API

Start the server:

```bash
wa serve [--port 8080] [--host localhost] [--api-key KEY]
```

All endpoints require `Authorization: Bearer <api-key>` (except health).

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/health` | Health check (no auth) |
| `POST` | `/api/v1/auth/login` | QR code login |
| `POST` | `/api/v1/auth/logout` | Logout |
| `GET` | `/api/v1/auth/status` | Connection status |
| `POST` | `/api/v1/messages/send` | Send message (JSON or multipart) |
| `GET` | `/api/v1/messages?jid=...` | List messages (cursor pagination) |
| `GET` | `/api/v1/messages/:id` | Get message |
| `DELETE` | `/api/v1/messages/:id` | Delete message |
| `POST` | `/api/v1/messages/:id/react` | React to message |
| `POST` | `/api/v1/messages/:id/read` | Mark as read |
| `POST` | `/api/v1/groups` | Create group |
| `GET` | `/api/v1/groups` | List groups |
| `GET` | `/api/v1/groups/:jid` | Group info |
| `POST` | `/api/v1/groups/:jid/leave` | Leave group |
| `GET` | `/api/v1/groups/:jid/invite-link` | Get invite link |
| `POST` | `/api/v1/groups/join` | Join via link |
| `POST` | `/api/v1/groups/:jid/participants/add` | Add members |
| `POST` | `/api/v1/groups/:jid/participants/remove` | Remove members |
| `POST` | `/api/v1/groups/:jid/participants/promote` | Promote to admin |
| `POST` | `/api/v1/groups/:jid/participants/demote` | Demote from admin |
| `GET` | `/api/v1/contacts` | List contacts |
| `GET` | `/api/v1/contacts/:jid` | Contact info |
| `POST` | `/api/v1/contacts/:jid/block` | Block |
| `POST` | `/api/v1/contacts/:jid/unblock` | Unblock |
| `POST` | `/api/v1/media/upload` | Upload media (multipart) |
| `GET` | `/api/v1/media/:message-id` | Download media |
| `POST` | `/api/v1/webhooks` | Register webhook |
| `GET` | `/api/v1/webhooks` | List webhooks |
| `DELETE` | `/api/v1/webhooks/:id` | Delete webhook |
| `GET` | `/api/v1/events?after=0&limit=50` | Poll events (cursor-based) |

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

Config file: `~/.config/wa/config.yaml` (created automatically on first run).

```yaml
api_key: "wa_xxxxxxxxxxxxx"     # Auto-generated, used for API auth
server:
  host: "localhost"
  port: 8080
  max_upload_size: 104857600    # 100MB
database:
  path: "~/.config/wa/wa.db"
events:
  max_buffer: 10000             # Max events in polling buffer
webhooks: []                    # Pre-configured webhooks
```

## Architecture

```
┌──────────────────────────────────────────────┐
│                   wa binary                   │
├───────────────────┬──────────────────────────┤
│   CLI (cobra)     │   REST API (chi)          │
│   cmd/wa/*.go     │   internal/api/           │
├───────────────────┴──────────────────────────┤
│            internal/client/ (Service)         │
│          (core WhatsApp operations)           │
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

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Authentication error |
| 3 | Not found |

## License

Apache 2.0
