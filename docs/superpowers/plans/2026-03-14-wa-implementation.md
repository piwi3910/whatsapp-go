# wa — WhatsApp CLI & API Tool Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single Go binary (`wa`) that provides both CLI commands and a REST API server for WhatsApp, wrapping the whatsmeow library.

**Architecture:** Bottom-up build: shared types/config → SQLite store → whatsmeow client wrapper → webhook dispatcher → REST API (chi) → CLI commands (cobra) → server lifecycle with CLI-server proxy. Each layer depends only on layers below it. The client package defines an interface so API and CLI layers can be tested with mocks.

**Tech Stack:** Go, whatsmeow (`go.mau.fi/whatsmeow`), cobra, chi, modernc.org/sqlite, gopkg.in/yaml.v3

**Spec:** `docs/superpowers/specs/2026-03-14-wa-cli-api-design.md`

---

## File Structure

```
cmd/wa/
  main.go              — entrypoint, wires config/store/client
  root.go              — cobra root command, global flags (--output, --config, --db)
  auth.go              — login, logout, auth status commands
  send.go              — wa send text/image/video/audio/document/sticker/location/contact/reaction
  message.go           — wa message list/info/delete
  group.go             — wa group create/list/info/join/leave/invite/add/remove/promote/demote
  contact.go           — wa contact list/info/block/unblock
  media.go             — wa media download
  event.go             — wa event listen
  serve.go             — wa serve (REST API server)

internal/
  models/
    message.go         — Message, SendRequest types
    group.go           — Group, Participant types
    contact.go         — Contact type
    event.go           — Event, EventType constants
    webhook.go         — Webhook type
    media_upload.go    — MediaUpload type
    response.go        — API response envelope (ok/error)

  config/
    config.go          — Config struct, Load/Save YAML, defaults

  jid/
    jid.go             — NormalizeJID, CompositeMessageID, phone number → JID conversion

  store/
    store.go           — DB init, RunMigrations, Close
    messages.go        — InsertMessage, GetMessage, GetMessages, DeleteMessage, UpdateReadStatus
    events.go          — InsertEvent, GetEvents, PruneEvents
    webhooks.go        — InsertWebhook, GetWebhooks, DeleteWebhook
    media.go           — InsertMediaUpload, GetMediaUpload, DeleteMediaUpload, PruneExpiredUploads

  client/
    interface.go       — Client interface (used by API/CLI for testability)
    client.go          — struct wrapping whatsmeow, Connect/Disconnect
    auth.go            — Login (QR), Logout, Status
    message.go         — SendText, SendImage, ..., DeleteMessage, React, MarkRead
    group.go           — CreateGroup, GetGroups, GetGroupInfo, JoinGroup, LeaveGroup, participants
    contact.go         — GetContacts, GetContactInfo, Block, Unblock
    media.go           — UploadMedia, DownloadMedia
    events.go          — event handler, whatsmeow event → models.Event mapping

  api/
    server.go          — chi router setup, Start/Stop, graceful shutdown
    middleware.go      — API key auth, request logging, error recovery, max body size
    response.go        — JSON response helpers (success, error)
    auth.go            — POST login, POST logout, GET status
    message.go         — POST send, GET list, GET by id, DELETE, POST react, POST read
    group.go           — POST create, GET list, GET info, POST leave, GET invite, POST join, participants
    contact.go         — GET list, GET info, POST block, POST unblock
    media.go           — POST upload, GET download
    webhook.go         — POST create, GET list, DELETE
    event.go           — GET events (polling)

  webhook/
    dispatcher.go      — Dispatcher struct, Register/Unregister, Dispatch with retries + HMAC

  pidfile/
    pidfile.go         — Write/Read/Remove PID file, IsRunning check
```

---

## Chunk 1: Foundation — Go Module, Models, Config, JID Helpers

### Task 1: Initialize Go Module

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Initialize Go module**

Run: `go mod init github.com/piwi3910/whatsapp-go`

- [ ] **Step 2: Add core dependencies**

Run:
```bash
go get go.mau.fi/whatsmeow@latest
go get github.com/spf13/cobra@latest
go get github.com/go-chi/chi/v5@latest
go get modernc.org/sqlite@latest
go get gopkg.in/yaml.v3@latest
```

Note: whatsmeow's sqlstore uses `database/sql` and supports any SQLite driver registered under the `"sqlite3"` name. We use `modernc.org/sqlite` for CGo-free builds — it registers as `"sqlite"`, so we use `go.mau.fi/whatsmeow/store/sqlstore` with driver name `"sqlite"`. No need for `go-sqlite3`.

- [ ] **Step 3: Verify module compiles**

Run: `go build ./...`
Expected: no errors (no Go files yet, but module resolves)

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "feat: initialize Go module with dependencies"
```

---

### Task 2: Shared Model Types

**Files:**
- Create: `internal/models/message.go`
- Create: `internal/models/group.go`
- Create: `internal/models/contact.go`
- Create: `internal/models/event.go`
- Create: `internal/models/webhook.go`
- Create: `internal/models/response.go`

- [ ] **Step 1: Create message types**

```go
// internal/models/message.go
package models

// Message represents a stored WhatsApp message.
type Message struct {
	ID        string `json:"id"`
	ChatJID   string `json:"chat_jid"`
	SenderJID string `json:"sender_jid"`
	WaID      string `json:"wa_id"`
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	MediaSize int64  `json:"media_size,omitempty"`
	MediaURL  string `json:"-"`
	MediaKey  []byte `json:"-"`
	Caption   string `json:"caption,omitempty"`
	Timestamp int64  `json:"timestamp"`
	IsFromMe  bool   `json:"is_from_me"`
	IsRead    bool   `json:"is_read"`
	RawProto  []byte `json:"-"`
	CreatedAt int64  `json:"created_at"`
}

// SendRequest represents a request to send a message via the API.
type SendRequest struct {
	To       string  `json:"to"`
	Type     string  `json:"type"`
	Content  string  `json:"content,omitempty"`
	MediaID  string  `json:"media_id,omitempty"`
	Caption  string  `json:"caption,omitempty"`
	Filename string  `json:"filename,omitempty"`
	Lat      float64 `json:"lat,omitempty"`
	Lon      float64 `json:"lon,omitempty"`
	Name     string  `json:"name,omitempty"`
	Emoji      string  `json:"emoji,omitempty"`
	ContactJID string  `json:"contact_jid,omitempty"`
}

// SendResponse is the result of sending a message.
type SendResponse struct {
	MessageID string `json:"message_id"`
	Timestamp int64  `json:"timestamp"`
}
```

- [ ] **Step 2: Create group types**

```go
// internal/models/group.go
package models

// Group represents a WhatsApp group.
type Group struct {
	JID          string        `json:"jid"`
	Name         string        `json:"name"`
	Topic        string        `json:"topic,omitempty"`
	Created      int64         `json:"created,omitempty"`
	Participants []Participant `json:"participants,omitempty"`
}

// Participant represents a group member.
type Participant struct {
	JID          string `json:"jid"`
	IsAdmin      bool   `json:"is_admin"`
	IsSuperAdmin bool   `json:"is_super_admin"`
}
```

- [ ] **Step 3: Create contact type**

```go
// internal/models/contact.go
package models

// Contact represents a WhatsApp contact.
type Contact struct {
	JID       string `json:"jid"`
	Name      string `json:"name,omitempty"`
	PushName  string `json:"push_name,omitempty"`
	Status    string `json:"status,omitempty"`
	PictureID string `json:"picture_id,omitempty"`
}
```

- [ ] **Step 4: Create event types**

```go
// internal/models/event.go
package models

// Event types matching the spec.
const (
	EventMessageReceived  = "message.received"
	EventMessageSent      = "message.sent"
	EventMessageDeleted   = "message.deleted"
	EventMessageReaction  = "message.reaction"
	EventMessageRead      = "message.read"
	EventGroupCreated     = "group.created"
	EventGroupUpdated     = "group.updated"
	EventGroupParticipantAdded   = "group.participant_added"
	EventGroupParticipantRemoved = "group.participant_removed"
	EventGroupParticipantPromoted = "group.participant_promoted"
	EventGroupParticipantDemoted  = "group.participant_demoted"
	EventContactUpdated   = "contact.updated"
	EventPresenceUpdated  = "presence.updated"
	EventConnectionLoggedOut    = "connection.logged_out"
	EventConnectionConnected    = "connection.connected"
	EventConnectionDisconnected = "connection.disconnected"
)

// Event represents a stored event for polling/webhooks.
type Event struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Payload   string `json:"payload"`
	Timestamp int64  `json:"timestamp"`
}
```

- [ ] **Step 5: Create webhook type**

```go
// internal/models/webhook.go
package models

// Webhook represents a registered webhook endpoint.
type Webhook struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	Secret    string   `json:"secret,omitempty"`
	CreatedAt int64    `json:"created_at"`
}
```

- [ ] **Step 6: Create media upload type**

```go
// internal/models/media_upload.go
package models

// MediaUpload represents a temporarily stored media file awaiting send.
type MediaUpload struct {
	ID        string `json:"id"`
	MimeType  string `json:"mime_type"`
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size"`
	Data      []byte `json:"-"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}
```

- [ ] **Step 7: Create API response envelope**

```go
// internal/models/response.go
package models

// APIResponse is the standard API response envelope.
type APIResponse struct {
	OK    bool     `json:"ok"`
	Data  any      `json:"data,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

// APIError represents an error in the API response.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
```

- [ ] **Step 8: Verify compilation**

Run: `go build ./internal/models/...`
Expected: compiles without errors

- [ ] **Step 9: Commit**

```bash
git add internal/models/
git commit -m "feat: add shared model types"
```

---

### Task 3: JID Normalization Helper

**Files:**
- Create: `internal/jid/jid.go`
- Create: `internal/jid/jid_test.go`

- [ ] **Step 1: Write failing tests for JID normalization**

```go
// internal/jid/jid_test.go
package jid

import (
	"testing"
)

func TestNormalizeJID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"phone with plus", "+1234567890", "1234567890@s.whatsapp.net", false},
		{"phone without plus", "1234567890", "1234567890@s.whatsapp.net", false},
		{"already full JID", "1234567890@s.whatsapp.net", "1234567890@s.whatsapp.net", false},
		{"group JID", "123456789@g.us", "123456789@g.us", false},
		{"empty input", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeJID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("NormalizeJID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("NormalizeJID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/jid/ -v`
Expected: FAIL — `NormalizeJID` not defined

- [ ] **Step 3: Implement NormalizeJID**

```go
// internal/jid/jid.go
package jid

import (
	"fmt"
	"strings"
)

// NormalizeJID converts flexible phone number input to a full WhatsApp JID.
// Accepts: "+1234567890", "1234567890", "1234567890@s.whatsapp.net", "groupid@g.us"
func NormalizeJID(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("empty JID input")
	}
	// Already a full JID
	if strings.Contains(input, "@") {
		return input, nil
	}
	// Strip leading +
	phone := strings.TrimPrefix(input, "+")
	return phone + "@s.whatsapp.net", nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/jid/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/jid/
git commit -m "feat: add JID normalization helper"
```

---

### Task 4: Configuration Management

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests for config**

```go
// internal/config/config_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Server.Host != "localhost" {
		t.Errorf("default host = %q, want %q", cfg.Server.Host, "localhost")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Events.MaxBuffer != 10000 {
		t.Errorf("default max_buffer = %d, want %d", cfg.Events.MaxBuffer, 10000)
	}
}

func TestLoadCreatesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", path, err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("loaded port = %d, want default 8080", cfg.Server.Port)
	}
	// File should have been created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("config file was not created")
	}
}

func TestLoadExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("api_key: \"wa_testkey123\"\nserver:\n  host: \"0.0.0.0\"\n  port: 9090\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if cfg.APIKey != "wa_testkey123" {
		t.Errorf("api_key = %q, want %q", cfg.APIKey, "wa_testkey123")
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("host = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("port = %d, want %d", cfg.Server.Port, 9090)
	}
}

func TestConfigDir(t *testing.T) {
	d := Dir()
	if d == "" {
		t.Error("Dir() returned empty string")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement config**

```go
// internal/config/config.go
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration.
type Config struct {
	APIKey   string         `yaml:"api_key"`
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Events   EventsConfig   `yaml:"events"`
	Webhooks []WebhookConfig `yaml:"webhooks"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	MaxUploadSize int64  `yaml:"max_upload_size"`
}

// DatabaseConfig holds database settings.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// EventsConfig holds event buffer settings.
type EventsConfig struct {
	MaxBuffer int `yaml:"max_buffer"`
}

// WebhookConfig represents a webhook defined in the config file.
type WebhookConfig struct {
	URL    string   `yaml:"url"`
	Events []string `yaml:"events"`
	Secret string   `yaml:"secret,omitempty"`
}

// Dir returns the default config directory (~/.config/wa/).
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wa"
	}
	return filepath.Join(home, ".config", "wa")
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		Server: ServerConfig{
			Host:          "localhost",
			Port:          8080,
			MaxUploadSize: 100 * 1024 * 1024, // 100MB
		},
		Database: DatabaseConfig{
			Path: filepath.Join(Dir(), "wa.db"),
		},
		Events: EventsConfig{
			MaxBuffer: 10000,
		},
	}
}

// Load reads config from path. If the file doesn't exist, creates it with defaults.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := Save(path, &cfg); err != nil {
			return nil, fmt.Errorf("creating default config: %w", err)
		}
		return &cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// Save writes config to path, creating parent directories as needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// GenerateAPIKey creates a random API key with the "wa_" prefix.
func GenerateAPIKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "wa_" + hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add YAML configuration management"
```

---

### Task 5: Message ID Generation Helper

**Files:**
- Modify: `internal/jid/jid.go`
- Modify: `internal/jid/jid_test.go`

- [ ] **Step 1: Write failing test for composite message ID**

Add to `internal/jid/jid_test.go`:

```go
func TestCompositeMessageID(t *testing.T) {
	id := CompositeMessageID("123@s.whatsapp.net", "456@s.whatsapp.net", "ABCDEF123")
	if len(id) != 16 {
		t.Errorf("CompositeMessageID length = %d, want 16", len(id))
	}

	// Deterministic — same inputs produce same output
	id2 := CompositeMessageID("123@s.whatsapp.net", "456@s.whatsapp.net", "ABCDEF123")
	if id != id2 {
		t.Error("CompositeMessageID is not deterministic")
	}

	// Different inputs produce different output
	id3 := CompositeMessageID("789@s.whatsapp.net", "456@s.whatsapp.net", "ABCDEF123")
	if id == id3 {
		t.Error("different inputs produced same ID")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/jid/ -run TestComposite -v`
Expected: FAIL

- [ ] **Step 3: Implement CompositeMessageID**

Add to `internal/jid/jid.go`:

```go
import (
	"crypto/sha256"
	"encoding/hex"
)

// CompositeMessageID generates a deterministic 16-char local ID from the
// WhatsApp message key tuple (chatJID, senderJID, waMessageID).
func CompositeMessageID(chatJID, senderJID, waMessageID string) string {
	h := sha256.Sum256([]byte(chatJID + ":" + senderJID + ":" + waMessageID))
	return hex.EncodeToString(h[:])[:16]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/jid/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/jid/
git commit -m "feat: add composite message ID generation"
```

---

### Task 6: PID File Helper

**Files:**
- Create: `internal/pidfile/pidfile.go`
- Create: `internal/pidfile/pidfile_test.go`

- [ ] **Step 1: Write failing tests for PID file operations**

```go
// internal/pidfile/pidfile_test.go
package pidfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	if err := Write(path); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	pid, err := Read(path)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("Read() = %d, want %d", pid, os.Getpid())
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.pid")
	_ = Write(path)
	Remove(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("PID file was not removed")
	}
}

func TestIsRunning_NoPidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")
	if IsRunning(path) {
		t.Error("IsRunning returned true for nonexistent file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pidfile/ -v`
Expected: FAIL

- [ ] **Step 3: Implement PID file operations**

```go
// internal/pidfile/pidfile.go
package pidfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Write creates a PID file at path containing the current process ID.
func Write(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// Read returns the PID stored in the file.
func Read(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// Remove deletes the PID file.
func Remove(path string) {
	os.Remove(path)
}

// IsRunning checks if a process with the PID in the file is still alive.
func IsRunning(path string) bool {
	pid, err := Read(path)
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists without actually sending a signal
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// ServerAddress reads the config to determine the running server's address.
// Returns empty string if no server is detected.
func ServerAddress(pidPath, host string, port int) string {
	if !IsRunning(pidPath) {
		return ""
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/pidfile/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pidfile/
git commit -m "feat: add PID file helper for server detection"
```

---

## Chunk 2: SQLite Store Layer

### Task 7: Store Initialization and Migrations

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

- [ ] **Step 1: Write failing test for store initialization**

```go
// internal/store/store_test.go
package store

import (
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q) error: %v", dbPath, err)
	}
	defer s.Close()

	// Verify tables exist by querying sqlite_master
	rows, err := s.db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}

	expected := []string{"events", "media_uploads", "messages", "webhooks"}
	if len(tables) < len(expected) {
		t.Errorf("tables = %v, want at least %v", tables, expected)
	}
	for _, exp := range expected {
		found := false
		for _, tbl := range tables {
			if tbl == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing table %q", exp)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL — package not found

- [ ] **Step 3: Implement store initialization**

```go
// internal/store/store.go
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store provides access to the application's SQLite database.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database at path and runs migrations.
func New(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for use by whatsmeow's sqlstore.
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS messages (
			id          TEXT PRIMARY KEY,
			chat_jid    TEXT NOT NULL,
			sender_jid  TEXT NOT NULL,
			wa_id       TEXT NOT NULL,
			type        TEXT NOT NULL,
			content     TEXT,
			media_type  TEXT,
			media_size  INTEGER,
			media_url   TEXT,
			media_key   BLOB,
			caption     TEXT,
			timestamp   INTEGER NOT NULL,
			is_from_me  INTEGER NOT NULL DEFAULT 0,
			is_read     INTEGER NOT NULL DEFAULT 0,
			raw_proto   BLOB,
			created_at  INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages(chat_jid, timestamp DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_wa_key ON messages(chat_jid, sender_jid, wa_id)`,

		`CREATE TABLE IF NOT EXISTS events (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			type        TEXT NOT NULL,
			payload     TEXT NOT NULL,
			timestamp   INTEGER NOT NULL,
			created_at  INTEGER NOT NULL DEFAULT (unixepoch())
		)`,

		`CREATE TABLE IF NOT EXISTS media_uploads (
			id          TEXT PRIMARY KEY,
			data        BLOB NOT NULL,
			mime_type   TEXT NOT NULL,
			filename    TEXT,
			size        INTEGER NOT NULL,
			created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
			expires_at  INTEGER NOT NULL
		)`,

		`CREATE TABLE IF NOT EXISTS webhooks (
			id          TEXT PRIMARY KEY,
			url         TEXT NOT NULL,
			events      TEXT NOT NULL,
			secret      TEXT,
			created_at  INTEGER NOT NULL DEFAULT (unixepoch())
		)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, m)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: add SQLite store initialization with schema migrations"
```

---

### Task 8: Message CRUD Operations

**Files:**
- Create: `internal/store/messages.go`
- Create: `internal/store/messages_test.go`

- [ ] **Step 1: Write failing tests for message operations**

```go
// internal/store/messages_test.go
package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndGetMessage(t *testing.T) {
	s := newTestStore(t)
	msg := &models.Message{
		ID:        "abc123def456",
		ChatJID:   "123@s.whatsapp.net",
		SenderJID: "456@s.whatsapp.net",
		WaID:      "WAID001",
		Type:      "text",
		Content:   "hello world",
		Timestamp: time.Now().Unix(),
		IsFromMe:  false,
	}
	if err := s.InsertMessage(msg); err != nil {
		t.Fatalf("InsertMessage error: %v", err)
	}

	got, err := s.GetMessage(msg.ID)
	if err != nil {
		t.Fatalf("GetMessage error: %v", err)
	}
	if got.Content != "hello world" {
		t.Errorf("content = %q, want %q", got.Content, "hello world")
	}
	if got.ChatJID != msg.ChatJID {
		t.Errorf("chat_jid = %q, want %q", got.ChatJID, msg.ChatJID)
	}
}

func TestGetMessages_Pagination(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		s.InsertMessage(&models.Message{
			ID:        "msg" + string(rune('a'+i)),
			ChatJID:   "chat@s.whatsapp.net",
			SenderJID: "sender@s.whatsapp.net",
			WaID:      "WA" + string(rune('a'+i)),
			Type:      "text",
			Content:   "message",
			Timestamp: now + int64(i),
		})
	}

	msgs, err := s.GetMessages("chat@s.whatsapp.net", 3, 0)
	if err != nil {
		t.Fatalf("GetMessages error: %v", err)
	}
	if len(msgs) != 3 {
		t.Errorf("got %d messages, want 3", len(msgs))
	}
	// Should be newest first
	if msgs[0].Timestamp < msgs[1].Timestamp {
		t.Error("messages not in descending timestamp order")
	}
}

func TestGetMessage_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetMessage("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent message")
	}
}

func TestDeleteMessage(t *testing.T) {
	s := newTestStore(t)
	msg := &models.Message{
		ID: "todelete", ChatJID: "c@s.whatsapp.net", SenderJID: "s@s.whatsapp.net",
		WaID: "W1", Type: "text", Timestamp: time.Now().Unix(),
	}
	s.InsertMessage(msg)

	if err := s.DeleteMessage("todelete"); err != nil {
		t.Fatalf("DeleteMessage error: %v", err)
	}
	_, err := s.GetMessage("todelete")
	if err == nil {
		t.Error("message still exists after delete")
	}
}

func TestUpdateReadStatus(t *testing.T) {
	s := newTestStore(t)
	msg := &models.Message{
		ID: "toread", ChatJID: "c@s.whatsapp.net", SenderJID: "s@s.whatsapp.net",
		WaID: "W2", Type: "text", Timestamp: time.Now().Unix(), IsRead: false,
	}
	s.InsertMessage(msg)

	if err := s.UpdateReadStatus("toread", true); err != nil {
		t.Fatalf("UpdateReadStatus error: %v", err)
	}
	got, _ := s.GetMessage("toread")
	if !got.IsRead {
		t.Error("message not marked as read")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestInsert -v`
Expected: FAIL — `InsertMessage` not defined

- [ ] **Step 3: Implement message CRUD**

```go
// internal/store/messages.go
package store

import (
	"database/sql"
	"fmt"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// InsertMessage stores a message. On conflict (duplicate wa_key), it updates content fields.
func (s *Store) InsertMessage(msg *models.Message) error {
	_, err := s.db.Exec(`
		INSERT INTO messages (id, chat_jid, sender_jid, wa_id, type, content, media_type,
			media_size, media_url, media_key, caption, timestamp, is_from_me, is_read, raw_proto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			is_read = excluded.is_read`,
		msg.ID, msg.ChatJID, msg.SenderJID, msg.WaID, msg.Type, msg.Content,
		msg.MediaType, msg.MediaSize, msg.MediaURL, msg.MediaKey, msg.Caption,
		msg.Timestamp, msg.IsFromMe, msg.IsRead, msg.RawProto,
	)
	return err
}

// GetMessage retrieves a message by its local composite ID.
func (s *Store) GetMessage(id string) (*models.Message, error) {
	msg := &models.Message{}
	err := s.db.QueryRow(`
		SELECT id, chat_jid, sender_jid, wa_id, type,
			COALESCE(content, ''), COALESCE(media_type, ''),
			COALESCE(media_size, 0), COALESCE(media_url, ''), media_key,
			COALESCE(caption, ''), timestamp, is_from_me, is_read,
			raw_proto, created_at
		FROM messages WHERE id = ?`, id,
	).Scan(
		&msg.ID, &msg.ChatJID, &msg.SenderJID, &msg.WaID, &msg.Type, &msg.Content,
		&msg.MediaType, &msg.MediaSize, &msg.MediaURL, &msg.MediaKey, &msg.Caption,
		&msg.Timestamp, &msg.IsFromMe, &msg.IsRead, &msg.RawProto, &msg.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("message %q not found", id)
	}
	return msg, err
}

// GetMessages retrieves messages for a chat, ordered by timestamp descending.
// If before > 0, only messages with timestamp < before are returned.
func (s *Store) GetMessages(chatJID string, limit int, before int64) ([]models.Message, error) {
	var rows *sql.Rows
	var err error

	// Note: media_url, media_key, raw_proto omitted from list queries (large/binary, not needed for listing)
	const listCols = `id, chat_jid, sender_jid, wa_id, type,
		COALESCE(content, ''), COALESCE(media_type, ''),
		COALESCE(media_size, 0), COALESCE(caption, ''),
		timestamp, is_from_me, is_read, created_at`

	if before > 0 {
		rows, err = s.db.Query(`SELECT `+listCols+`
			FROM messages WHERE chat_jid = ? AND timestamp < ?
			ORDER BY timestamp DESC LIMIT ?`, chatJID, before, limit)
	} else {
		rows, err = s.db.Query(`SELECT `+listCols+`
			FROM messages WHERE chat_jid = ?
			ORDER BY timestamp DESC LIMIT ?`, chatJID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []models.Message
	for rows.Next() {
		var m models.Message
		if err := rows.Scan(
			&m.ID, &m.ChatJID, &m.SenderJID, &m.WaID, &m.Type, &m.Content,
			&m.MediaType, &m.MediaSize, &m.Caption, &m.Timestamp,
			&m.IsFromMe, &m.IsRead, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// DeleteMessage removes a message by its local composite ID.
func (s *Store) DeleteMessage(id string) error {
	res, err := s.db.Exec("DELETE FROM messages WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("message %q not found", id)
	}
	return nil
}

// UpdateReadStatus marks a message as read or unread.
func (s *Store) UpdateReadStatus(id string, read bool) error {
	_, err := s.db.Exec("UPDATE messages SET is_read = ? WHERE id = ?", read, id)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/messages.go internal/store/messages_test.go
git commit -m "feat: add message CRUD operations to store"
```

---

### Task 9: Event Ring Buffer Operations

**Files:**
- Create: `internal/store/events.go`
- Create: `internal/store/events_test.go`

- [ ] **Step 1: Write failing tests for event operations**

```go
// internal/store/events_test.go
package store

import (
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestInsertAndGetEvents(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Unix()

	for i := 0; i < 3; i++ {
		err := s.InsertEvent(&models.Event{
			Type:      "message.received",
			Payload:   `{"text":"hello"}`,
			Timestamp: now + int64(i),
		})
		if err != nil {
			t.Fatalf("InsertEvent error: %v", err)
		}
	}

	events, err := s.GetEvents(0, 10)
	if err != nil {
		t.Fatalf("GetEvents error: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3", len(events))
	}
	// IDs should be monotonically increasing
	if events[0].ID >= events[1].ID {
		t.Error("event IDs not increasing")
	}
}

func TestGetEvents_Cursor(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Unix()
	for i := 0; i < 5; i++ {
		s.InsertEvent(&models.Event{
			Type: "message.received", Payload: "{}", Timestamp: now + int64(i),
		})
	}

	// Get first 2
	events, _ := s.GetEvents(0, 2)
	if len(events) != 2 {
		t.Fatalf("got %d, want 2", len(events))
	}

	// Get remaining after cursor
	cursor := events[len(events)-1].ID
	events2, _ := s.GetEvents(cursor, 10)
	if len(events2) != 3 {
		t.Errorf("after cursor got %d, want 3", len(events2))
	}
}

func TestPruneEvents(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Unix()
	for i := 0; i < 20; i++ {
		s.InsertEvent(&models.Event{
			Type: "test", Payload: "{}", Timestamp: now + int64(i),
		})
	}

	if err := s.PruneEvents(10); err != nil {
		t.Fatalf("PruneEvents error: %v", err)
	}

	events, _ := s.GetEvents(0, 100)
	if len(events) != 10 {
		t.Errorf("after prune got %d events, want 10", len(events))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestInsertAndGetEvents -v`
Expected: FAIL

- [ ] **Step 3: Implement event operations**

```go
// internal/store/events.go
package store

import (
	"github.com/piwi3910/whatsapp-go/internal/models"
)

// InsertEvent stores an event. The ID is auto-generated by AUTOINCREMENT.
func (s *Store) InsertEvent(evt *models.Event) error {
	res, err := s.db.Exec(
		"INSERT INTO events (type, payload, timestamp) VALUES (?, ?, ?)",
		evt.Type, evt.Payload, evt.Timestamp,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	evt.ID = id
	return nil
}

// GetEvents retrieves events with ID > after, up to limit rows.
func (s *Store) GetEvents(after int64, limit int) ([]models.Event, error) {
	rows, err := s.db.Query(
		"SELECT id, type, payload, timestamp FROM events WHERE id > ? ORDER BY id ASC LIMIT ?",
		after, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.Event
	for rows.Next() {
		var e models.Event
		if err := rows.Scan(&e.ID, &e.Type, &e.Payload, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// PruneEvents keeps only the most recent maxEvents rows.
func (s *Store) PruneEvents(maxEvents int) error {
	_, err := s.db.Exec(`
		DELETE FROM events WHERE id NOT IN (
			SELECT id FROM events ORDER BY id DESC LIMIT ?
		)`, maxEvents)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/events.go internal/store/events_test.go
git commit -m "feat: add event ring buffer to store"
```

---

### Task 10: Webhook CRUD Operations

**Files:**
- Create: `internal/store/webhooks.go`
- Create: `internal/store/webhooks_test.go`

- [ ] **Step 1: Write failing tests for webhook operations**

```go
// internal/store/webhooks_test.go
package store

import (
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestWebhookCRUD(t *testing.T) {
	s := newTestStore(t)

	wh := &models.Webhook{
		ID:     "wh_001",
		URL:    "https://example.com/hook",
		Events: []string{"message.received", "message.sent"},
		Secret: "s3cret",
	}

	// Insert
	if err := s.InsertWebhook(wh); err != nil {
		t.Fatalf("InsertWebhook error: %v", err)
	}

	// List
	hooks, err := s.GetWebhooks()
	if err != nil {
		t.Fatalf("GetWebhooks error: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("got %d webhooks, want 1", len(hooks))
	}
	if hooks[0].URL != "https://example.com/hook" {
		t.Errorf("url = %q, want %q", hooks[0].URL, "https://example.com/hook")
	}
	if len(hooks[0].Events) != 2 {
		t.Errorf("events count = %d, want 2", len(hooks[0].Events))
	}

	// Delete
	if err := s.DeleteWebhook("wh_001"); err != nil {
		t.Fatalf("DeleteWebhook error: %v", err)
	}
	hooks, _ = s.GetWebhooks()
	if len(hooks) != 0 {
		t.Errorf("got %d webhooks after delete, want 0", len(hooks))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestWebhook -v`
Expected: FAIL

- [ ] **Step 3: Implement webhook CRUD**

```go
// internal/store/webhooks.go
package store

import (
	"encoding/json"
	"fmt"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// InsertWebhook stores a webhook. Events are stored as a JSON array.
func (s *Store) InsertWebhook(wh *models.Webhook) error {
	eventsJSON, err := json.Marshal(wh.Events)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		"INSERT INTO webhooks (id, url, events, secret) VALUES (?, ?, ?, ?)",
		wh.ID, wh.URL, string(eventsJSON), wh.Secret,
	)
	return err
}

// GetWebhooks retrieves all registered webhooks.
func (s *Store) GetWebhooks() ([]models.Webhook, error) {
	rows, err := s.db.Query("SELECT id, url, events, COALESCE(secret, ''), created_at FROM webhooks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []models.Webhook
	for rows.Next() {
		var wh models.Webhook
		var eventsJSON string
		if err := rows.Scan(&wh.ID, &wh.URL, &eventsJSON, &wh.Secret, &wh.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(eventsJSON), &wh.Events); err != nil {
			return nil, fmt.Errorf("parsing webhook events: %w", err)
		}
		webhooks = append(webhooks, wh)
	}
	return webhooks, rows.Err()
}

// DeleteWebhook removes a webhook by ID.
func (s *Store) DeleteWebhook(id string) error {
	res, err := s.db.Exec("DELETE FROM webhooks WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("webhook %q not found", id)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/webhooks.go internal/store/webhooks_test.go
git commit -m "feat: add webhook CRUD to store"
```

---

### Task 11: Media Upload Operations

**Files:**
- Create: `internal/store/media.go`
- Create: `internal/store/media_test.go`

- [ ] **Step 1: Write failing tests for media upload operations**

```go
// internal/store/media_test.go
package store

import (
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestMediaUploadCRUD(t *testing.T) {
	s := newTestStore(t)

	upload := &models.MediaUpload{
		ID:        "media_001",
		MimeType:  "image/jpeg",
		Filename:  "photo.jpg",
		Size:      1024,
		Data:      []byte("fake image data"),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}

	// Insert
	if err := s.InsertMediaUpload(upload); err != nil {
		t.Fatalf("InsertMediaUpload error: %v", err)
	}

	// Get
	got, err := s.GetMediaUpload("media_001")
	if err != nil {
		t.Fatalf("GetMediaUpload error: %v", err)
	}
	if got.MimeType != "image/jpeg" {
		t.Errorf("mime = %q, want %q", got.MimeType, "image/jpeg")
	}
	if string(got.Data) != "fake image data" {
		t.Error("data mismatch")
	}

	// Delete
	if err := s.DeleteMediaUpload("media_001"); err != nil {
		t.Fatalf("DeleteMediaUpload error: %v", err)
	}
	_, err = s.GetMediaUpload("media_001")
	if err == nil {
		t.Error("media upload still exists after delete")
	}
}

func TestPruneExpiredUploads(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Unix()

	// Insert expired upload
	s.InsertMediaUpload(&models.MediaUpload{
		ID: "expired", MimeType: "image/png", Size: 100,
		Data: []byte("x"), ExpiresAt: now - 3600,
	})
	// Insert valid upload
	s.InsertMediaUpload(&models.MediaUpload{
		ID: "valid", MimeType: "image/png", Size: 100,
		Data: []byte("y"), ExpiresAt: now + 3600,
	})

	n, err := s.PruneExpiredUploads()
	if err != nil {
		t.Fatalf("PruneExpiredUploads error: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}

	// Valid should still exist
	_, err = s.GetMediaUpload("valid")
	if err != nil {
		t.Error("valid upload was pruned")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestMedia -v`
Expected: FAIL

- [ ] **Step 3: Implement media upload operations**

```go
// internal/store/media.go
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// InsertMediaUpload stores a temporary media upload.
func (s *Store) InsertMediaUpload(upload *models.MediaUpload) error {
	_, err := s.db.Exec(
		`INSERT INTO media_uploads (id, data, mime_type, filename, size, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		upload.ID, upload.Data, upload.MimeType, upload.Filename, upload.Size, upload.ExpiresAt,
	)
	return err
}

// GetMediaUpload retrieves a media upload by ID. Returns error if not found or expired.
func (s *Store) GetMediaUpload(id string) (*models.MediaUpload, error) {
	u := &models.MediaUpload{}
	err := s.db.QueryRow(
		`SELECT id, data, mime_type, filename, size, created_at, expires_at
		 FROM media_uploads WHERE id = ? AND expires_at > ?`, id, time.Now().Unix(),
	).Scan(&u.ID, &u.Data, &u.MimeType, &u.Filename, &u.Size, &u.CreatedAt, &u.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("media upload %q not found or expired", id)
	}
	return u, err
}

// DeleteMediaUpload removes a media upload by ID.
func (s *Store) DeleteMediaUpload(id string) error {
	_, err := s.db.Exec("DELETE FROM media_uploads WHERE id = ?", id)
	return err
}

// PruneExpiredUploads removes all expired media uploads. Returns the number of rows deleted.
func (s *Store) PruneExpiredUploads() (int64, error) {
	res, err := s.db.Exec("DELETE FROM media_uploads WHERE expires_at <= ?", time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/media.go internal/store/media_test.go
git commit -m "feat: add media upload temp storage to store"
```

---

## Chunk 3: Client Wrapper (whatsmeow Integration)

### Task 12: Client Interface and Struct

**Files:**
- Create: `internal/client/interface.go`
- Create: `internal/client/client.go`

- [ ] **Step 1: Define client interface**

This interface is used by the API and CLI layers for testability (mock implementations).

```go
// internal/client/interface.go
package client

import "github.com/piwi3910/whatsapp-go/internal/models"

// Service defines all WhatsApp operations used by the API and CLI layers.
type Service interface {
	// Connection
	Connect() error
	Disconnect()
	IsConnected() bool
	Status() ConnectionStatus

	// Auth
	Login() (<-chan QREvent, error)
	Logout() error

	// Messages
	SendText(jid, text string) (*models.SendResponse, error)
	SendImage(jid string, data []byte, filename, caption string) (*models.SendResponse, error)
	SendVideo(jid string, data []byte, filename, caption string) (*models.SendResponse, error)
	SendAudio(jid string, data []byte, filename string) (*models.SendResponse, error)
	SendDocument(jid string, data []byte, filename string) (*models.SendResponse, error)
	SendSticker(jid string, data []byte) (*models.SendResponse, error)
	SendLocation(jid string, lat, lon float64, name string) (*models.SendResponse, error)
	SendContact(jid, contactJID string) (*models.SendResponse, error)
	SendReaction(messageID, emoji string) error
	DeleteMessage(messageID string, forEveryone bool) error
	MarkRead(messageID string) error
	GetMessages(chatJID string, limit int, before int64) ([]models.Message, error)
	GetMessage(messageID string) (*models.Message, error)

	// Groups
	CreateGroup(name string, participants []string) (*models.Group, error)
	GetGroups() ([]models.Group, error)
	GetGroupInfo(groupJID string) (*models.Group, error)
	JoinGroup(inviteLink string) (string, error)
	LeaveGroup(groupJID string) error
	GetInviteLink(groupJID string) (string, error)
	AddParticipants(groupJID string, participants []string) error
	RemoveParticipants(groupJID string, participants []string) error
	PromoteParticipants(groupJID string, participants []string) error
	DemoteParticipants(groupJID string, participants []string) error

	// Contacts
	GetContacts() ([]models.Contact, error)
	GetContactInfo(jid string) (*models.Contact, error)
	BlockContact(jid string) error
	UnblockContact(jid string) error

	// Media
	DownloadMedia(messageID string) ([]byte, string, error) // data, mimeType, error

	// Events
	RegisterEventHandler(handler func(models.Event))
	SetupEventHandlers()
}

// ConnectionStatus represents the current connection state.
type ConnectionStatus struct {
	State       string `json:"state"` // "connecting", "connected", "disconnected", "logged_out"
	PhoneNumber string `json:"phone_number,omitempty"`
	PushName    string `json:"push_name,omitempty"`
}

// QREvent represents a QR code event during login.
type QREvent struct {
	Code string // QR code string for display
	Done bool   // true when login is complete
}
```

- [ ] **Step 2: Create client struct with Connect/Disconnect**

```go
// internal/client/client.go
package client

import (
	"context"
	"fmt"
	"os"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	appstore "github.com/piwi3910/whatsapp-go/internal/store"
)

// Client wraps whatsmeow and the app store, implementing the Service interface.
type Client struct {
	wac      *whatsmeow.Client
	store    *appstore.Store
	log      waLog.Logger
	handlers []func(models.Event)
}

// New creates a new Client. dbPath is the SQLite database path used for both
// whatsmeow's device store and the app's data.
func New(appStore *appstore.Store, dbPath string, log waLog.Logger) (*Client, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(on)", dbPath)
	container, err := sqlstore.New(context.Background(), "sqlite", dsn, log)
	if err != nil {
		return nil, fmt.Errorf("creating whatsmeow container: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getting device store: %w", err)
	}

	wac := whatsmeow.NewClient(deviceStore, log)
	return &Client{
		wac:   wac,
		store: appStore,
		log:   log,
	}, nil
}

// Connect establishes the WhatsApp connection.
func (c *Client) Connect() error {
	return c.wac.Connect()
}

// Disconnect closes the WhatsApp connection.
func (c *Client) Disconnect() {
	c.wac.Disconnect()
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool {
	return c.wac.IsConnected()
}
```

Note: The `New` constructor will be refined in implementation. The whatsmeow `sqlstore.New` needs the actual DB path/DSN — at implementation time, use the same SQLite database as the app store or a separate whatsmeow-specific database file. The key point is that `sqlstore` manages its own tables for device/session state.

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/client/...`
Expected: compiles (may need import fixes)

- [ ] **Step 4: Commit**

```bash
git add internal/client/interface.go internal/client/client.go
git commit -m "feat: add client interface and whatsmeow wrapper struct"
```

---

### Task 13: Authentication (Login/Logout/Status)

**Files:**
- Create: `internal/client/auth.go`

- [ ] **Step 1: Implement auth methods**

```go
// internal/client/auth.go
package client

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/types"
)

// Login initiates QR-based device linking. Returns a channel of QR events.
// The caller should display QR codes from the channel until Done is true.
func (c *Client) Login() (<-chan QREvent, error) {
	if c.wac.Store.ID != nil {
		return nil, fmt.Errorf("already logged in")
	}

	qrChan, err := c.wac.GetQRChannel(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getting QR channel: %w", err)
	}

	if err := c.wac.Connect(); err != nil {
		return nil, fmt.Errorf("connecting: %w", err)
	}

	// Bridge whatsmeow QR events to our QREvent type
	out := make(chan QREvent)
	go func() {
		defer close(out)
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				out <- QREvent{Code: evt.Code}
			case "success":
				out <- QREvent{Done: true}
				return
			case "timeout":
				out <- QREvent{Done: true}
				return
			}
		}
	}()

	return out, nil
}

// Logout unlinks the device and clears session data.
func (c *Client) Logout() error {
	if err := c.wac.Logout(context.Background()); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// Status returns the current connection and auth state.
func (c *Client) Status() ConnectionStatus {
	status := ConnectionStatus{State: "disconnected"}

	if c.wac.Store.ID == nil {
		status.State = "logged_out"
		return status
	}

	if c.wac.IsConnected() {
		status.State = "connected"
	} else if c.wac.IsLoggedIn() {
		status.State = "connecting"
	}

	// Get phone number and push name from device store
	if c.wac.Store.ID != nil {
		status.PhoneNumber = c.wac.Store.ID.User
		status.PushName = c.wac.Store.PushName
	}

	return status
}
```

Note: The `PushName` access pattern may need adjustment at implementation time depending on the whatsmeow version. The `Store.PushName` field type varies — check the actual type and adapt.

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/client/...`
Expected: compiles

- [ ] **Step 3: Commit**

```bash
git add internal/client/auth.go
git commit -m "feat: add login/logout/status to client"
```

---

### Task 14: Message Sending

**Files:**
- Create: `internal/client/message.go`

- [ ] **Step 1: Implement text message sending**

```go
// internal/client/message.go
package client

import (
	"context"
	"fmt"
	"time"

	"net/http"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/piwi3910/whatsapp-go/internal/jid"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (c *Client) parseJID(input string) (types.JID, error) {
	normalized, err := jid.NormalizeJID(input)
	if err != nil {
		return types.JID{}, err
	}
	return types.ParseJID(normalized)
}

// SendText sends a text message and stores it locally.
func (c *Client) SendText(jidStr, text string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	resp, err := c.wac.SendMessage(context.Background(), to, msg)
	if err != nil {
		return nil, fmt.Errorf("sending text: %w", err)
	}

	localID := jid.CompositeMessageID(to.String(), c.wac.Store.ID.String(), resp.ID)
	now := time.Now().Unix()

	// Store sent message
	c.store.InsertMessage(&models.Message{
		ID:        localID,
		ChatJID:   to.String(),
		SenderJID: c.wac.Store.ID.String(),
		WaID:      resp.ID,
		Type:      "text",
		Content:   text,
		Timestamp: now,
		IsFromMe:  true,
	})

	return &models.SendResponse{MessageID: localID, Timestamp: now}, nil
}

// SendImage sends an image message.
func (c *Client) SendImage(jidStr string, data []byte, filename, caption string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return nil, fmt.Errorf("uploading image: %w", err)
	}

	msg := &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
			Caption:       proto.String(caption),
		},
	}

	return c.sendAndStore(to, msg, "image", caption, data)
}

// SendVideo sends a video message.
func (c *Client) SendVideo(jidStr string, data []byte, filename, caption string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaVideo)
	if err != nil {
		return nil, fmt.Errorf("uploading video: %w", err)
	}

	msg := &waE2E.Message{
		VideoMessage: &waE2E.VideoMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
			Caption:       proto.String(caption),
		},
	}

	return c.sendAndStore(to, msg, "video", caption, data)
}

// SendAudio sends an audio message.
func (c *Client) SendAudio(jidStr string, data []byte, filename string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return nil, fmt.Errorf("uploading audio: %w", err)
	}

	msg := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
		},
	}

	return c.sendAndStore(to, msg, "audio", "", data)
}

// SendDocument sends a document message.
func (c *Client) SendDocument(jidStr string, data []byte, filename string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaDocument)
	if err != nil {
		return nil, fmt.Errorf("uploading document: %w", err)
	}

	msg := &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String(detectMIME(filename, data)),
			FileName:      proto.String(filename),
		},
	}

	return c.sendAndStore(to, msg, "document", "", data)
}

// SendSticker sends a sticker message.
func (c *Client) SendSticker(jidStr string, data []byte) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	uploaded, err := c.wac.Upload(context.Background(), data, whatsmeow.MediaImage)
	if err != nil {
		return nil, fmt.Errorf("uploading sticker: %w", err)
	}

	msg := &waE2E.Message{
		StickerMessage: &waE2E.StickerMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			Mimetype:      proto.String("image/webp"),
		},
	}

	return c.sendAndStore(to, msg, "sticker", "", data)
}

// SendLocation sends a location message.
func (c *Client) SendLocation(jidStr string, lat, lon float64, name string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	msg := &waE2E.Message{
		LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(lat),
			DegreesLongitude: proto.Float64(lon),
			Name:             proto.String(name),
		},
	}

	return c.sendAndStore(to, msg, "location", "", nil)
}

// SendContact sends a contact card message.
func (c *Client) SendContact(jidStr, contactJIDStr string) (*models.SendResponse, error) {
	to, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	// Build a simple vCard for the contact
	vcard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nTEL:%s\nEND:VCARD", contactJIDStr)

	msg := &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String(contactJIDStr),
			Vcard:       proto.String(vcard),
		},
	}

	return c.sendAndStore(to, msg, "contact", "", nil)
}

// SendReaction reacts to a message. Looks up the message in the local store
// to get the full key tuple needed by whatsmeow.
func (c *Client) SendReaction(messageID, emoji string) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	reaction := c.wac.BuildReaction(chatJID, senderJID, msg.WaID, emoji)
	_, err = c.wac.SendMessage(context.Background(), chatJID, reaction)
	return err
}

// DeleteMessage revokes a message.
func (c *Client) DeleteMessage(messageID string, forEveryone bool) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	if forEveryone {
		revoke := c.wac.BuildRevoke(chatJID, senderJID, msg.WaID)
		_, err = c.wac.SendMessage(context.Background(), chatJID, revoke)
		if err != nil {
			return err
		}
	}

	return c.store.DeleteMessage(messageID)
}

// MarkRead marks a message as read in both WhatsApp and local store.
func (c *Client) MarkRead(messageID string) error {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}

	chatJID, _ := types.ParseJID(msg.ChatJID)
	senderJID, _ := types.ParseJID(msg.SenderJID)

	err = c.wac.MarkRead(
		context.Background(),
		[]types.MessageID{msg.WaID},
		time.Now(),
		chatJID,
		senderJID,
	)
	if err != nil {
		return err
	}

	return c.store.UpdateReadStatus(messageID, true)
}

// GetMessages retrieves messages from the local store.
func (c *Client) GetMessages(chatJID string, limit int, before int64) ([]models.Message, error) {
	return c.store.GetMessages(chatJID, limit, before)
}

// GetMessage retrieves a single message from the local store.
func (c *Client) GetMessage(messageID string) (*models.Message, error) {
	return c.store.GetMessage(messageID)
}

// sendAndStore is a helper that sends a message and stores it locally.
func (c *Client) sendAndStore(to types.JID, msg *waE2E.Message, msgType, caption string, mediaData []byte) (*models.SendResponse, error) {
	resp, err := c.wac.SendMessage(context.Background(), to, msg)
	if err != nil {
		return nil, fmt.Errorf("sending %s: %w", msgType, err)
	}

	localID := jid.CompositeMessageID(to.String(), c.wac.Store.ID.String(), resp.ID)
	now := time.Now().Unix()

	stored := &models.Message{
		ID:        localID,
		ChatJID:   to.String(),
		SenderJID: c.wac.Store.ID.String(),
		WaID:      resp.ID,
		Type:      msgType,
		Caption:   caption,
		Timestamp: now,
		IsFromMe:  true,
	}
	if mediaData != nil {
		stored.MediaSize = int64(len(mediaData))
	}
	c.store.InsertMessage(stored)

	return &models.SendResponse{MessageID: localID, Timestamp: now}, nil
}

// detectMIME detects the MIME type from filename extension, falling back to http.DetectContentType.
func detectMIME(filename string, data []byte) string {
	ext := ""
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '.' {
			ext = filename[i:]
			break
		}
	}

	mimeMap := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
		".gif": "image/gif", ".webp": "image/webp",
		".mp4": "video/mp4", ".3gp": "video/3gpp", ".mov": "video/quicktime",
		".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".m4a": "audio/mp4", ".wav": "audio/wav",
		".pdf": "application/pdf", ".doc": "application/msword",
	}

	if mime, ok := mimeMap[ext]; ok {
		return mime
	}

	// Fallback to content detection
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
}
```

Note: The `detectMIME` function needs `import "net/http"` — add to the import block.

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/client/...`
Expected: compiles

- [ ] **Step 3: Commit**

```bash
git add internal/client/message.go
git commit -m "feat: add message sending/reactions/delete to client"
```

---

### Task 15: Group Operations

**Files:**
- Create: `internal/client/group.go`

- [ ] **Step 1: Implement group operations**

```go
// internal/client/group.go
package client

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// CreateGroup creates a new WhatsApp group.
func (c *Client) CreateGroup(name string, participants []string) (*models.Group, error) {
	jids := make([]types.JID, len(participants))
	for i, p := range participants {
		j, err := c.parseJID(p)
		if err != nil {
			return nil, fmt.Errorf("invalid participant %q: %w", p, err)
		}
		jids[i] = j
	}

	req := whatsmeow.ReqCreateGroup{
		Name:         name,
		Participants: jids,
	}
	info, err := c.wac.CreateGroup(context.Background(), req)
	if err != nil {
		return nil, fmt.Errorf("creating group: %w", err)
	}

	return groupInfoToModel(info), nil
}

// GetGroups returns all joined groups.
func (c *Client) GetGroups() ([]models.Group, error) {
	groups, err := c.wac.GetJoinedGroups(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getting groups: %w", err)
	}

	result := make([]models.Group, len(groups))
	for i, g := range groups {
		result[i] = *groupInfoToModel(g)
	}
	return result, nil
}

// GetGroupInfo returns info about a specific group.
func (c *Client) GetGroupInfo(groupJID string) (*models.Group, error) {
	j, err := c.parseJID(groupJID)
	if err != nil {
		return nil, err
	}
	info, err := c.wac.GetGroupInfo(context.Background(), j)
	if err != nil {
		return nil, fmt.Errorf("getting group info: %w", err)
	}
	return groupInfoToModel(info), nil
}

// JoinGroup joins a group via invite link. Returns the group JID.
func (c *Client) JoinGroup(inviteLink string) (string, error) {
	// Extract code from link (format: https://chat.whatsapp.com/CODE)
	code := inviteLink
	if strings.HasPrefix(code, "https://chat.whatsapp.com/") {
		code = strings.TrimPrefix(code, "https://chat.whatsapp.com/")
	} else if strings.HasPrefix(code, "http://chat.whatsapp.com/") {
		code = strings.TrimPrefix(code, "http://chat.whatsapp.com/")
	}

	groupJID, err := c.wac.JoinGroupWithLink(context.Background(), code)
	if err != nil {
		return "", fmt.Errorf("joining group: %w", err)
	}
	return groupJID.String(), nil
}

// LeaveGroup leaves a group.
func (c *Client) LeaveGroup(groupJID string) error {
	j, err := c.parseJID(groupJID)
	if err != nil {
		return err
	}
	return c.wac.LeaveGroup(context.Background(), j)
}

// GetInviteLink returns the group invite link.
func (c *Client) GetInviteLink(groupJID string) (string, error) {
	j, err := c.parseJID(groupJID)
	if err != nil {
		return "", err
	}
	link, err := c.wac.GetGroupInviteLink(context.Background(), j, false)
	if err != nil {
		return "", fmt.Errorf("getting invite link: %w", err)
	}
	return link, nil
}

// AddParticipants adds participants to a group.
func (c *Client) AddParticipants(groupJID string, participants []string) error {
	return c.updateParticipants(groupJID, participants, whatsmeow.ParticipantChangeAdd)
}

// RemoveParticipants removes participants from a group.
func (c *Client) RemoveParticipants(groupJID string, participants []string) error {
	return c.updateParticipants(groupJID, participants, whatsmeow.ParticipantChangeRemove)
}

// PromoteParticipants makes participants group admins.
func (c *Client) PromoteParticipants(groupJID string, participants []string) error {
	return c.updateParticipants(groupJID, participants, whatsmeow.ParticipantChangePromote)
}

// DemoteParticipants removes admin status from participants.
func (c *Client) DemoteParticipants(groupJID string, participants []string) error {
	return c.updateParticipants(groupJID, participants, whatsmeow.ParticipantChangeDemote)
}

func (c *Client) updateParticipants(groupJID string, participants []string, action whatsmeow.ParticipantChange) error {
	gJID, err := c.parseJID(groupJID)
	if err != nil {
		return err
	}

	jids := make([]types.JID, len(participants))
	for i, p := range participants {
		j, err := c.parseJID(p)
		if err != nil {
			return fmt.Errorf("invalid participant %q: %w", p, err)
		}
		jids[i] = j
	}

	_, err = c.wac.UpdateGroupParticipants(context.Background(), gJID, jids, action)
	return err
}

func groupInfoToModel(info *types.GroupInfo) *models.Group {
	participants := make([]models.Participant, len(info.Participants))
	for i, p := range info.Participants {
		participants[i] = models.Participant{
			JID:          p.JID.String(),
			IsAdmin:      p.IsAdmin,
			IsSuperAdmin: p.IsSuperAdmin,
		}
	}
	return &models.Group{
		JID:          info.JID.String(),
		Name:         info.GroupName.Name,
		Topic:        info.GroupTopic.Topic,
		Created:      info.GroupCreated.Unix(),
		Participants: participants,
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/client/...`
Expected: compiles

- [ ] **Step 3: Commit**

```bash
git add internal/client/group.go
git commit -m "feat: add group operations to client"
```

---

### Task 16: Contact Operations

**Files:**
- Create: `internal/client/contact.go`

- [ ] **Step 1: Implement contact operations**

```go
// internal/client/contact.go
package client

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/types"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// GetContacts returns all synced contacts.
func (c *Client) GetContacts() ([]models.Contact, error) {
	contacts, err := c.wac.Store.Contacts.GetAllContacts(context.Background())
	if err != nil {
		return nil, fmt.Errorf("getting contacts: %w", err)
	}

	var result []models.Contact
	for jid, info := range contacts {
		result = append(result, models.Contact{
			JID:      jid.String(),
			Name:     info.FullName,
			PushName: info.PushName,
		})
	}
	return result, nil
}

// GetContactInfo returns info about a specific contact.
func (c *Client) GetContactInfo(jidStr string) (*models.Contact, error) {
	j, err := c.parseJID(jidStr)
	if err != nil {
		return nil, err
	}

	// Get user info from WhatsApp
	users, err := c.wac.GetUserInfo(context.Background(), []types.JID{j})
	if err != nil {
		return nil, fmt.Errorf("getting user info: %w", err)
	}

	info, ok := users[j]
	if !ok {
		return nil, fmt.Errorf("contact %q not found", jidStr)
	}

	contact := &models.Contact{
		JID:       j.String(),
		Status:    info.Status,
		PictureID: info.PictureID,
	}

	// Try to get name from contact store
	stored, err := c.wac.Store.Contacts.GetContact(context.Background(), j)
	if err == nil {
		contact.Name = stored.FullName
		contact.PushName = stored.PushName
	}

	return contact, nil
}

// BlockContact blocks a contact.
func (c *Client) BlockContact(jidStr string) error {
	j, err := c.parseJID(jidStr)
	if err != nil {
		return err
	}
	_, err = c.wac.UpdateBlocklist(context.Background(), j, "block")
	return err
}

// UnblockContact unblocks a contact.
func (c *Client) UnblockContact(jidStr string) error {
	j, err := c.parseJID(jidStr)
	if err != nil {
		return err
	}
	_, err = c.wac.UpdateBlocklist(context.Background(), j, "unblock")
	return err
}
```

Note: The `UpdateBlocklist` method signature may differ in the actual whatsmeow version. At implementation time, check `client.UpdateBlocklist` or equivalent blocklist management method. If it doesn't exist, use the lower-level `SetPrivacySetting` approach.

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/client/...`
Expected: compiles

- [ ] **Step 3: Commit**

```bash
git add internal/client/contact.go
git commit -m "feat: add contact operations to client"
```

---

### Task 17: Media Download

**Files:**
- Create: `internal/client/media.go`

- [ ] **Step 1: Implement media download**

```go
// internal/client/media.go
package client

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// DownloadMedia downloads media from a message. Returns the raw bytes, MIME type, and any error.
// Reconstructs the protobuf message from raw_proto stored in DB, then uses
// whatsmeow's Download which handles decryption and hash verification.
func (c *Client) DownloadMedia(messageID string) ([]byte, string, error) {
	msg, err := c.store.GetMessage(messageID)
	if err != nil {
		return nil, "", fmt.Errorf("message not found: %w", err)
	}

	if len(msg.RawProto) == 0 {
		return nil, "", fmt.Errorf("message %q has no stored media proto", messageID)
	}

	// Reconstruct the protobuf message
	var protoMsg waE2E.Message
	if err := proto.Unmarshal(msg.RawProto, &protoMsg); err != nil {
		return nil, "", fmt.Errorf("unmarshaling proto: %w", err)
	}

	// whatsmeow.Download accepts any DownloadableMessage (ImageMessage, VideoMessage, etc.)
	// Extract the correct sub-message type
	downloadable := extractDownloadable(&protoMsg)
	if downloadable == nil {
		return nil, "", fmt.Errorf("message %q has no downloadable media", messageID)
	}

	data, err := c.wac.Download(context.Background(), downloadable)
	if err != nil {
		return nil, "", fmt.Errorf("downloading media: %w", err)
	}

	return data, msg.MediaType, nil
}

// extractDownloadable returns the DownloadableMessage from a proto message.
func extractDownloadable(msg *waE2E.Message) whatsmeow.DownloadableMessage {
	switch {
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage()
	case msg.GetAudioMessage() != nil:
		return msg.GetAudioMessage()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage()
	case msg.GetStickerMessage() != nil:
		return msg.GetStickerMessage()
	default:
		return nil
	}
}

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/client/...`
Expected: compiles

- [ ] **Step 3: Commit**

```bash
git add internal/client/media.go
git commit -m "feat: add media download to client"
```

---

### Task 18: Event Handler

**Files:**
- Create: `internal/client/events.go`

- [ ] **Step 1: Implement event handler that maps whatsmeow events to model events**

```go
// internal/client/events.go
package client

import (
	"encoding/json"
	"time"

	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/piwi3910/whatsapp-go/internal/jid"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

// RegisterEventHandler registers a handler that receives mapped events.
func (c *Client) RegisterEventHandler(handler func(models.Event)) {
	c.handlers = append(c.handlers, handler)
}

// SetupEventHandlers registers the whatsmeow event handler that processes
// all incoming events, stores messages, and dispatches to registered handlers.
func (c *Client) SetupEventHandlers() {
	c.wac.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			c.handleMessage(v)
		case *events.Receipt:
			c.handleReceipt(v)
		case *events.Connected:
			c.dispatch(models.Event{
				Type:      models.EventConnectionConnected,
				Payload:   "{}",
				Timestamp: time.Now().Unix(),
			})
		case *events.Disconnected:
			c.dispatch(models.Event{
				Type:      models.EventConnectionDisconnected,
				Payload:   "{}",
				Timestamp: time.Now().Unix(),
			})
		case *events.LoggedOut:
			c.dispatch(models.Event{
				Type:      models.EventConnectionLoggedOut,
				Payload:   "{}",
				Timestamp: time.Now().Unix(),
			})
		case *events.GroupInfo:
			c.handleGroupEvent(v)
		case *events.JoinedGroup:
			payload, _ := json.Marshal(map[string]string{"group_jid": v.JID.String()})
			c.dispatch(models.Event{
				Type:      models.EventGroupCreated,
				Payload:   string(payload),
				Timestamp: time.Now().Unix(),
			})
		case *events.PushName:
			payload, _ := json.Marshal(map[string]string{
				"jid": v.JID.String(), "push_name": v.NewPushName,
			})
			c.dispatch(models.Event{
				Type:      models.EventContactUpdated,
				Payload:   string(payload),
				Timestamp: time.Now().Unix(),
			})
		case *events.Presence:
			payload, _ := json.Marshal(map[string]interface{}{
				"jid": v.From.String(), "unavailable": v.Unavailable,
			})
			c.dispatch(models.Event{
				Type:      models.EventPresenceUpdated,
				Payload:   string(payload),
				Timestamp: time.Now().Unix(),
			})
		}
	})
}

func (c *Client) handleMessage(v *events.Message) {
	info := v.Info
	chatJID := info.Chat.String()
	senderJID := info.Sender.String()
	waID := info.ID

	localID := jid.CompositeMessageID(chatJID, senderJID, waID)

	msgType, content, caption := extractMessageContent(v.Message)

	var rawProto []byte
	if v.Message != nil {
		rawProto, _ = proto.Marshal(v.Message)
	}

	msg := &models.Message{
		ID:        localID,
		ChatJID:   chatJID,
		SenderJID: senderJID,
		WaID:      waID,
		Type:      msgType,
		Content:   content,
		Caption:   caption,
		Timestamp: info.Timestamp.Unix(),
		IsFromMe:  info.IsFromMe,
		RawProto:  rawProto,
	}

	// Extract media metadata if present
	if img := v.Message.GetImageMessage(); img != nil {
		msg.MediaType = img.GetMimetype()
		msg.MediaSize = int64(img.GetFileLength())
		msg.MediaURL = img.GetDirectPath()
		msg.MediaKey = img.GetMediaKey()
	} else if vid := v.Message.GetVideoMessage(); vid != nil {
		msg.MediaType = vid.GetMimetype()
		msg.MediaSize = int64(vid.GetFileLength())
		msg.MediaURL = vid.GetDirectPath()
		msg.MediaKey = vid.GetMediaKey()
	} else if aud := v.Message.GetAudioMessage(); aud != nil {
		msg.MediaType = aud.GetMimetype()
		msg.MediaSize = int64(aud.GetFileLength())
		msg.MediaURL = aud.GetDirectPath()
		msg.MediaKey = aud.GetMediaKey()
	} else if doc := v.Message.GetDocumentMessage(); doc != nil {
		msg.MediaType = doc.GetMimetype()
		msg.MediaSize = int64(doc.GetFileLength())
		msg.MediaURL = doc.GetDirectPath()
		msg.MediaKey = doc.GetMediaKey()
	} else if stk := v.Message.GetStickerMessage(); stk != nil {
		msg.MediaType = stk.GetMimetype()
		msg.MediaSize = int64(stk.GetFileLength())
		msg.MediaURL = stk.GetDirectPath()
		msg.MediaKey = stk.GetMediaKey()
	}

	c.store.InsertMessage(msg)

	eventType := models.EventMessageReceived
	if info.IsFromMe {
		eventType = models.EventMessageSent
	}
	payload, _ := json.Marshal(msg)
	c.dispatch(models.Event{
		Type:      eventType,
		Payload:   string(payload),
		Timestamp: info.Timestamp.Unix(),
	})
}

func (c *Client) handleReceipt(v *events.Receipt) {
	if v.Type == "read" {
		for _, id := range v.MessageIDs {
			// Try to find and update in local store (best effort)
			localID := jid.CompositeMessageID(v.Chat.String(), v.Sender.String(), id)
			c.store.UpdateReadStatus(localID, true)
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"chat_jid":    v.Chat.String(),
			"message_ids": v.MessageIDs,
		})
		c.dispatch(models.Event{
			Type:      models.EventMessageRead,
			Payload:   string(payload),
			Timestamp: time.Now().Unix(),
		})
	}
}

func (c *Client) handleGroupEvent(v *events.GroupInfo) {
	if len(v.Join) > 0 {
		jids := make([]string, len(v.Join))
		for i, j := range v.Join {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantAdded, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if len(v.Leave) > 0 {
		jids := make([]string, len(v.Leave))
		for i, j := range v.Leave {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantRemoved, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if len(v.Promote) > 0 {
		jids := make([]string, len(v.Promote))
		for i, j := range v.Promote {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantPromoted, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if len(v.Demote) > 0 {
		jids := make([]string, len(v.Demote))
		for i, j := range v.Demote {
			jids[i] = j.String()
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"group_jid": v.JID.String(), "participants": jids,
		})
		c.dispatch(models.Event{
			Type: models.EventGroupParticipantDemoted, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
	if v.Name != nil || v.Topic != nil {
		payload, _ := json.Marshal(map[string]string{"group_jid": v.JID.String()})
		c.dispatch(models.Event{
			Type: models.EventGroupUpdated, Payload: string(payload), Timestamp: v.Timestamp.Unix(),
		})
	}
}

func (c *Client) dispatch(evt models.Event) {
	// Store event in DB
	c.store.InsertEvent(&evt)

	// Fan out to registered handlers
	for _, h := range c.handlers {
		h(evt)
	}
}

// extractMessageContent extracts the type, content text, and caption from a whatsmeow message.
func extractMessageContent(msg *waE2E.Message) (msgType, content, caption string) {
	switch {
	case msg.GetConversation() != "":
		return "text", msg.GetConversation(), ""
	case msg.GetExtendedTextMessage() != nil:
		return "text", msg.GetExtendedTextMessage().GetText(), ""
	case msg.GetImageMessage() != nil:
		return "image", "", msg.GetImageMessage().GetCaption()
	case msg.GetVideoMessage() != nil:
		return "video", "", msg.GetVideoMessage().GetCaption()
	case msg.GetAudioMessage() != nil:
		return "audio", "", ""
	case msg.GetDocumentMessage() != nil:
		return "document", "", msg.GetDocumentMessage().GetCaption()
	case msg.GetStickerMessage() != nil:
		return "sticker", "", ""
	case msg.GetLocationMessage() != nil:
		loc := msg.GetLocationMessage()
		locJSON, _ := json.Marshal(map[string]interface{}{
			"lat": loc.GetDegreesLatitude(), "lon": loc.GetDegreesLongitude(),
			"name": loc.GetName(),
		})
		return "location", string(locJSON), ""
	case msg.GetContactMessage() != nil:
		return "contact", msg.GetContactMessage().GetVcard(), ""
	case msg.GetReactionMessage() != nil:
		return "reaction", msg.GetReactionMessage().GetText(), ""
	default:
		return "unknown", "", ""
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/client/...`
Expected: compiles

- [ ] **Step 3: Commit**

```bash
git add internal/client/events.go
git commit -m "feat: add event handler with whatsmeow event mapping"
```

---

## Chunk 4: Webhook Dispatcher + REST API Server

### Task 19: Webhook Dispatcher

**Files:**
- Create: `internal/webhook/dispatcher.go`
- Create: `internal/webhook/dispatcher_test.go`

- [ ] **Step 1: Write failing tests for webhook dispatcher**

```go
// internal/webhook/dispatcher_test.go
package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestDispatch_MatchingEvent(t *testing.T) {
	var received []byte
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{
		ID:     "wh1",
		URL:    srv.URL,
		Events: []string{"message.received"},
	})

	d.Dispatch(models.Event{
		Type:      "message.received",
		Payload:   `{"text":"hello"}`,
		Timestamp: time.Now().Unix(),
	})

	// Wait for async delivery
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("webhook was not called")
	}

	var body map[string]interface{}
	json.Unmarshal(received, &body)
	if body["event"] != "message.received" {
		t.Errorf("event = %v, want message.received", body["event"])
	}
}

func TestDispatch_WildcardSubscription(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"*"}})

	d.Dispatch(models.Event{
		Type: "group.created", Payload: "{}", Timestamp: time.Now().Unix(),
	})

	time.Sleep(100 * time.Millisecond)
	if !called {
		t.Error("wildcard webhook was not called")
	}
}

func TestDispatch_NonMatchingEvent(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{ID: "wh1", URL: srv.URL, Events: []string{"message.sent"}})

	d.Dispatch(models.Event{
		Type: "message.received", Payload: "{}", Timestamp: time.Now().Unix(),
	})

	time.Sleep(100 * time.Millisecond)
	if called {
		t.Error("webhook should not have been called for non-matching event")
	}
}

func TestDispatch_HMACSignature(t *testing.T) {
	var sigHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHeader = r.Header.Get("X-Wa-Signature")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := New()
	d.Register(models.Webhook{
		ID: "wh1", URL: srv.URL, Events: []string{"*"}, Secret: "mysecret",
	})

	d.Dispatch(models.Event{
		Type: "test", Payload: "{}", Timestamp: time.Now().Unix(),
	})

	time.Sleep(100 * time.Millisecond)
	if sigHeader == "" {
		t.Error("HMAC signature header missing")
	}
	if len(sigHeader) < 10 || sigHeader[:7] != "sha256=" {
		t.Errorf("signature format wrong: %q", sigHeader)
	}
}

func TestUnregister(t *testing.T) {
	d := New()
	d.Register(models.Webhook{ID: "wh1", URL: "http://example.com", Events: []string{"*"}})
	d.Unregister("wh1")

	// Should have no webhooks
	if len(d.webhooks) != 0 {
		t.Errorf("still have %d webhooks after unregister", len(d.webhooks))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/webhook/ -v`
Expected: FAIL

- [ ] **Step 3: Implement webhook dispatcher**

```go
// internal/webhook/dispatcher.go
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

// Dispatcher manages webhook delivery with retries and HMAC signing.
type Dispatcher struct {
	mu       sync.RWMutex
	webhooks map[string]models.Webhook
	client   *http.Client
}

// New creates a new Dispatcher.
func New() *Dispatcher {
	return &Dispatcher{
		webhooks: make(map[string]models.Webhook),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Register adds a webhook.
func (d *Dispatcher) Register(wh models.Webhook) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.webhooks[wh.ID] = wh
}

// Unregister removes a webhook.
func (d *Dispatcher) Unregister(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.webhooks, id)
}

// Dispatch sends an event to all matching webhooks asynchronously.
func (d *Dispatcher) Dispatch(evt models.Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, wh := range d.webhooks {
		if !matchesEvent(wh.Events, evt.Type) {
			continue
		}
		// Deliver asynchronously
		go d.deliver(wh, evt)
	}
}

func matchesEvent(subscribed []string, eventType string) bool {
	for _, s := range subscribed {
		if s == "*" || s == eventType {
			return true
		}
	}
	return false
}

// webhookPayload is the JSON body sent to webhook URLs.
type webhookPayload struct {
	Event     string          `json:"event"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

func (d *Dispatcher) deliver(wh models.Webhook, evt models.Event) {
	payload := webhookPayload{
		Event:     evt.Type,
		Timestamp: evt.Timestamp,
		Data:      json.RawMessage(evt.Payload),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhook %s: marshal error: %v", wh.ID, err)
		return
	}

	// Retry schedule: 1s, 5s, 15s
	delays := []time.Duration{0, 1 * time.Second, 5 * time.Second, 15 * time.Second}

	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}

		req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("webhook %s: request error: %v", wh.ID, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		// Add HMAC signature if secret is configured
		if wh.Secret != "" {
			mac := hmac.New(sha256.New, []byte(wh.Secret))
			mac.Write(body)
			sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-Wa-Signature", sig)
		}

		resp, err := d.client.Do(req)
		if err != nil {
			log.Printf("webhook %s: attempt %d failed: %v", wh.ID, attempt+1, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // Success
		}
		log.Printf("webhook %s: attempt %d status %d", wh.ID, attempt+1, resp.StatusCode)
	}

	log.Printf("webhook %s: all delivery attempts failed for event %s", wh.ID, evt.Type)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/webhook/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/webhook/
git commit -m "feat: add webhook dispatcher with retries and HMAC signing"
```

---

### Task 20: REST API Server, Middleware, and Response Helpers

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/middleware.go`
- Create: `internal/api/response.go`

- [ ] **Step 1: Create response helpers**

```go
// internal/api/response.go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.APIResponse{OK: true, Data: data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.APIResponse{
		OK:    false,
		Error: &models.APIError{Code: code, Message: message},
	})
}
```

- [ ] **Step 2: Create middleware**

```go
// internal/api/middleware.go
package api

import (
	"log"
	"net/http"
	"time"
)

// apiKeyAuth returns middleware that validates the Bearer token.
func apiKeyAuth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" || len(auth) < 8 || auth[:7] != "Bearer " {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid Authorization header")
				return
			}
			token := auth[7:]
			if token != apiKey {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid API key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger logs each request with method, path, status, and duration.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// recoverer catches panics and returns 500.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v", err)
				writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 3: Create server with chi router**

```go
// internal/api/server.go
package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/internal/webhook"
)

// Server is the REST API server.
type Server struct {
	router     chi.Router
	httpServer *http.Server
	client     client.Service
	store      *store.Store
	dispatcher *webhook.Dispatcher
	apiKey     string
	startTime  time.Time
	version    string
}

// NewServer creates a new API server.
func NewServer(svc client.Service, st *store.Store, disp *webhook.Dispatcher, apiKey, version string, maxUploadSize int64) *Server {
	s := &Server{
		client:     svc,
		store:      st,
		dispatcher: disp,
		apiKey:     apiKey,
		version:    version,
		startTime:  time.Now(),
	}

	r := chi.NewRouter()
	r.Use(recoverer)
	r.Use(requestLogger)

	// Health endpoint — no auth required
	r.Get("/api/v1/health", s.handleHealth)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(apiKeyAuth(apiKey))
		if maxUploadSize > 0 {
			r.Use(func(next http.Handler) http.Handler {
				return http.MaxBytesHandler(next, maxUploadSize)
			})
		}

		// Auth
		r.Post("/api/v1/auth/login", s.handleLogin)
		r.Post("/api/v1/auth/logout", s.handleLogout)
		r.Get("/api/v1/auth/status", s.handleAuthStatus)

		// Messages
		r.Post("/api/v1/messages/send", s.handleSendMessage)
		r.Get("/api/v1/messages", s.handleListMessages)
		r.Get("/api/v1/messages/{id}", s.handleGetMessage)
		r.Delete("/api/v1/messages/{id}", s.handleDeleteMessage)
		r.Post("/api/v1/messages/{id}/react", s.handleReactMessage)
		r.Post("/api/v1/messages/{id}/read", s.handleMarkRead)

		// Groups
		r.Post("/api/v1/groups", s.handleCreateGroup)
		r.Get("/api/v1/groups", s.handleListGroups)
		r.Get("/api/v1/groups/{jid}", s.handleGetGroup)
		r.Post("/api/v1/groups/{jid}/leave", s.handleLeaveGroup)
		r.Get("/api/v1/groups/{jid}/invite-link", s.handleGetInviteLink)
		r.Post("/api/v1/groups/join", s.handleJoinGroup)
		r.Post("/api/v1/groups/{jid}/participants/add", s.handleAddParticipants)
		r.Post("/api/v1/groups/{jid}/participants/remove", s.handleRemoveParticipants)
		r.Post("/api/v1/groups/{jid}/participants/promote", s.handlePromoteParticipants)
		r.Post("/api/v1/groups/{jid}/participants/demote", s.handleDemoteParticipants)

		// Contacts
		r.Get("/api/v1/contacts", s.handleListContacts)
		r.Get("/api/v1/contacts/{jid}", s.handleGetContact)
		r.Post("/api/v1/contacts/{jid}/block", s.handleBlockContact)
		r.Post("/api/v1/contacts/{jid}/unblock", s.handleUnblockContact)

		// Media
		r.Post("/api/v1/media/upload", s.handleUploadMedia)
		r.Get("/api/v1/media/{messageId}", s.handleDownloadMedia)

		// Webhooks
		r.Post("/api/v1/webhooks", s.handleCreateWebhook)
		r.Get("/api/v1/webhooks", s.handleListWebhooks)
		r.Delete("/api/v1/webhooks/{id}", s.handleDeleteWebhook)

		// Events
		r.Get("/api/v1/events", s.handleListEvents)
	})

	s.router = r
	return s
}

// Start begins listening. Blocks until the server is stopped.
func (s *Server) Start(host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("API server listening on %s", addr)
	return s.httpServer.Serve(ln)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./internal/api/...`
Expected: will fail because handler methods are not yet defined — that's expected, they come in subsequent tasks

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/middleware.go internal/api/response.go
git commit -m "feat: add REST API server skeleton with middleware and routing"
```

---

### Task 21: API — Health and Auth Handlers

**Files:**
- Create: `internal/api/auth.go`

- [ ] **Step 1: Implement health and auth handlers**

```go
// internal/api/auth.go
package api

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/skip2/go-qrcode"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := s.client.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state":          status.State,
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"version":        s.version,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	qrChan, err := s.client.Login()
	if err != nil {
		writeError(w, http.StatusBadRequest, "LOGIN_ERROR", err.Error())
		return
	}

	// Wait for first QR code
	evt, ok := <-qrChan
	if !ok || evt.Done {
		writeError(w, http.StatusInternalServerError, "LOGIN_ERROR", "login channel closed unexpectedly")
		return
	}

	// Generate QR code image as base64
	qrPNG, err := qrcode.Encode(evt.Code, qrcode.Medium, 256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QR_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"qr_code_base64": base64.StdEncoding.EncodeToString(qrPNG),
		"qr_code_text":   evt.Code,
		"timeout":        60,
	})

	// Continue processing QR events in background (waiting for scan)
	go func() {
		for range qrChan {
			// Drain remaining events
		}
	}()
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.client.Logout(); err != nil {
		writeError(w, http.StatusInternalServerError, "LOGOUT_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.client.Status())
}
```

Note: `github.com/skip2/go-qrcode` is an additional dependency for QR code image generation. Add via `go get github.com/skip2/go-qrcode@latest`. If QR image generation is not desired, the `qr_code_text` field alone is sufficient for terminal-based QR rendering.

- [ ] **Step 2: Verify compilation**

Run: `go get github.com/skip2/go-qrcode@latest && go build ./internal/api/...`
Expected: compiles (handler stubs still missing for other endpoints)

- [ ] **Step 3: Commit**

```bash
git add internal/api/auth.go
git commit -m "feat: add health and auth API handlers"
```

---

### Task 22: API — Message Handlers

**Files:**
- Create: `internal/api/message.go`

- [ ] **Step 1: Implement message handlers**

```go
// internal/api/message.go
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")

	var req models.SendRequest

	if strings.HasPrefix(contentType, "application/json") || contentType == "" {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
			return
		}
	} else if strings.HasPrefix(contentType, "multipart/") {
		// Multipart form: inline upload+send
		if err := r.ParseMultipartForm(100 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
			return
		}
		req.To = r.FormValue("to")
		req.Type = r.FormValue("type")
		req.Content = r.FormValue("content")
		req.Caption = r.FormValue("caption")
		req.Filename = r.FormValue("filename")
		req.MediaID = r.FormValue("media_id")
		req.ContactJID = r.FormValue("contact_jid")
		req.Name = r.FormValue("name")
		if lat := r.FormValue("lat"); lat != "" {
			req.Lat, _ = strconv.ParseFloat(lat, 64)
		}
		if lon := r.FormValue("lon"); lon != "" {
			req.Lon, _ = strconv.ParseFloat(lon, 64)
		}
	} else {
		writeError(w, http.StatusBadRequest, "INVALID_CONTENT_TYPE", "expected application/json or multipart/form-data")
		return
	}

	var resp *models.SendResponse
	var err error

	switch req.Type {
	case "text":
		resp, err = s.client.SendText(req.To, req.Content)
	case "image":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendImage(req.To, data, fname, req.Caption)
	case "video":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendVideo(req.To, data, fname, req.Caption)
	case "audio":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendAudio(req.To, data, fname)
	case "document":
		data, fname, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendDocument(req.To, data, fname)
	case "sticker":
		data, _, mediaErr := s.getMediaData(r, &req)
		if mediaErr != nil {
			writeError(w, http.StatusBadRequest, "MEDIA_ERROR", mediaErr.Error())
			return
		}
		resp, err = s.client.SendSticker(req.To, data)
	case "location":
		resp, err = s.client.SendLocation(req.To, req.Lat, req.Lon, req.Name)
	case "contact":
		resp, err = s.client.SendContact(req.To, req.ContactJID)
	default:
		writeError(w, http.StatusBadRequest, "INVALID_TYPE", "unsupported message type: "+req.Type)
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "SEND_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// getMediaData retrieves media bytes from either a media_id reference or multipart file upload.
func (s *Server) getMediaData(r *http.Request, req *models.SendRequest) ([]byte, string, error) {
	// Two-step: media_id reference
	if req.MediaID != "" {
		upload, err := s.store.GetMediaUpload(req.MediaID)
		if err != nil {
			return nil, "", err
		}
		s.store.DeleteMediaUpload(req.MediaID)
		fname := upload.Filename
		if fname == "" && req.Filename != "" {
			fname = req.Filename
		}
		return upload.Data, fname, nil
	}

	// Inline: multipart file
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, "", err
	}

	fname := header.Filename
	if req.Filename != "" {
		fname = req.Filename
	}
	return data, fname, nil
}

func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	jid := r.URL.Query().Get("jid")
	if jid == "" {
		writeError(w, http.StatusBadRequest, "MISSING_PARAM", "jid query parameter is required")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	var before int64
	if b := r.URL.Query().Get("before"); b != "" {
		if n, err := strconv.ParseInt(b, 10, 64); err == nil {
			before = n
		}
	}

	msgs, err := s.client.GetMessages(jid, limit, before)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}

	cursor := ""
	if len(msgs) > 0 {
		cursor = strconv.FormatInt(msgs[len(msgs)-1].Timestamp, 10)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"messages": msgs,
		"cursor":   cursor,
	})
}

func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	msg, err := s.client.GetMessage(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	forEveryone := r.URL.Query().Get("for_everyone") == "true"

	if err := s.client.DeleteMessage(id, forEveryone); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleReactMessage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Emoji string `json:"emoji"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	if err := s.client.SendReaction(id, body.Emoji); err != nil {
		writeError(w, http.StatusInternalServerError, "REACTION_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.client.MarkRead(id); err != nil {
		writeError(w, http.StatusInternalServerError, "READ_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/api/...`

- [ ] **Step 3: Commit**

```bash
git add internal/api/message.go
git commit -m "feat: add message API handlers"
```

---

### Task 23: API — Group, Contact, Media, Webhook, Event Handlers

**Files:**
- Create: `internal/api/group.go`
- Create: `internal/api/contact.go`
- Create: `internal/api/media.go`
- Create: `internal/api/webhook.go`
- Create: `internal/api/event.go`

- [ ] **Step 1: Implement group handlers**

```go
// internal/api/group.go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		Participants []string `json:"participants"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	group, err := s.client.CreateGroup(body.Name, body.Participants)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "CREATE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.client.GetGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"groups": groups})
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	group, err := s.client.GetGroupInfo(jid)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, group)
}

func (s *Server) handleLeaveGroup(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	if err := s.client.LeaveGroup(jid); err != nil {
		writeError(w, http.StatusInternalServerError, "LEAVE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGetInviteLink(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	link, err := s.client.GetInviteLink(jid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INVITE_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"invite_link": link})
}

func (s *Server) handleJoinGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InviteLink string `json:"invite_link"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	groupJID, err := s.client.JoinGroup(body.InviteLink)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "JOIN_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"group_jid": groupJID})
}

func (s *Server) handleParticipants(w http.ResponseWriter, r *http.Request, action func(string, []string) error) {
	jid := chi.URLParam(r, "jid")
	var body struct {
		JIDs []string `json:"jids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if err := action(jid, body.JIDs); err != nil {
		writeError(w, http.StatusInternalServerError, "PARTICIPANT_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAddParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.AddParticipants)
}
func (s *Server) handleRemoveParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.RemoveParticipants)
}
func (s *Server) handlePromoteParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.PromoteParticipants)
}
func (s *Server) handleDemoteParticipants(w http.ResponseWriter, r *http.Request) {
	s.handleParticipants(w, r, s.client.DemoteParticipants)
}
```

- [ ] **Step 2: Implement contact handlers**

```go
// internal/api/contact.go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	contacts, err := s.client.GetContacts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"contacts": contacts})
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	contact, err := s.client.GetContactInfo(jid)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, contact)
}

func (s *Server) handleBlockContact(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	if err := s.client.BlockContact(jid); err != nil {
		writeError(w, http.StatusInternalServerError, "BLOCK_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleUnblockContact(w http.ResponseWriter, r *http.Request) {
	jid := chi.URLParam(r, "jid")
	if err := s.client.UnblockContact(jid); err != nil {
		writeError(w, http.StatusInternalServerError, "UNBLOCK_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 3: Implement media handlers**

```go
// internal/api/media.go
package api

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Server) handleUploadMedia(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "MISSING_FILE", "file field is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "READ_ERROR", err.Error())
		return
	}

	// Generate media ID
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	mediaID := "med_" + hex.EncodeToString(idBytes)

	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = http.DetectContentType(data)
	}

	upload := &models.MediaUpload{
		ID:        mediaID,
		MimeType:  mime,
		Filename:  header.Filename,
		Size:      int64(len(data)),
		Data:      data,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}

	if err := s.store.InsertMediaUpload(upload); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"media_id": mediaID,
		"type":     mime,
		"mime":     mime,
		"size":     len(data),
	})
}

func (s *Server) handleDownloadMedia(w http.ResponseWriter, r *http.Request) {
	messageID := chi.URLParam(r, "messageId")
	data, mimeType, err := s.client.DownloadMedia(messageID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
```

- [ ] **Step 4: Implement webhook handlers**

```go
// internal/api/webhook.go
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	whID := "wh_" + hex.EncodeToString(idBytes)

	wh := &models.Webhook{
		ID:     whID,
		URL:    body.URL,
		Events: body.Events,
		Secret: body.Secret,
	}

	if err := s.store.InsertWebhook(wh); err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	// Register with dispatcher
	s.dispatcher.Register(*wh)

	writeJSON(w, http.StatusOK, wh)
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := s.store.GetWebhooks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"webhooks": webhooks})
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.DeleteWebhook(id); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	s.dispatcher.Unregister(id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
```

- [ ] **Step 5: Implement event polling handler**

```go
// internal/api/event.go
package api

import (
	"net/http"
	"strconv"
)

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	after := int64(0)
	if a := r.URL.Query().Get("after"); a != "" {
		if n, err := strconv.ParseInt(a, 10, 64); err == nil {
			after = n
		}
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	events, err := s.store.GetEvents(after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QUERY_ERROR", err.Error())
		return
	}

	cursor := ""
	if len(events) > 0 {
		cursor = strconv.FormatInt(events[len(events)-1].ID, 10)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"events": events,
		"cursor": cursor,
	})
}
```

- [ ] **Step 6: Verify compilation**

Run: `go build ./internal/api/...`
Expected: compiles

- [ ] **Step 7: Commit**

```bash
git add internal/api/group.go internal/api/contact.go internal/api/media.go internal/api/webhook.go internal/api/event.go
git commit -m "feat: add group, contact, media, webhook, event API handlers"
```

---

## Chunk 5: CLI Commands + Server Lifecycle

### Task 24: CLI Root Command and Global Flags

**Files:**
- Create: `cmd/wa/main.go`
- Create: `cmd/wa/root.go`

- [ ] **Step 1: Create main.go entrypoint**

```go
// cmd/wa/main.go
package main

import "os"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Create root command with global flags**

```go
// cmd/wa/root.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/piwi3910/whatsapp-go/internal/config"
)

var (
	outputFormat string // "json" or "" (human-readable)
	configPath   string
	dbPath       string
	cfg          *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "wa",
	Short: "WhatsApp CLI & API tool",
	Long:  "wa is a command-line tool and REST API server for WhatsApp.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for help
		if cmd.Name() == "help" {
			return nil
		}

		if configPath == "" {
			configPath = filepath.Join(config.Dir(), "config.yaml")
		}

		var err error
		cfg, err = config.Load(configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		// Override DB path if flag set
		if dbPath != "" {
			cfg.Database.Path = dbPath
		}

		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&outputFormat, "output", "", "output format: json")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "config file path")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database file path")
}

// printOutput prints data as pretty-printed JSON. Individual commands provide
// their own human-readable formatting when --output is not "json".
func printOutput(data any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

// printError prints an error message and exits.
func exitError(msg string, code int) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	os.Exit(code)
}
```

- [ ] **Step 3: Verify it builds**

Run: `go build -o wa ./cmd/wa/`
Expected: compiles and produces `wa` binary

- [ ] **Step 4: Commit**

```bash
git add cmd/wa/main.go cmd/wa/root.go
git commit -m "feat: add CLI root command with global flags"
```

---

### Task 25: CLI Auth Commands

**Files:**
- Create: `cmd/wa/auth.go`

- [ ] **Step 1: Implement auth commands**

```go
// cmd/wa/auth.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/store"
)

// newClient creates a Client for CLI use (temporary connection).
func newClient() (*client.Client, *store.Store, func()) {
	s, err := store.New(cfg.Database.Path)
	if err != nil {
		exitError(fmt.Sprintf("opening database: %v", err), 1)
	}

	log := waLog.Stdout("wa", "WARN", true)
	c, err := client.New(s, cfg.Database.Path, log)
	if err != nil {
		s.Close()
		exitError(fmt.Sprintf("creating client: %v", err), 1)
	}

	cleanup := func() {
		c.Disconnect()
		s.Close()
	}
	return c, s, cleanup
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Link a WhatsApp device via QR code",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		qrChan, err := c.Login()
		if err != nil {
			exitError(err.Error(), 2)
		}

		for evt := range qrChan {
			if evt.Done {
				fmt.Println("Login successful!")
				return
			}
			// Print QR code text for terminal rendering
			fmt.Printf("Scan this QR code with WhatsApp:\n%s\n\n", evt.Code)
		}
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Unlink the WhatsApp device",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		if err := c.Connect(); err != nil {
			exitError(err.Error(), 2)
		}

		if err := c.Logout(); err != nil {
			exitError(err.Error(), 2)
		}
		fmt.Println("Logged out successfully.")
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication and connection status",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		status := c.Status()
		if outputFormat == "json" {
			printOutput(status)
		} else {
			fmt.Printf("State: %s\n", status.State)
			if status.PhoneNumber != "" {
				fmt.Printf("Phone: %s\n", status.PhoneNumber)
			}
			if status.PushName != "" {
				fmt.Printf("Name:  %s\n", status.PushName)
			}
		}
	},
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication commands",
}

func init() {
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(loginCmd, logoutCmd, authCmd)
}
```

Note: The `newClient` helper creates a temporary connection for CLI one-off use. When a server is running, this should auto-proxy through the REST API instead — that logic is added in Task 30 (CLI-server proxy).

- [ ] **Step 2: Verify it builds**

Run: `go build -o wa ./cmd/wa/`

- [ ] **Step 3: Commit**

```bash
git add cmd/wa/auth.go
git commit -m "feat: add CLI auth commands (login, logout, status)"
```

---

### Task 26: CLI Send Commands

**Files:**
- Create: `cmd/wa/send.go`

- [ ] **Step 1: Implement send commands**

```go
// cmd/wa/send.go
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

var sendCaption string
var sendFilename string
var sendName string

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send messages",
}

var sendTextCmd = &cobra.Command{
	Use:   "text <jid> <message>",
	Short: "Send a text message",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		text := args[1]
		if text == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				exitError(fmt.Sprintf("reading stdin: %v", err), 1)
			}
			text = string(data)
		}

		resp, err := c.SendText(args[0], text)
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(resp)
	},
}

func makeSendMediaCmd(use, short, msgType string, needsCaption bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			c, _, cleanup := newClient()
			defer cleanup()
			if err := c.Connect(); err != nil {
				exitError(err.Error(), 1)
			}

			data, err := os.ReadFile(args[1])
			if err != nil {
				exitError(fmt.Sprintf("reading file: %v", err), 1)
			}

			fname := args[1]
			if sendFilename != "" {
				fname = sendFilename
			}

			var resp interface{}
			switch msgType {
			case "image":
				resp, err = c.SendImage(args[0], data, fname, sendCaption)
			case "video":
				resp, err = c.SendVideo(args[0], data, fname, sendCaption)
			case "audio":
				resp, err = c.SendAudio(args[0], data, fname)
			case "document":
				resp, err = c.SendDocument(args[0], data, fname)
			case "sticker":
				resp, err = c.SendSticker(args[0], data)
			}
			if err != nil {
				exitError(err.Error(), 1)
			}
			printOutput(resp)
		},
	}
	if needsCaption {
		cmd.Flags().StringVarP(&sendCaption, "caption", "c", "", "media caption")
	}
	return cmd
}

var sendLocationCmd = &cobra.Command{
	Use:   "location <jid> <lat> <lon>",
	Short: "Send a location",
	Args:  cobra.ExactArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		lat, _ := strconv.ParseFloat(args[1], 64)
		lon, _ := strconv.ParseFloat(args[2], 64)
		resp, err := c.SendLocation(args[0], lat, lon, sendName)
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(resp)
	},
}

var sendContactCmd = &cobra.Command{
	Use:   "contact <jid> <contact-jid>",
	Short: "Send a contact card",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		resp, err := c.SendContact(args[0], args[1])
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(resp)
	},
}

var sendReactionCmd = &cobra.Command{
	Use:   "reaction <message-id> <emoji>",
	Short: "React to a message (sender looked up from local DB)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		if err := c.SendReaction(args[0], args[1]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Reaction sent.")
	},
}

func init() {
	sendLocationCmd.Flags().StringVarP(&sendName, "name", "n", "", "location name")

	sendCmd.AddCommand(
		sendTextCmd,
		makeSendMediaCmd("image <jid> <file>", "Send an image", "image", true),
		makeSendMediaCmd("video <jid> <file>", "Send a video", "video", true),
		makeSendMediaCmd("audio <jid> <file>", "Send audio", "audio", false),
		makeSendMediaCmd("document <jid> <file>", "Send a document", "document", false),
		makeSendMediaCmd("sticker <jid> <file>", "Send a sticker", "sticker", false),
		sendLocationCmd,
		sendContactCmd,
		sendReactionCmd,
	)
	rootCmd.AddCommand(sendCmd)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build -o wa ./cmd/wa/`

- [ ] **Step 3: Commit**

```bash
git add cmd/wa/send.go
git commit -m "feat: add CLI send commands (text, media, location, contact, reaction)"
```

---

### Task 27: CLI Message Commands

**Files:**
- Create: `cmd/wa/message.go`

- [ ] **Step 1: Implement message list/info/delete commands**

```go
// cmd/wa/message.go
package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

var msgLimit int
var msgBefore string
var msgForEveryone bool

var messageCmd = &cobra.Command{
	Use:   "message",
	Short: "Message operations",
}

var messageListCmd = &cobra.Command{
	Use:   "list <jid>",
	Short: "List messages in a chat",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		var before int64
		if msgBefore != "" {
			before, _ = strconv.ParseInt(msgBefore, 10, 64)
		}

		msgs, err := c.GetMessages(args[0], msgLimit, before)
		if err != nil {
			exitError(err.Error(), 1)
		}

		if outputFormat == "json" {
			printOutput(msgs)
		} else {
			for _, m := range msgs {
				ts := time.Unix(m.Timestamp, 0).Format("2006-01-02 15:04:05")
				direction := "<-"
				if m.IsFromMe {
					direction = "->"
				}
				fmt.Printf("[%s] %s %s: %s\n", ts, direction, m.ID[:8], m.Content)
			}
		}
	},
}

var messageInfoCmd = &cobra.Command{
	Use:   "info <message-id>",
	Short: "Show message details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		msg, err := c.GetMessage(args[0])
		if err != nil {
			exitError(err.Error(), 3)
		}
		printOutput(msg)
	},
}

var messageDeleteCmd = &cobra.Command{
	Use:   "delete <jid> <message-id>",
	Short: "Delete a message",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		if err := c.DeleteMessage(args[1], msgForEveryone); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Message deleted.")
	},
}

func init() {
	messageListCmd.Flags().IntVar(&msgLimit, "limit", 20, "maximum messages to return")
	messageListCmd.Flags().StringVar(&msgBefore, "before", "", "return messages before this timestamp")
	messageDeleteCmd.Flags().BoolVar(&msgForEveryone, "for-everyone", false, "delete for everyone")

	messageCmd.AddCommand(messageListCmd, messageInfoCmd, messageDeleteCmd)
	rootCmd.AddCommand(messageCmd)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build -o wa ./cmd/wa/`

- [ ] **Step 3: Commit**

```bash
git add cmd/wa/message.go
git commit -m "feat: add CLI message commands (list, info, delete)"
```

---

### Task 28: CLI Group and Contact Commands

**Files:**
- Create: `cmd/wa/group.go`
- Create: `cmd/wa/contact.go`

- [ ] **Step 1: Implement group commands**

```go
// cmd/wa/group.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var groupCmd = &cobra.Command{
	Use:   "group",
	Short: "Group operations",
}

var groupCreateCmd = &cobra.Command{
	Use:   "create <name> <jid>...",
	Short: "Create a group",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		group, err := c.CreateGroup(args[0], args[1:])
		if err != nil {
			exitError(err.Error(), 1)
		}
		printOutput(group)
	},
}

var groupListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all groups",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		groups, err := c.GetGroups()
		if err != nil {
			exitError(err.Error(), 1)
		}
		if outputFormat == "json" {
			printOutput(groups)
		} else {
			for _, g := range groups {
				fmt.Printf("%s  %s  (%d members)\n", g.JID, g.Name, len(g.Participants))
			}
		}
	},
}

var groupInfoCmd = &cobra.Command{
	Use:   "info <group-jid>",
	Short: "Show group info",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		group, err := c.GetGroupInfo(args[0])
		if err != nil {
			exitError(err.Error(), 3)
		}
		printOutput(group)
	},
}

var groupJoinCmd = &cobra.Command{
	Use:   "join <invite-link>",
	Short: "Join a group via invite link",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		jid, err := c.JoinGroup(args[0])
		if err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Printf("Joined group: %s\n", jid)
	},
}

var groupLeaveCmd = &cobra.Command{
	Use:   "leave <group-jid>",
	Short: "Leave a group",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.LeaveGroup(args[0]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Left group.")
	},
}

var groupInviteCmd = &cobra.Command{
	Use:   "invite <group-jid>",
	Short: "Get group invite link",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		link, err := c.GetInviteLink(args[0])
		if err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println(link)
	},
}

func makeParticipantCmd(use, short string, action func(client.Service, string, []string) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			c, _, cleanup := newClient()
			defer cleanup()
			if err := c.Connect(); err != nil {
				exitError(err.Error(), 1)
			}
			if err := action(c, args[0], args[1:]); err != nil {
				exitError(err.Error(), 1)
			}
			fmt.Println("Done.")
		},
	}
}

func init() {
	groupCmd.AddCommand(
		groupCreateCmd, groupListCmd, groupInfoCmd,
		groupJoinCmd, groupLeaveCmd, groupInviteCmd,
		makeParticipantCmd("add <group-jid> <jid>...", "Add participants", func(c client.Service, gj string, p []string) error { return c.AddParticipants(gj, p) }),
		makeParticipantCmd("remove <group-jid> <jid>...", "Remove participants", func(c client.Service, gj string, p []string) error { return c.RemoveParticipants(gj, p) }),
		makeParticipantCmd("promote <group-jid> <jid>...", "Promote to admin", func(c client.Service, gj string, p []string) error { return c.PromoteParticipants(gj, p) }),
		makeParticipantCmd("demote <group-jid> <jid>...", "Demote from admin", func(c client.Service, gj string, p []string) error { return c.DemoteParticipants(gj, p) }),
	)
	rootCmd.AddCommand(groupCmd)
}
```

Note: The `makeParticipantCmd` function references `*client.Client` directly. Since the CLI package is `main`, this needs the import `"github.com/piwi3910/whatsapp-go/internal/client"`. The `newClient()` function already returns `*client.Client`.

- [ ] **Step 2: Implement contact commands**

```go
// cmd/wa/contact.go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var contactCmd = &cobra.Command{
	Use:   "contact",
	Short: "Contact operations",
}

var contactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List contacts",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		contacts, err := c.GetContacts()
		if err != nil {
			exitError(err.Error(), 1)
		}
		if outputFormat == "json" {
			printOutput(contacts)
		} else {
			for _, ct := range contacts {
				name := ct.Name
				if name == "" {
					name = ct.PushName
				}
				fmt.Printf("%s  %s\n", ct.JID, name)
			}
		}
	},
}

var contactInfoCmd = &cobra.Command{
	Use:   "info <jid>",
	Short: "Show contact info",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		contact, err := c.GetContactInfo(args[0])
		if err != nil {
			exitError(err.Error(), 3)
		}
		printOutput(contact)
	},
}

var contactBlockCmd = &cobra.Command{
	Use:   "block <jid>",
	Short: "Block a contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.BlockContact(args[0]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Contact blocked.")
	},
}

var contactUnblockCmd = &cobra.Command{
	Use:   "unblock <jid>",
	Short: "Unblock a contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}
		if err := c.UnblockContact(args[0]); err != nil {
			exitError(err.Error(), 1)
		}
		fmt.Println("Contact unblocked.")
	},
}

func init() {
	contactCmd.AddCommand(contactListCmd, contactInfoCmd, contactBlockCmd, contactUnblockCmd)
	rootCmd.AddCommand(contactCmd)
}
```

- [ ] **Step 3: Verify it builds**

Run: `go build -o wa ./cmd/wa/`

- [ ] **Step 4: Commit**

```bash
git add cmd/wa/group.go cmd/wa/contact.go
git commit -m "feat: add CLI group and contact commands"
```

---

### Task 29: CLI Media Download, Event Listen Commands

**Files:**
- Create: `cmd/wa/media.go`
- Create: `cmd/wa/event.go`

- [ ] **Step 1: Implement media download command**

```go
// cmd/wa/media.go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var mediaOutput string

var mediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Media operations",
}

var mediaDownloadCmd = &cobra.Command{
	Use:   "download <message-id>",
	Short: "Download media from a message",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		data, mimeType, err := c.DownloadMedia(args[0])
		if err != nil {
			exitError(err.Error(), 1)
		}

		outPath := mediaOutput
		if outPath == "" {
			// Default: message-id with extension based on MIME
			ext := mimeToExt(mimeType)
			outPath = args[0] + ext
		}

		if err := os.WriteFile(outPath, data, 0644); err != nil {
			exitError(fmt.Sprintf("writing file: %v", err), 1)
		}
		fmt.Printf("Downloaded to %s (%d bytes)\n", outPath, len(data))
	},
}

func mimeToExt(mime string) string {
	m := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif",
		"image/webp": ".webp", "video/mp4": ".mp4", "audio/mpeg": ".mp3",
		"audio/ogg": ".ogg", "audio/mp4": ".m4a", "application/pdf": ".pdf",
	}
	if ext, ok := m[mime]; ok {
		return ext
	}
	return ".bin"
}

func init() {
	mediaDownloadCmd.Flags().StringVarP(&mediaOutput, "output", "o", "", "output file path")
	mediaCmd.AddCommand(mediaDownloadCmd)
	rootCmd.AddCommand(mediaCmd)
}
```

- [ ] **Step 2: Implement event listen command**

```go
// cmd/wa/event.go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

var eventTypes string

var eventCmd = &cobra.Command{
	Use:   "event",
	Short: "Event operations",
}

var eventListenCmd = &cobra.Command{
	Use:   "listen",
	Short: "Stream events as NDJSON to stdout",
	Run: func(cmd *cobra.Command, args []string) {
		c, _, cleanup := newClient()
		defer cleanup()

		// Parse type filter
		var typeFilter map[string]bool
		if eventTypes != "" {
			typeFilter = make(map[string]bool)
			for _, t := range strings.Split(eventTypes, ",") {
				typeFilter[strings.TrimSpace(t)] = true
			}
		}

		c.RegisterEventHandler(func(evt models.Event) {
			if typeFilter != nil && !typeFilter[evt.Type] {
				return
			}
			line, _ := json.Marshal(evt)
			fmt.Println(string(line))
		})

		c.SetupEventHandlers()
		if err := c.Connect(); err != nil {
			exitError(err.Error(), 1)
		}

		fmt.Fprintln(os.Stderr, "Listening for events... (Ctrl+C to stop)")

		// Wait for interrupt
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "\nStopping.")
	},
}

func init() {
	eventListenCmd.Flags().StringVar(&eventTypes, "types", "", "comma-separated event types to filter")
	eventCmd.AddCommand(eventListenCmd)
	rootCmd.AddCommand(eventCmd)
}
```

- [ ] **Step 3: Verify it builds**

Run: `go build -o wa ./cmd/wa/`

- [ ] **Step 4: Commit**

```bash
git add cmd/wa/media.go cmd/wa/event.go
git commit -m "feat: add CLI media download and event listen commands"
```

---

### Task 30: CLI Serve Command + Server Lifecycle

**Files:**
- Create: `cmd/wa/serve.go`

- [ ] **Step 1: Implement serve command with full server lifecycle**

```go
// cmd/wa/serve.go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/piwi3910/whatsapp-go/internal/api"
	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/config"
	"github.com/piwi3910/whatsapp-go/internal/models"
	"github.com/piwi3910/whatsapp-go/internal/pidfile"
	"github.com/piwi3910/whatsapp-go/internal/store"
	"github.com/piwi3910/whatsapp-go/internal/webhook"
)

var servePort int
var serveHost string
var serveAPIKey string

const version = "0.1.0"

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the REST API server",
	Run: func(cmd *cobra.Command, args []string) {
		// Apply flag overrides
		if serveHost != "" {
			cfg.Server.Host = serveHost
		}
		if servePort != 0 {
			cfg.Server.Port = servePort
		}
		if serveAPIKey != "" {
			cfg.APIKey = serveAPIKey
		}

		// Generate API key if not set
		if cfg.APIKey == "" {
			cfg.APIKey = config.GenerateAPIKey()
			log.Printf("Generated API key: %s", cfg.APIKey)
			// Save to config
			if configPath != "" {
				config.Save(configPath, cfg)
			}
		}

		// Check PID file
		pidPath := filepath.Join(config.Dir(), "wa.pid")
		if pidfile.IsRunning(pidPath) {
			exitError("another server instance is already running", 1)
		}

		// Open store
		s, err := store.New(cfg.Database.Path)
		if err != nil {
			exitError(fmt.Sprintf("opening database: %v", err), 1)
		}
		defer s.Close()

		// Create client
		waLogger := waLog.Stdout("wa", "WARN", true)
		c, err := client.New(s, cfg.Database.Path, waLogger)
		if err != nil {
			exitError(fmt.Sprintf("creating client: %v", err), 1)
		}

		// Setup event handlers
		disp := webhook.New()

		// Load webhooks from store
		webhooks, _ := s.GetWebhooks()
		for _, wh := range webhooks {
			disp.Register(wh)
		}

		// Load webhooks from config
		for i, wh := range cfg.Webhooks {
			hook := models.Webhook{
				ID:     fmt.Sprintf("cfg_%d", i),
				URL:    wh.URL,
				Events: wh.Events,
				Secret: wh.Secret,
			}
			disp.Register(hook)
		}

		// Register event handler that dispatches to webhooks
		c.RegisterEventHandler(func(evt models.Event) {
			disp.Dispatch(evt)
		})

		c.SetupEventHandlers()

		// Connect to WhatsApp (if previously logged in)
		if err := c.Connect(); err != nil {
			log.Printf("WhatsApp connection: %v (login may be needed)", err)
		}

		// Write PID file
		if err := pidfile.Write(pidPath); err != nil {
			log.Printf("Warning: could not write PID file: %v", err)
		}
		defer pidfile.Remove(pidPath)

		// Start media upload pruning goroutine
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				if n, err := s.PruneExpiredUploads(); err != nil {
					log.Printf("prune uploads: %v", err)
				} else if n > 0 {
					log.Printf("pruned %d expired media uploads", n)
				}
			}
		}()

		// Start event pruning goroutine
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				if err := s.PruneEvents(cfg.Events.MaxBuffer); err != nil {
					log.Printf("prune events: %v", err)
				}
			}
		}()

		// Create and start API server
		srv := api.NewServer(c, s, disp, cfg.APIKey, version, cfg.Server.MaxUploadSize)

		// Graceful shutdown
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			log.Println("Shutting down...")
			srv.Stop()
			c.Disconnect()
		}()

		log.Printf("API key: %s", cfg.APIKey)
		if err := srv.Start(cfg.Server.Host, cfg.Server.Port); err != nil {
			log.Printf("Server stopped: %v", err)
		}
	},
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "server port (overrides config)")
	serveCmd.Flags().StringVar(&serveHost, "host", "", "server host (overrides config)")
	serveCmd.Flags().StringVar(&serveAPIKey, "api-key", "", "API key (overrides config)")
	rootCmd.AddCommand(serveCmd)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build -o wa ./cmd/wa/`
Expected: compiles

- [ ] **Step 3: Run `./wa --help` to verify command tree**

Run: `./wa --help`
Expected: shows all commands (login, logout, auth, send, message, group, contact, media, event, serve)

- [ ] **Step 4: Commit**

```bash
git add cmd/wa/serve.go
git commit -m "feat: add serve command with full server lifecycle"
```

---

### Task 31: CLI-Server Proxy

**Files:**
- Modify: `cmd/wa/auth.go` (update `newClient` to detect running server)

- [ ] **Step 1: Add HTTP proxy client that implements the Service interface**

Create a lightweight HTTP client in `cmd/wa/proxy.go` that implements `client.Service` by forwarding calls to the REST API:

```go
// cmd/wa/proxy.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/piwi3910/whatsapp-go/internal/client"
	"github.com/piwi3910/whatsapp-go/internal/models"
)

// proxyClient implements client.Service by forwarding to the REST API.
type proxyClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newProxyClient(baseURL, apiKey string) *proxyClient {
	return &proxyClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{},
	}
}

// Note: this file needs imports for: bytes, encoding/json, fmt, io, mime/multipart, net/http
// plus: github.com/piwi3910/whatsapp-go/internal/client
// plus: github.com/piwi3910/whatsapp-go/internal/models

func (p *proxyClient) do(method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, p.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return p.http.Do(req)
}

func (p *proxyClient) decodeResponse(resp *http.Response, target any) error {
	defer resp.Body.Close()
	var apiResp models.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return err
	}
	if !apiResp.OK {
		return fmt.Errorf("%s: %s", apiResp.Error.Code, apiResp.Error.Message)
	}
	if target != nil {
		data, _ := json.Marshal(apiResp.Data)
		return json.Unmarshal(data, target)
	}
	return nil
}

// Implement Service interface methods by delegating to REST API.
// Each method makes an HTTP request and decodes the response.

func (p *proxyClient) Connect() error                  { return nil } // server is already connected
func (p *proxyClient) Disconnect()                     {}
func (p *proxyClient) IsConnected() bool               { return true }

func (p *proxyClient) Status() client.ConnectionStatus {
	resp, err := p.do("GET", "/api/v1/auth/status", nil)
	if err != nil {
		return client.ConnectionStatus{State: "error"}
	}
	var status client.ConnectionStatus
	p.decodeResponse(resp, &status)
	return status
}

func (p *proxyClient) Login() (<-chan client.QREvent, error) {
	return nil, fmt.Errorf("login must be done directly, not through proxy")
}

func (p *proxyClient) Logout() error {
	resp, err := p.do("POST", "/api/v1/auth/logout", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}

func (p *proxyClient) SendText(jid, text string) (*models.SendResponse, error) {
	resp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "text", Content: text,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}

// sendMedia uploads media via the two-step flow (upload + send with media_id).
func (p *proxyClient) sendMedia(jid string, data []byte, filename, caption, msgType string) (*models.SendResponse, error) {
	// Step 1: Upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	part.Write(data)
	writer.Close()

	req, err := http.NewRequest("POST", p.baseURL+"/api/v1/media/upload", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	var uploadResult struct {
		MediaID string `json:"media_id"`
	}
	if err := p.decodeResponse(uploadResp, &uploadResult); err != nil {
		return nil, err
	}

	// Step 2: Send with media_id
	sendResp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: msgType, MediaID: uploadResult.MediaID, Caption: caption, Filename: filename,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(sendResp, &result)
}

func (p *proxyClient) SendImage(jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, caption, "image")
}
func (p *proxyClient) SendVideo(jid string, data []byte, filename, caption string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, caption, "video")
}
func (p *proxyClient) SendAudio(jid string, data []byte, filename string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, "", "audio")
}
func (p *proxyClient) SendDocument(jid string, data []byte, filename string) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, filename, "", "document")
}
func (p *proxyClient) SendSticker(jid string, data []byte) (*models.SendResponse, error) {
	return p.sendMedia(jid, data, "sticker.webp", "", "sticker")
}
func (p *proxyClient) SendLocation(jid string, lat, lon float64, name string) (*models.SendResponse, error) {
	resp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "location", Lat: lat, Lon: lon, Name: name,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}
func (p *proxyClient) SendContact(jid, contactJID string) (*models.SendResponse, error) {
	resp, err := p.do("POST", "/api/v1/messages/send", models.SendRequest{
		To: jid, Type: "contact", ContactJID: contactJID,
	})
	if err != nil {
		return nil, err
	}
	var result models.SendResponse
	return &result, p.decodeResponse(resp, &result)
}
func (p *proxyClient) SendReaction(messageID, emoji string) error {
	resp, err := p.do("POST", fmt.Sprintf("/api/v1/messages/%s/react", messageID), map[string]string{"emoji": emoji})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DeleteMessage(messageID string, forEveryone bool) error {
	fe := ""
	if forEveryone {
		fe = "?for_everyone=true"
	}
	resp, err := p.do("DELETE", fmt.Sprintf("/api/v1/messages/%s%s", messageID, fe), nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) MarkRead(messageID string) error {
	resp, err := p.do("POST", fmt.Sprintf("/api/v1/messages/%s/read", messageID), nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetMessages(chatJID string, limit int, before int64) ([]models.Message, error) {
	path := fmt.Sprintf("/api/v1/messages?jid=%s&limit=%d", chatJID, limit)
	if before > 0 {
		path += fmt.Sprintf("&before=%d", before)
	}
	resp, err := p.do("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result struct{ Messages []models.Message }
	return result.Messages, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetMessage(messageID string) (*models.Message, error) {
	resp, err := p.do("GET", "/api/v1/messages/"+messageID, nil)
	if err != nil {
		return nil, err
	}
	var msg models.Message
	return &msg, p.decodeResponse(resp, &msg)
}
func (p *proxyClient) CreateGroup(name string, participants []string) (*models.Group, error) {
	resp, err := p.do("POST", "/api/v1/groups", map[string]interface{}{"name": name, "participants": participants})
	if err != nil {
		return nil, err
	}
	var group models.Group
	return &group, p.decodeResponse(resp, &group)
}
func (p *proxyClient) GetGroups() ([]models.Group, error) {
	resp, err := p.do("GET", "/api/v1/groups", nil)
	if err != nil {
		return nil, err
	}
	var result struct{ Groups []models.Group }
	return result.Groups, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetGroupInfo(groupJID string) (*models.Group, error) {
	resp, err := p.do("GET", "/api/v1/groups/"+groupJID, nil)
	if err != nil {
		return nil, err
	}
	var group models.Group
	return &group, p.decodeResponse(resp, &group)
}
func (p *proxyClient) JoinGroup(inviteLink string) (string, error) {
	resp, err := p.do("POST", "/api/v1/groups/join", map[string]string{"invite_link": inviteLink})
	if err != nil {
		return "", err
	}
	var result struct{ GroupJID string `json:"group_jid"` }
	return result.GroupJID, p.decodeResponse(resp, &result)
}
func (p *proxyClient) LeaveGroup(groupJID string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/leave", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetInviteLink(groupJID string) (string, error) {
	resp, err := p.do("GET", "/api/v1/groups/"+groupJID+"/invite-link", nil)
	if err != nil {
		return "", err
	}
	var result struct{ InviteLink string `json:"invite_link"` }
	return result.InviteLink, p.decodeResponse(resp, &result)
}
func (p *proxyClient) AddParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/add", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) RemoveParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/remove", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) PromoteParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/promote", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DemoteParticipants(groupJID string, participants []string) error {
	resp, err := p.do("POST", "/api/v1/groups/"+groupJID+"/participants/demote", map[string][]string{"jids": participants})
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) GetContacts() ([]models.Contact, error) {
	resp, err := p.do("GET", "/api/v1/contacts", nil)
	if err != nil {
		return nil, err
	}
	var result struct{ Contacts []models.Contact }
	return result.Contacts, p.decodeResponse(resp, &result)
}
func (p *proxyClient) GetContactInfo(jid string) (*models.Contact, error) {
	resp, err := p.do("GET", "/api/v1/contacts/"+jid, nil)
	if err != nil {
		return nil, err
	}
	var contact models.Contact
	return &contact, p.decodeResponse(resp, &contact)
}
func (p *proxyClient) BlockContact(jid string) error {
	resp, err := p.do("POST", "/api/v1/contacts/"+jid+"/block", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) UnblockContact(jid string) error {
	resp, err := p.do("POST", "/api/v1/contacts/"+jid+"/unblock", nil)
	if err != nil {
		return err
	}
	return p.decodeResponse(resp, nil)
}
func (p *proxyClient) DownloadMedia(messageID string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", p.baseURL+"/api/v1/media/"+messageID, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("download failed: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}
func (p *proxyClient) RegisterEventHandler(handler func(models.Event)) {
	// Not supported via proxy — events come through polling
}
func (p *proxyClient) SetupEventHandlers() {
	// No-op for proxy — server handles event setup
}
```

- [ ] **Step 2: Update newClient to detect running server and auto-proxy**

Modify `cmd/wa/auth.go` — replace the `newClient` function:

```go
// newClient creates a client for CLI use. If a server is running (detected via
// PID file), returns a proxy client that forwards through the REST API.
// Otherwise creates a direct whatsmeow connection.
func newClient() (client.Service, *store.Store, func()) {
	pidPath := filepath.Join(config.Dir(), "wa.pid")
	serverAddr := pidfile.ServerAddress(pidPath, cfg.Server.Host, cfg.Server.Port)

	if serverAddr != "" && cfg.APIKey != "" {
		// Server is running — proxy through REST API
		proxy := newProxyClient(serverAddr, cfg.APIKey)
		return proxy, nil, func() {} // no cleanup needed
	}

	// No server running — direct connection
	s, err := store.New(cfg.Database.Path)
	if err != nil {
		exitError(fmt.Sprintf("opening database: %v", err), 1)
	}

	log := waLog.Stdout("wa", "WARN", true)
	c, err := client.New(s, cfg.Database.Path, log)
	if err != nil {
		s.Close()
		exitError(fmt.Sprintf("creating client: %v", err), 1)
	}

	cleanup := func() {
		c.Disconnect()
		s.Close()
	}
	return c, s, cleanup
}
```

Note: The return type changes from `*client.Client` to `client.Service`. All CLI commands that call methods on the client already go through the interface, so this is transparent. The `newClient` function in `auth.go` needs to import `"github.com/piwi3910/whatsapp-go/internal/pidfile"` and `"path/filepath"`.

- [ ] **Step 3: Verify it builds**

Run: `go build -o wa ./cmd/wa/`

- [ ] **Step 4: Commit**

```bash
git add cmd/wa/proxy.go cmd/wa/auth.go
git commit -m "feat: add CLI-server proxy for auto-forwarding when server is running"
```

---

### Task 32: Final Build Verification

- [ ] **Step 1: Run all tests**

Run: `go test ./... -v`
Expected: all store, jid, pidfile, webhook tests pass

- [ ] **Step 2: Build the binary**

Run: `go build -o wa ./cmd/wa/`
Expected: clean build

- [ ] **Step 3: Verify command tree**

Run: `./wa --help`
Expected: shows all subcommands

Run: `./wa send --help`
Expected: shows send subcommands (text, image, video, audio, document, sticker, location, contact, reaction)

Run: `./wa serve --help`
Expected: shows --port, --host, --api-key flags

- [ ] **Step 4: Clean up binary**

Run: `rm -f wa`

- [ ] **Step 5: Final commit**

```bash
git add .
git commit -m "feat: complete wa CLI and API implementation"
```

