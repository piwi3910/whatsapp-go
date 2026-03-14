package store

import (
	"database/sql"
	"fmt"
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
	return n, nil
}
