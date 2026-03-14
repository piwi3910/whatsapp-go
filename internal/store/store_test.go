package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew_CreatesAllTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	tables := []string{"messages", "events", "media_uploads", "webhooks"}
	for _, table := range tables {
		var name string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestNew_DirectoryCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "test.db")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		t.Error("directory was not created")
	}
}
