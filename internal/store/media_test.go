package store

import (
	"testing"
	"time"

	"github.com/piwi3910/whatsapp-go/internal/models"
)

func TestMediaUploadCRUD(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().Unix()
	upload := &models.MediaUpload{
		ID:        "media-1",
		Data:      []byte("file content"),
		MimeType:  "image/jpeg",
		Filename:  "photo.jpg",
		Size:      int64(len("file content")),
		CreatedAt: now,
		ExpiresAt: now + 3600,
	}

	// Insert
	if err := s.InsertMediaUpload(upload); err != nil {
		t.Fatalf("InsertMediaUpload: %v", err)
	}

	// Get
	got, err := s.GetMediaUpload("media-1")
	if err != nil {
		t.Fatalf("GetMediaUpload: %v", err)
	}
	if got.ID != upload.ID {
		t.Errorf("ID: got %q want %q", got.ID, upload.ID)
	}
	if got.MimeType != upload.MimeType {
		t.Errorf("MimeType: got %q want %q", got.MimeType, upload.MimeType)
	}
	if string(got.Data) != string(upload.Data) {
		t.Errorf("Data: got %q want %q", got.Data, upload.Data)
	}

	// Delete
	if err := s.DeleteMediaUpload("media-1"); err != nil {
		t.Fatalf("DeleteMediaUpload: %v", err)
	}

	// Verify gone
	if _, err := s.GetMediaUpload("media-1"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestPruneExpiredUploads(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().Unix()

	// Expired upload
	expired := &models.MediaUpload{
		ID:        "media-expired",
		Data:      []byte("old data"),
		MimeType:  "image/png",
		Size:      8,
		CreatedAt: now - 7200,
		ExpiresAt: now - 3600,
	}
	if err := s.InsertMediaUpload(expired); err != nil {
		t.Fatalf("InsertMediaUpload expired: %v", err)
	}

	// Valid upload
	valid := &models.MediaUpload{
		ID:        "media-valid",
		Data:      []byte("new data"),
		MimeType:  "image/png",
		Size:      8,
		CreatedAt: now,
		ExpiresAt: now + 3600,
	}
	if err := s.InsertMediaUpload(valid); err != nil {
		t.Fatalf("InsertMediaUpload valid: %v", err)
	}

	n, err := s.PruneExpiredUploads()
	if err != nil {
		t.Fatalf("PruneExpiredUploads: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 pruned, got %d", n)
	}

	// Expired should be gone
	if _, err := s.GetMediaUpload("media-expired"); err != ErrNotFound {
		t.Errorf("expected expired to be pruned, got %v", err)
	}

	// Valid should still be there
	if _, err := s.GetMediaUpload("media-valid"); err != nil {
		t.Errorf("expected valid to remain, got %v", err)
	}
}
