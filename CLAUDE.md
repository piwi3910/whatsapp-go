# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`wa` is a WhatsApp CLI & API tool — a single Go binary providing both CLI commands (via cobra) and a REST API server (via chi), built on the [whatsmeow](https://github.com/tulir/whatsmeow) library. Design spec: `docs/superpowers/specs/2026-03-14-wa-cli-api-design.md`.

**Status:** Pre-implementation (spec only). No Go code exists yet.

## Build & Development Commands

These commands should be used once the Go module is initialized:

```bash
go build -o wa ./cmd/wa/          # build binary
go test ./...                      # run all tests
go test ./internal/client/ -run TestSendText  # run single test
go vet ./...                       # static analysis
```

Prefer `modernc.org/sqlite` over `go-sqlite3` for CGo-free builds.

## Architecture

```
cmd/wa/           → CLI commands (cobra) — thin layer calling internal/client
internal/client/  → Core logic — wraps whatsmeow, used by both CLI and API
internal/api/     → REST API handlers (chi router, /api/v1/ prefix)
internal/store/   → SQLite storage (messages, events, webhooks, media_uploads)
internal/webhook/ → Webhook dispatcher (POST + HMAC-SHA256 + retries)
internal/config/  → YAML config (~/.config/wa/config.yaml)
internal/models/  → Shared types (Message, Group, Contact, Event, Webhook)
```

**Key pattern:** Both CLI and API are thin layers over `internal/client.Client`. All WhatsApp logic lives in the client package.

### Concurrency Model

whatsmeow doesn't support concurrent connections from the same device:
- If `wa serve` is running (detected via PID file at `~/.config/wa/wa.pid`): CLI auto-proxies through the local REST API
- If no server: CLI starts a temporary connection, executes, disconnects
- SQLite WAL mode + file lock prevents dual whatsmeow instances

### Message Identity

WhatsApp message IDs aren't globally unique. Local composite ID = first 16 chars of SHA256(`chatJID:senderJID:waMessageID`). The full tuple is stored for whatsmeow operations.

### Event System

Events flow: WhatsApp → whatsmeow → Client.eventHandler() → fan out to SQLite store, webhook dispatcher, and event ring buffer (10k entries, cursor-based polling).

## Key Dependencies

- `whatsmeow` — WhatsApp protocol
- `cobra` — CLI framework
- `chi` — HTTP router
- `modernc.org/sqlite` — CGo-free SQLite

## Git Conventions

- Author: `Pascal Watteel <pascal@watteel.com>`
- Do not add Co-Authored-By lines for AI
