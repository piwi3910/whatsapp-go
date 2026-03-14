package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration for the wa daemon.
type Config struct {
	APIKey   string         `yaml:"api_key"`
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Events   EventsConfig   `yaml:"events"`
	Webhooks WebhooksConfig `yaml:"webhooks"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	MaxUploadSize int64  `yaml:"max_upload_size"`
}

// DatabaseConfig controls the SQLite store.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// EventsConfig controls the in-process event bus.
type EventsConfig struct {
	MaxBuffer int `yaml:"max_buffer"`
}

// WebhooksConfig controls outgoing webhook delivery.
type WebhooksConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	MaxRetries     int `yaml:"max_retries"`
}

// Dir returns the default application configuration directory:
// $XDG_CONFIG_HOME/wa or ~/.config/wa.
func Dir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "wa")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wa")
}

// Defaults returns a Config populated with sensible defaults.
func Defaults() Config {
	return Config{
		APIKey: "",
		Server: ServerConfig{
			Host:          "127.0.0.1",
			Port:          8080,
			MaxUploadSize: 64 * 1024 * 1024, // 64 MiB
		},
		Database: DatabaseConfig{
			Path: filepath.Join(Dir(), "wa.db"),
		},
		Events: EventsConfig{
			MaxBuffer: 1000,
		},
		Webhooks: WebhooksConfig{
			TimeoutSeconds: 10,
			MaxRetries:     3,
		},
	}
}

// Load reads config from path. If the file does not exist, it creates it with
// defaults (including a freshly generated API key) and returns that config.
func Load(path string) (Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key, genErr := GenerateAPIKey()
		if genErr != nil {
			return cfg, genErr
		}
		cfg.APIKey = key
		if saveErr := Save(path, cfg); saveErr != nil {
			return cfg, saveErr
		}
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Save writes cfg to path in YAML format, creating parent directories as needed.
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// GenerateAPIKey returns a cryptographically random 32-byte hex string.
func GenerateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
