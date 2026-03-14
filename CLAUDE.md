# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`wa` is a WhatsApp CLI & API tool — a single Go binary providing both CLI commands (via cobra) and a REST API server (via chi), built on the [whatsmeow](https://github.com/tulir/whatsmeow) library. Design spec: `docs/superpowers/specs/2026-03-14-wa-cli-api-design.md`.

## Build & Development Commands

```bash
go build -o wa ./cmd/wa/          # build binary
go test ./...                      # run all tests
go test ./internal/store/ -run TestInsert -v  # run single test
go vet ./...                       # static analysis
go mod tidy                        # clean up dependencies
```

CGo-free build using `modernc.org/sqlite`. No C compiler needed.

## Architecture

```
cmd/wa/           → CLI commands (cobra) + CLI-server proxy
internal/client/  → Service interface + whatsmeow wrapper (core logic)
internal/api/     → REST API handlers (chi router, /api/v1/ prefix)
internal/store/   → SQLite storage (messages, events, webhooks, media_uploads)
internal/webhook/ → Webhook dispatcher (POST + HMAC-SHA256 + retries)
internal/config/  → YAML config (~/.config/wa/config.yaml)
internal/models/  → Shared types (Message, Group, Contact, Event, Webhook)
internal/jid/     → JID normalization + composite message ID generation
internal/pidfile/ → PID file for server detection
```

**Key pattern:** Both CLI and API are thin layers over `internal/client.Service` interface. All WhatsApp logic lives in the client package. The proxy client (`cmd/wa/proxy.go`) implements the same interface by forwarding to the REST API.

### Database Layout

Two separate SQLite databases in `~/.config/wa/`:
- `wa.db` — app data (messages, events, webhooks, media_uploads)
- `whatsmeow.db` — whatsmeow device/session data (managed by whatsmeow)

Both use WAL mode. They are separate to avoid driver/dialect conflicts between our store (modernc.org/sqlite, driver name "sqlite") and whatsmeow's sqlstore (which expects dialect "sqlite3" internally).

### Concurrency Model

whatsmeow doesn't support concurrent connections from the same device:
- If `wa serve` is running (detected via PID file at `~/.config/wa/wa.pid`): CLI auto-proxies through the local REST API
- If no server: CLI starts a temporary connection, executes, disconnects

### Message Identity

WhatsApp message IDs aren't globally unique. Local composite ID = first 16 chars of SHA256(`chatJID:senderJID:waMessageID`). The full tuple is stored for whatsmeow operations.

### Event System

Events flow: WhatsApp → whatsmeow → Client.eventHandler() → fan out to SQLite store, webhook dispatcher, and registered handlers. Event ring buffer capped at 10k entries with cursor-based polling.

## Key Dependencies

- `go.mau.fi/whatsmeow` — WhatsApp multi-device protocol
- `github.com/spf13/cobra` — CLI framework
- `github.com/go-chi/chi/v5` — HTTP router
- `modernc.org/sqlite` — CGo-free SQLite driver
- `gopkg.in/yaml.v3` — config file parsing
- `github.com/mdp/qrterminal/v3` — terminal QR code rendering

## Git Conventions

- Author: `Pascal Watteel <pascal@watteel.com>`
- Do not add Co-Authored-By lines for AI
