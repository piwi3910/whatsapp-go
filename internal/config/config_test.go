package config

import (
	"os"
	"path/filepath"
	"strings"
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
	if cfg.Server.MaxUploadSize != 100*1024*1024 {
		t.Errorf("default max_upload_size = %d, want %d", cfg.Server.MaxUploadSize, 100*1024*1024)
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
	if !strings.HasSuffix(d, "wa") {
		t.Errorf("Dir() = %q, should end with 'wa'", d)
	}
}

func TestGenerateAPIKey(t *testing.T) {
	key := GenerateAPIKey()
	if !strings.HasPrefix(key, "wa_") {
		t.Errorf("key = %q, should start with wa_", key)
	}
	if len(key) < 10 {
		t.Errorf("key too short: %q", key)
	}
	key2 := GenerateAPIKey()
	if key == key2 {
		t.Error("GenerateAPIKey produced same key twice")
	}
}
