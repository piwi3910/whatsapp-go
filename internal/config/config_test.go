package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piwi3910/whatsapp-go/internal/config"
)

func TestDefaults(t *testing.T) {
	cfg := config.Defaults()

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q; want %q", cfg.Server.Host, "127.0.0.1")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d; want 8080", cfg.Server.Port)
	}
	if cfg.Server.MaxUploadSize != 64*1024*1024 {
		t.Errorf("Server.MaxUploadSize = %d; want %d", cfg.Server.MaxUploadSize, 64*1024*1024)
	}
	if cfg.Events.MaxBuffer != 1000 {
		t.Errorf("Events.MaxBuffer = %d; want 1000", cfg.Events.MaxBuffer)
	}
	if cfg.Webhooks.TimeoutSeconds != 10 {
		t.Errorf("Webhooks.TimeoutSeconds = %d; want 10", cfg.Webhooks.TimeoutSeconds)
	}
	if cfg.Webhooks.MaxRetries != 3 {
		t.Errorf("Webhooks.MaxRetries = %d; want 3", cfg.Webhooks.MaxRetries)
	}
	if cfg.Database.Path == "" {
		t.Error("Database.Path should not be empty")
	}
}

func TestLoadCreatesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// A new API key should have been generated.
	if cfg.APIKey == "" {
		t.Error("Load() should generate an API key for a new config")
	}
	if len(cfg.APIKey) != 64 {
		t.Errorf("APIKey length = %d; want 64 hex chars", len(cfg.APIKey))
	}

	// The file should now exist on disk.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Load() should have created config file: %v", err)
	}

	// Defaults should be applied.
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d; want 8080", cfg.Server.Port)
	}
}

func TestLoadExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Write a partial config.
	content := `api_key: "testkey12345"
server:
  host: "0.0.0.0"
  port: 9090
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.APIKey != "testkey12345" {
		t.Errorf("APIKey = %q; want %q", cfg.APIKey, "testkey12345")
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q; want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d; want 9090", cfg.Server.Port)
	}
	// Fields not in the file should keep defaults.
	if cfg.Events.MaxBuffer != 1000 {
		t.Errorf("Events.MaxBuffer = %d; want 1000 (default)", cfg.Events.MaxBuffer)
	}
}

func TestDir(t *testing.T) {
	dir := config.Dir()
	if dir == "" {
		t.Error("Dir() should not return empty string")
	}
	if !strings.HasSuffix(dir, "wa") {
		t.Errorf("Dir() = %q; should end with 'wa'", dir)
	}
}

func TestGenerateAPIKey(t *testing.T) {
	key1, err := config.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}
	if len(key1) != 64 {
		t.Errorf("GenerateAPIKey() length = %d; want 64", len(key1))
	}

	key2, err := config.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey() error = %v", err)
	}
	if key1 == key2 {
		t.Error("GenerateAPIKey() returned the same key twice")
	}
}

func TestDirRespectsXDG(t *testing.T) {
	prev := os.Getenv("XDG_CONFIG_HOME")
	defer os.Setenv("XDG_CONFIG_HOME", prev)

	os.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	dir := config.Dir()
	if dir != "/tmp/xdg-test/wa" {
		t.Errorf("Dir() with XDG_CONFIG_HOME = %q; want %q", dir, "/tmp/xdg-test/wa")
	}
}
