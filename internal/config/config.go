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
