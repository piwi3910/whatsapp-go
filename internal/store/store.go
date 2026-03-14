package store

import (
	"database/sql"
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

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) migrate() error {
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
`)
	return err
}
