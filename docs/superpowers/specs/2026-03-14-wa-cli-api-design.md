# wa — WhatsApp CLI & API Tool Design Spec

## Overview

`wa` is a command-line tool and REST API server for WhatsApp, similar to GitHub's `gh` CLI. It enables AI agents and developers to send/receive messages, manage groups, handle media, and more — all through a single Go binary built on the [whatsmeow](https://github.com/tulir/whatsmeow) library.

**License:** Apache 2.0

## Key Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Tool name | `wa` | Short, memorable, ergonomic |
| Language | Go | Repo language, matches whatsmeow |
| WhatsApp library | whatsmeow | Most mature Go library, multi-device support |
| Architecture | Single binary, dual mode | CLI commands + `wa serve` for REST API |
| Auth (WhatsApp) | QR-based device linking | whatsmeow's native approach |
| Auth (API) | API key (Bearer token) | Simple, sufficient for self-hosted use |
| Storage | SQLite | Single file, no dependencies, whatsmeow compatible |
| HTTP router | chi | Lightweight, stdlib-compatible |
| Events | Webhooks + polling | Real-time push + simple fallback |
| Config location | `~/.config/wa/` | XDG-friendly |

## Project Structure

```
wa/
├── cmd/wa/
│   ├── main.go              # entrypoint
│   ├── root.go              # cobra root command, global flags
│   ├── auth.go              # login, logout, status
│   ├── message.go           # send, list, info, delete
│   ├── group.go             # create, list, info, join, leave, invite, participants
│   ├── contact.go           # list, info, block, unblock
│   ├── media.go             # download
│   ├── event.go             # listen (stream events to stdout)
│   └── serve.go             # start REST API server
├── internal/
│   ├── client/              # whatsmeow wrapper — core logic
│   │   ├── client.go        # Client struct, connect/disconnect
│   │   ├── auth.go          # QR login, logout
│   │   ├── message.go       # send, delete, react, mark read
│   │   ├── group.go         # group operations
│   │   ├── contact.go       # contact operations
│   │   ├── media.go         # upload/download media
│   │   └── events.go        # event handler registration
│   ├── api/                 # REST API handlers
│   │   ├── server.go        # chi router setup, middleware
│   │   ├── auth.go          # auth endpoints
│   │   ├── message.go       # message endpoints
│   │   ├── group.go         # group endpoints
│   │   ├── contact.go       # contact endpoints
│   │   ├── media.go         # media endpoints
│   │   ├── webhook.go       # webhook CRUD endpoints
│   │   ├── event.go         # polling endpoint
│   │   └── middleware.go    # API key auth, logging, error handling
│   ├── store/               # SQLite storage layer
│   │   ├── store.go         # DB init, migrations
│   │   ├── messages.go      # message CRUD
│   │   ├── events.go        # event ring buffer
│   │   └── webhooks.go      # webhook CRUD
│   ├── webhook/             # webhook dispatcher
│   │   └── dispatcher.go    # POST to URLs, retries, HMAC signing
│   ├── config/              # configuration management
│   │   └── config.go        # load/save YAML config
│   └── models/              # shared types
│       ├── message.go       # Message, MessagePayload
│       ├── group.go         # Group, Participant
│       ├── contact.go       # Contact
│       ├── event.go         # Event, EventType
│       └── webhook.go       # Webhook
├── go.mod
├── go.sum
└── LICENSE
```

## CLI Command Tree

```
wa login                                    # show QR code in terminal, link device
wa logout                                   # unlink device
wa auth status                               # show connection/auth status

wa send text <jid> <message>                # send text message
wa send image <jid> <file> [-c caption]     # send image
wa send video <jid> <file> [-c caption]     # send video
wa send audio <jid> <file>                  # send audio
wa send document <jid> <file> [-f filename] # send document
wa send sticker <jid> <file>                # send sticker
wa send location <jid> <lat> <lon> [-n name]# send location
wa send contact <jid> <contact-jid>         # send contact card
wa send reaction <jid> <message-id> <emoji> # react to message (sender looked up from local DB)

wa message list <jid> [--limit N] [--before timestamp]
wa message info <message-id>
wa message delete <jid> <message-id> [--for-everyone]

wa group create <name> <jid>...             # create group with participants
wa group list                               # list all groups
wa group info <group-jid>
wa group join <invite-link>
wa group leave <group-jid>
wa group invite <group-jid>                 # get invite link
wa group add <group-jid> <jid>...           # add participants
wa group remove <group-jid> <jid>...        # remove participants
wa group promote <group-jid> <jid>...       # make admin
wa group demote <group-jid> <jid>...        # remove admin

wa contact list
wa contact info <jid>
wa contact block <jid>
wa contact unblock <jid>

wa media download <message-id> [-o output-path]

wa event listen [--types message.received,group.created]  # stream NDJSON to stdout

wa serve [--port 8080] [--host localhost] [--api-key KEY]
```

### Global Flags

- `--output json` — machine-readable JSON output (default: human-friendly text/table)
- `--config <path>` — override config file location
- `--db <path>` — override database file location

### Stdin Support

`wa send text <jid> -` reads the message body from stdin, useful for long messages or piping from other tools.

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Authentication error |
| 3 | Not found |

### Phone Number Normalization

CLI accepts flexible input:
- `+1234567890` → `1234567890@s.whatsapp.net`
- `1234567890` → `1234567890@s.whatsapp.net`
- `1234567890@s.whatsapp.net` → passed through
- Group JIDs: `groupid@g.us`

## REST API Design

All endpoints prefixed with `/api/v1/`. All require `Authorization: Bearer <api-key>`.

### Response Format

Success:
```json
{
  "ok": true,
  "data": { ... }
}
```

Error:
```json
{
  "ok": false,
  "error": {
    "code": "NOT_FOUND",
    "message": "Message not found"
  }
}
```

### Health Endpoint

```
GET    /api/v1/health            → {state, uptime_seconds, version}
```

No authentication required. Useful for monitoring and AI agent health checks.

### Authentication Endpoints

```
POST   /api/v1/auth/login       → {qr_code_base64, qr_code_text, timeout}
POST   /api/v1/auth/logout      → {ok}
GET    /api/v1/auth/status       → {state: "connecting"|"connected"|"disconnected"|"logged_out", phone_number, push_name}
```

### Message Endpoints

```
POST   /api/v1/messages/send    → {message_id, timestamp}
  Body (JSON): {to, type, content?, media_id?, caption?, lat?, lon?, ...}
  Body (multipart): to, type, file, caption? (inline upload+send)

GET    /api/v1/messages          → {messages: [...], cursor}
  Query: jid (required), limit?, before?

GET    /api/v1/messages/:id      → {message}
  Note: :id is the local DB primary key (composite of chat JID + sender + WhatsApp msg ID,
  stored as a deterministic hash). The local DB resolves to the full message key internally.

DELETE /api/v1/messages/:id      → {ok}
  Query: for_everyone? (default false)

POST   /api/v1/messages/:id/react → {ok}
  Body: {emoji}

POST   /api/v1/messages/:id/read  → {ok}
```

### Group Endpoints

```
POST   /api/v1/groups                              → {group}
  Body: {name, participants: [jid...]}

GET    /api/v1/groups                              → {groups: [...]}

GET    /api/v1/groups/:jid                         → {group}

POST   /api/v1/groups/:jid/leave                   → {ok}

GET    /api/v1/groups/:jid/invite-link              → {invite_link}

POST   /api/v1/groups/join                         → {group_jid}
  Body: {invite_link}

POST   /api/v1/groups/:jid/participants/add        → {ok}
  Body: {jids: [...]}

POST   /api/v1/groups/:jid/participants/remove     → {ok}
  Body: {jids: [...]}

POST   /api/v1/groups/:jid/participants/promote    → {ok}
  Body: {jids: [...]}

POST   /api/v1/groups/:jid/participants/demote     → {ok}
  Body: {jids: [...]}
```

### Contact Endpoints

```
GET    /api/v1/contacts                → {contacts: [...]}
GET    /api/v1/contacts/:jid           → {contact}
POST   /api/v1/contacts/:jid/block     → {ok}
POST   /api/v1/contacts/:jid/unblock   → {ok}
```

Note: `GET /contacts` returns contacts from whatsmeow's synced contact store (contacts the linked phone has synced). This may not include all phone contacts.

### Media Endpoints

```
POST   /api/v1/media/upload            → {media_id, type, mime, size}
  Body: multipart/form-data with file

GET    /api/v1/media/:message-id       → binary file download
```

### Webhook Endpoints

```
POST   /api/v1/webhooks                → {webhook}
  Body: {url, events: ["message.received", ...] | ["*"], secret?}

GET    /api/v1/webhooks                → {webhooks: [...]}

DELETE /api/v1/webhooks/:id            → {ok}
```

### Event Polling Endpoint

```
GET    /api/v1/events                  → {events: [...], cursor}
  Query: after (event ID cursor, default 0), limit? (default 50)
```

## Core Architecture

### Client Layer (`internal/client/`)

The `Client` struct wraps whatsmeow and provides the interface used by both CLI and API:

```go
type Client struct {
    wac    *whatsmeow.Client
    store  *store.Store
    log    waLog.Logger
}

// Key methods:
func (c *Client) Connect() error
func (c *Client) Disconnect()
func (c *Client) Login() (<-chan whatsmeow.QRChannelItem, error)
func (c *Client) Logout() error
func (c *Client) SendText(jid, text string) (string, error)
func (c *Client) SendImage(jid string, data []byte, caption string) (string, error)
func (c *Client) SendVideo(jid string, data []byte, caption string) (string, error)
func (c *Client) SendAudio(jid string, data []byte) (string, error)
func (c *Client) SendDocument(jid string, data []byte, filename string) (string, error)
func (c *Client) SendSticker(jid string, data []byte) (string, error)
func (c *Client) SendLocation(jid string, lat, lon float64, name string) (string, error)
func (c *Client) SendContact(jid, contactJID string) (string, error)
func (c *Client) SendReaction(jid, senderJID, messageID, emoji string) error
func (c *Client) DeleteMessage(jid, senderJID, messageID string, forEveryone bool) error
func (c *Client) MarkRead(jid, senderJID, messageID string) error
func (c *Client) GetMessages(jid string, limit int, before int64) ([]models.Message, error)
func (c *Client) CreateGroup(name string, participants []string) (string, error)
func (c *Client) GetGroups() ([]models.Group, error)
func (c *Client) GetGroupInfo(jid string) (*models.Group, error)
// ... etc
func (c *Client) RegisterEventHandler(handler func(models.Event))
```

### Data Flow

```
┌──────────────────────────────────────────────┐
│                   wa binary                   │
├───────────────────┬──────────────────────────┤
│   CLI (cobra)     │   REST API (chi)          │
│   cmd/wa/*.go     │   internal/api/           │
├───────────────────┴──────────────────────────┤
│               internal/client/                │
│          (core WhatsApp operations)           │
├──────────────────────────────────────────────┤
│               internal/store/                 │
│      (SQLite: sessions + messages + events)   │
├──────────────────────────────────────────────┤
│               whatsmeow library               │
│       (protocol, encryption, transport)       │
└──────────────────────────────────────────────┘
```

### Event Flow (Server Mode)

```
WhatsApp ──→ whatsmeow ──→ Client.eventHandler()
                                 │
                      ┌──────────┼──────────┐
                      ▼          ▼          ▼
                   Store      Webhook    Event
                 (SQLite)   Dispatcher  Buffer
                               │       (polling)
                               ▼
                       POST to registered
                        webhook URLs
```

## Message Identity

WhatsApp message IDs are not globally unique — a message is identified by the tuple `(chat JID, sender JID, message ID)`. To provide a simple single-key API, we generate a local composite ID:

- **Local ID** = first 16 chars of SHA256(`chatJID + ":" + senderJID + ":" + waMessageID`)
- This is the `id` column in the `messages` table and what's used in all API/CLI operations
- The full tuple (chat JID, sender JID, WhatsApp message ID) is stored in the `messages` table for whatsmeow operations
- CLI commands like `wa message delete <jid> <message-id>` take the local ID; the JID argument provides context for display but the local ID is sufficient for lookup

## CLI vs Server Concurrency

whatsmeow does not support concurrent connections from the same device. The concurrency model:

- **If `wa serve` is running:** CLI commands detect a running server (via PID file at `~/.config/wa/wa.pid`) and proxy through the local REST API automatically. The CLI becomes a thin HTTP client.
- **If no server is running:** CLI commands start a temporary whatsmeow connection, execute the command, and disconnect. This is slower but works for one-off operations.
- **`wa event listen`:** Connects to a running server's SSE/polling endpoint. If no server is running, it starts a persistent whatsmeow connection (like a lightweight server without the HTTP API) and disconnects on Ctrl+C.
- **Lock file:** SQLite database uses WAL mode. A file lock prevents two whatsmeow instances from connecting simultaneously — the second caller gets an error telling them to use `wa serve`.

## Message Storage & Ingestion

Messages are persisted to SQLite in the following scenarios:

- **Server mode (`wa serve`):** The event handler receives all `events.Message` from whatsmeow and writes them to the `messages` table. This is the primary message ingestion path.
- **CLI mode (no server):** Sent messages are stored after successful send. Received messages are NOT captured (no persistent listener). `wa message list` returns only locally-stored messages and may be empty if the server hasn't been run.
- **Implication:** For full message history, `wa serve` should be running. This is by design — the server is the persistent component.

## Storage

### SQLite Schema

whatsmeow manages its own tables for device/session state. We add:

```sql
CREATE TABLE messages (
    id          TEXT PRIMARY KEY,     -- local composite ID (see Message Identity section)
    chat_jid    TEXT NOT NULL,        -- chat/conversation JID
    sender_jid  TEXT NOT NULL,        -- sender JID (needed for reactions, deletions)
    wa_id       TEXT NOT NULL,        -- original WhatsApp message ID
    type        TEXT NOT NULL,        -- text, image, video, audio, document, sticker, location, contact
    content     TEXT,                 -- text content or JSON metadata
    media_type  TEXT,                 -- MIME type
    media_size  INTEGER,
    media_url   TEXT,                 -- encrypted media URL
    media_key   BLOB,                -- decryption key
    caption     TEXT,
    timestamp   INTEGER NOT NULL,
    is_from_me  BOOLEAN NOT NULL DEFAULT 0,
    is_read     BOOLEAN NOT NULL DEFAULT 0,
    raw_proto   BLOB,                -- original protobuf for lossless storage
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_messages_chat_ts ON messages(chat_jid, timestamp DESC);
CREATE UNIQUE INDEX idx_messages_wa_key ON messages(chat_jid, sender_jid, wa_id);

CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    type        TEXT NOT NULL,
    payload     TEXT NOT NULL,        -- JSON
    timestamp   INTEGER NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX idx_events_id ON events(id);

CREATE TABLE media_uploads (
    id          TEXT PRIMARY KEY,     -- media_id returned to caller
    data        BLOB NOT NULL,        -- raw file bytes
    mime_type   TEXT NOT NULL,
    filename    TEXT,
    size        INTEGER NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    expires_at  INTEGER NOT NULL      -- auto-pruned after 1 hour
);

CREATE TABLE webhooks (
    id          TEXT PRIMARY KEY,
    url         TEXT NOT NULL,
    events      TEXT NOT NULL,        -- JSON array of event types
    secret      TEXT,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
```

Event table is pruned to keep at most 10,000 rows (configurable).

## Media Handling

### Supported Types

| Type | Extensions | WhatsApp Proto |
|------|-----------|----------------|
| image | jpg, png, gif, webp | ImageMessage |
| video | mp4, 3gp, mov | VideoMessage |
| audio | mp3, ogg, m4a, wav | AudioMessage |
| document | any | DocumentMessage |
| sticker | webp | StickerMessage |

### Send Flow

1. Read file → detect MIME type
2. Upload to WhatsApp CDN via whatsmeow `Upload()`
3. Construct appropriate protobuf message
4. Send via whatsmeow

### Receive Flow

1. Store message metadata (type, MIME, size, media key, URL) in SQLite
2. Media is NOT auto-downloaded
3. Downloaded on demand: `wa media download <msg-id>` or `GET /api/v1/media/:msg-id`
4. whatsmeow handles decryption

### API Upload

Two approaches supported:

**Two-step (useful for AI agents):**
```
POST /api/v1/media/upload → {media_id}
POST /api/v1/messages/send → {to, type: "image", media_id, caption?}
```

The uploaded file is stored temporarily in the `media_uploads` SQLite table with a 1-hour TTL. When `POST /messages/send` references a `media_id`, the server retrieves the file data, uploads to WhatsApp's CDN via whatsmeow `Upload()`, sends the message, and deletes the temporary record. A background goroutine prunes expired uploads every 10 minutes.

**Inline (single request):**
```
POST /api/v1/messages/send (multipart/form-data)
  to, type, file, caption?
```

### MIME Type Detection

Primary: file extension mapping. Fallback: `http.DetectContentType` (reads first 512 bytes). Extension is authoritative when present since `DetectContentType` is unreliable for some audio/video formats.

## Webhook System

### Registration

Via API or config file. Each webhook has:
- **url** — POST target
- **events** — array of subscribed event types, or `["*"]` for all
- **secret** — optional, used for HMAC-SHA256 signature. Stored in plaintext in SQLite (required for signing outgoing payloads; this is a self-hosted tool)

### Delivery

- POST JSON: `{"event": "message.received", "timestamp": ..., "data": {...}}`
- Signature header: `X-Wa-Signature: sha256=<hmac>`
- 3 retries with exponential backoff: 1s, 5s, 15s
- Timeout: 10s per attempt
- Failed deliveries are logged, not queued indefinitely

### Event Types

```
message.received       message.sent          message.deleted
message.reaction       message.read
group.created          group.updated
group.participant_added    group.participant_removed
group.participant_promoted group.participant_demoted
contact.updated        presence.updated
connection.logged_out  connection.connected   connection.disconnected
```

## Event Polling

Ring buffer in SQLite (`events` table), default 10,000 entries.

```
GET /api/v1/events?after=0&limit=50
→ {events: [...], cursor: "152"}
```

Cursor-based pagination using the monotonically increasing event `id` (not timestamp, which can have duplicates). Use returned `cursor` as next `after` value. First call uses `after=0` to get all available events.

## Configuration

Location: `~/.config/wa/`

```yaml
# config.yaml
api_key: "wa_xxxxxxxxxxxxx"     # auto-generated on first `wa serve` if not set
server:
  host: "localhost"
  port: 8080
database:
  path: "~/.config/wa/wa.db"
events:
  max_buffer: 10000              # max events in polling buffer
webhooks: []                     # can pre-configure webhooks here
```

## Connection Management

- whatsmeow handles automatic reconnection on network drops
- Connection states: `connecting`, `connected`, `disconnected`, `logged_out`
- If device is unlinked from phone → `logged_out` state → `connection.logged_out` webhook event
- No client-side rate limiting — WhatsApp enforces its own limits, errors surfaced directly
- Max request body size: 100MB (for media uploads). Configurable via `server.max_upload_size` in config

## Server Lifecycle

```
wa serve
  1. Load config from ~/.config/wa/config.yaml
  2. Open SQLite database
  3. Connect to WhatsApp (if previously logged in)
  4. Start HTTP server on configured host:port
  5. Register event handlers → store + webhooks + event buffer
  6. Graceful shutdown on SIGINT/SIGTERM
```

## Dependencies

- **whatsmeow** — WhatsApp multi-device protocol
- **cobra** — CLI framework
- **chi** — HTTP router
- **go-sqlite3** or **modernc.org/sqlite** — SQLite driver (prefer modernc for CGo-free builds)
- **tablewriter** or similar — human-friendly CLI output
