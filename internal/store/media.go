package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func (s *Store) InsertMediaUpload(upload *models.MediaUpload) error {
	_, err := s.db.Exec(`
INSERT INTO media_uploads (id, data, mime_type, filename, size, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		upload.ID, upload.Data, upload.MimeType,
		nullString(upload.Filename), upload.Size,
		upload.CreatedAt, upload.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("inserting media upload: %w", err)
	}
	return nil
}

func (s *Store) GetMediaUpload(id string) (*models.MediaUpload, error) {
	now := time.Now().Unix()
	row := s.db.QueryRow(`
SELECT id, data, mime_type, COALESCE(filename, ''), size, created_at, expires_at
FROM media_uploads
WHERE id = ? AND expires_at > ?`, id, now)

	var upload models.MediaUpload
	err := row.Scan(
		&upload.ID, &upload.Data, &upload.MimeType, &upload.Filename,
		&upload.Size, &upload.CreatedAt, &upload.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting media upload: %w", err)
	}
	return &upload, nil
}

func (s *Store) DeleteMediaUpload(id string) error {
	res, err := s.db.Exec(`DELETE FROM media_uploads WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting media upload: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// vacuumThresholdBytes is how much free space must accumulate before a full
// VACUUM is worth its cost. A VACUUM rewrites the entire database file, so
// running one after every prune would be pathological on a busy server;
// waiting for 32MB of freelist means it runs rarely and reclaims a lot.
const vacuumThresholdBytes = 32 << 20

// PruneExpiredUploads deletes uploads past their expiry and returns free
// space to the filesystem.
//
// Media is stored as SQLite BLOBs, and deleting a row only moves its pages
// onto the freelist — the file itself never shrinks. Media blobs are by far
// the largest rows in this database, so without the reclaim below an hour of
// uploads permanently inflates the file and the disk usage only ever ratchets
// upward.
func (s *Store) PruneExpiredUploads() (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`DELETE FROM media_uploads WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, fmt.Errorf("pruning expired uploads: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected: %w", err)
	}
	if n > 0 {
		// Non-fatal: the rows are gone either way, and failing the prune
		// because housekeeping could not run would be a worse outcome than
		// a temporarily larger file.
		if err := s.ReclaimFreeSpace(); err != nil {
			slog.Warn("reclaiming database free space", "error", err)
		}
	}
	return n, nil
}

// ReclaimFreeSpace returns free pages to the filesystem.
//
// It prefers PRAGMA incremental_vacuum, which is cheap and incremental, but
// that only does anything when the database was created with
// auto_vacuum=INCREMENTAL — a mode that cannot be turned on for an existing
// database without a full VACUUM, and the connection is opened in store.go.
// So it falls back to a full VACUUM, gated on vacuumThresholdBytes of
// accumulated freelist so the whole-file rewrite stays rare.
//
// Safe on this Store because it holds a single connection with no long-lived
// transaction; VACUUM would fail inside one.
func (s *Store) ReclaimFreeSpace() error {
	var autoVacuum int
	if err := s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		return fmt.Errorf("reading auto_vacuum mode: %w", err)
	}
	const autoVacuumIncremental = 2
	if autoVacuum == autoVacuumIncremental {
		if _, err := s.db.Exec(`PRAGMA incremental_vacuum`); err != nil {
			return fmt.Errorf("incremental vacuum: %w", err)
		}
		return nil
	}

	freeBytes, err := s.freeListBytes()
	if err != nil {
		return err
	}
	if freeBytes < vacuumThresholdBytes {
		return nil
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("vacuuming database: %w", err)
	}
	// In WAL mode VACUUM rewrites the database through the write-ahead log,
	// so the main file does not actually shrink until a checkpoint copies
	// those pages back and truncates. Without this the VACUUM appears to do
	// nothing — and the WAL is now as large as the data we just freed.
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpointing after vacuum: %w", err)
	}
	slog.Info("vacuumed database", "reclaimed_bytes", freeBytes)
	return nil
}

func (s *Store) freeListBytes() (int64, error) {
	var freePages, pageSize int64
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return 0, fmt.Errorf("reading freelist_count: %w", err)
	}
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("reading page_size: %w", err)
	}
	return freePages * pageSize, nil
}
