package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}
	// busy_timeout is essential, not tuning: WAL allows a single writer, and
	// writes arrive from two directions at once — the whatsmeow event
	// goroutine persisting inbound messages, and HTTP/library callers. With
	// no busy timeout SQLite returns SQLITE_BUSY immediately on that overlap,
	// which previously surfaced as silently dropped inbound messages.
	// auto_vacuum(incremental) lets ReclaimFreeSpace recover the space freed
	// by pruned media blobs with a cheap incremental pass instead of a full
	// VACUUM (which rewrites the whole file and needs a second copy of it on
	// disk). It only takes effect when the database is *created*; an
	// existing file keeps its current mode and falls back to the gated full
	// VACUUM, so this is safe to add to a deployed system.
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)&_pragma=auto_vacuum(incremental)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// One connection: database/sql would otherwise open several, and
	// concurrent writers on one SQLite file contend even with WAL. Serializing
	// here costs little (writes are small and local) and removes the whole
	// class of SQLITE_BUSY failures. Note busy_timeout still matters — the
	// whatsmeow device store is a separate connection to a separate file.
	db.SetMaxOpenConns(1)
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

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) migrate() error {
	if err := s.createTables(); err != nil {
		return err
	}
	return s.migrateEventDedupe()
}

func (s *Store) createTables() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS messages (
	id          TEXT PRIMARY KEY NOT NULL,
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
);

CREATE INDEX IF NOT EXISTS idx_messages_chat_ts
	ON messages (chat_jid, timestamp DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_identity
	ON messages (chat_jid, sender_jid, wa_id);

CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	type       TEXT NOT NULL,
	payload    TEXT NOT NULL,
	timestamp  INTEGER NOT NULL,
	created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS media_uploads (
	id         TEXT PRIMARY KEY,
	data       BLOB NOT NULL,
	mime_type  TEXT NOT NULL,
	filename   TEXT,
	size       INTEGER NOT NULL,
	created_at INTEGER NOT NULL DEFAULT (unixepoch()),
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS webhooks (
	id         TEXT PRIMARY KEY,
	url        TEXT NOT NULL,
	events     TEXT NOT NULL,
	secret     TEXT,
	created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY NOT NULL,
	value TEXT NOT NULL
);
`)
	return err
}

// migrateEventDedupe adds the events.dedupe_key column and its UNIQUE
// index to databases created before event deduplication existed.
//
// It runs on every open and must be safe on a database that already holds
// duplicate events: the backfill assigns the key to the FIRST row of each
// duplicate group only and leaves the later copies NULL, so creating the
// UNIQUE index can never fail on existing data (SQLite allows any number of
// NULLs in a unique index). No row is ever deleted or rewritten.
func (s *Store) migrateEventDedupe() error {
	has, err := s.columnExists("events", "dedupe_key")
	if err != nil {
		return err
	}
	if !has {
		if _, err := s.db.Exec(`ALTER TABLE events ADD COLUMN dedupe_key TEXT`); err != nil {
			return fmt.Errorf("adding events.dedupe_key: %w", err)
		}
	}

	// Backfill the natural key (event type + composite message ID from the
	// payload) for message events, first occurrence wins.
	if _, err := s.db.Exec(`
UPDATE events AS e
SET dedupe_key = e.type || ':' || json_extract(e.payload, '$.id')
WHERE e.dedupe_key IS NULL
  AND e.type IN ('message.received', 'message.sent', 'message.reaction')
  AND json_valid(e.payload)
  AND json_extract(e.payload, '$.id') IS NOT NULL
  -- first occurrence of each duplicate group only; later copies keep a
  -- NULL key, which the UNIQUE index tolerates
  AND e.id IN (
	SELECT MIN(id) FROM events
	WHERE dedupe_key IS NULL
	  AND type IN ('message.received', 'message.sent', 'message.reaction')
	  AND json_valid(payload)
	  AND json_extract(payload, '$.id') IS NOT NULL
	GROUP BY type, json_extract(payload, '$.id')
  )
  -- and never re-claim a key an earlier migration already assigned
  AND NOT EXISTS (
	SELECT 1 FROM events x
	WHERE x.dedupe_key = e.type || ':' || json_extract(e.payload, '$.id')
  )`); err != nil {
		return fmt.Errorf("backfilling events.dedupe_key: %w", err)
	}

	if _, err := s.db.Exec(`
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedupe_key ON events (dedupe_key)`); err != nil {
		return fmt.Errorf("creating events dedupe index: %w", err)
	}
	return nil
}

func (s *Store) columnExists(table, column string) (bool, error) {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, fmt.Errorf("inspecting %s columns: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("scanning %s columns: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) getMetaInt64(key string) (int64, error) {
	var v int64
	err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading meta %q: %w", key, err)
	}
	return v, nil
}
