// Package config loads the wa configuration.
//
// Precedence, highest first:
//
//  1. environment variables (WA_*)
//  2. the YAML config file
//  3. built-in defaults (which differ slightly in container mode)
//
// Loading never writes to disk. A container image typically runs with a
// read-only root filesystem and no writable home directory, so a load path
// that creates files fails the process before it can serve anything. Writing
// a config file is an explicit action (Save), triggered by `wa serve
// --write-config`.
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Environment variable names. Every field a deployment realistically needs to
// set has one, so the process can be configured entirely through a k8s
// Deployment spec plus a Secret, with no config file present at all.
const (
	EnvAPIKey                     = "WA_API_KEY"
	EnvHost                       = "WA_HOST"
	EnvPort                       = "WA_PORT"
	EnvDBPath                     = "WA_DB_PATH"
	EnvMaxUploadSize              = "WA_MAX_UPLOAD_SIZE"
	EnvEventsMaxBuffer            = "WA_EVENTS_MAX_BUFFER"
	EnvAllowPrivateWebhookTargets = "WA_ALLOW_PRIVATE_WEBHOOK_TARGETS"

	// EnvContainer forces container-mode detection on or off. Auto-detection
	// is a heuristic; an operator must always be able to overrule it.
	EnvContainer = "WA_CONTAINER"
)

// ContainerStateDir is where a containerised deployment is expected to mount
// its persistent volume. The session database must survive restarts — losing
// it means re-pairing the phone by hand.
const ContainerStateDir = "/data"

// Config is the top-level configuration.
type Config struct {
	APIKey   string          `yaml:"api_key"`
	Server   ServerConfig    `yaml:"server"`
	Database DatabaseConfig  `yaml:"database"`
	Events   EventsConfig    `yaml:"events"`
	Webhooks []WebhookConfig `yaml:"webhooks"`

	// AllowPrivateWebhookTargets permits webhook URLs pointing at loopback
	// or private addresses. Off by default: on an internet-reachable
	// server, anyone able to register a webhook could otherwise use it to
	// reach services on the host's network. Turn it on when the receiver is
	// deliberately private — e.g. another service in the same cluster.
	AllowPrivateWebhookTargets bool `yaml:"allow_private_webhook_targets"`
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

// Defaults returns a Config with default values for an interactive,
// single-user install. See ContainerDefaults for the container variant.
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

// ContainerDefaults returns the defaults used when running inside a
// container. Two differences matter:
//
//   - Host is 0.0.0.0. Bound to localhost the process is reachable only from
//     inside its own network namespace, so every probe and every Service
//     connection fails — the single most common "works locally, dead in k8s"
//     misconfiguration.
//   - The database lives on a fixed mount path rather than under $HOME, which
//     on a distroless non-root image is either unset or not writable.
func ContainerDefaults() Config {
	cfg := Defaults()
	cfg.Server.Host = "0.0.0.0"
	cfg.Database.Path = filepath.Join(ContainerStateDir, "wa.db")
	return cfg
}

// InContainer reports whether the process appears to be running inside a
// container. Explicit WA_CONTAINER wins; otherwise we look for the usual
// runtime fingerprints.
func InContainer() bool {
	if v, ok := os.LookupEnv(EnvContainer); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(data)
		for _, marker := range []string{"docker", "kubepods", "containerd", "podman"} {
			if strings.Contains(s, marker) {
				return true
			}
		}
	}
	return false
}

// Load builds the effective configuration: defaults, then the file at path if
// it exists, then environment overrides. A missing file is not an error — a
// container is expected to be configured entirely from the environment.
//
// Load never creates or modifies files.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	if InContainer() {
		cfg = ContainerDefaults()
	}

	var file Config
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
		mergeFile(&cfg, &file)
	case errors.Is(err, fs.ErrNotExist):
		// Fall through: defaults + environment are a complete configuration.
	default:
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := applyEnv(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// mergeFile copies the fields the file actually set over the defaults. It has
// to be explicit rather than a plain yaml.Unmarshal onto the default struct:
// distinguishing "absent from the file" from "set to the zero value" is what
// lets container defaults apply only where the operator stayed silent.
func mergeFile(dst, file *Config) {
	if file.APIKey != "" {
		dst.APIKey = file.APIKey
	}
	if file.Server.Host != "" {
		dst.Server.Host = file.Server.Host
	}
	if file.Server.Port != 0 {
		dst.Server.Port = file.Server.Port
	}
	if file.Server.MaxUploadSize != 0 {
		dst.Server.MaxUploadSize = file.Server.MaxUploadSize
	}
	if file.Database.Path != "" {
		dst.Database.Path = file.Database.Path
	}
	if file.Events.MaxBuffer != 0 {
		dst.Events.MaxBuffer = file.Events.MaxBuffer
	}
	if file.AllowPrivateWebhookTargets {
		dst.AllowPrivateWebhookTargets = true
	}
	if len(file.Webhooks) > 0 {
		dst.Webhooks = file.Webhooks
	}
}

// applyEnv overlays WA_* environment variables. A malformed value is a hard
// error: silently ignoring WA_PORT=8O80 (letter O) would leave the process
// listening somewhere the operator does not expect.
func applyEnv(cfg *Config) error {
	if v, ok := os.LookupEnv(EnvAPIKey); ok {
		cfg.APIKey = v
	}
	if v, ok := os.LookupEnv(EnvHost); ok {
		cfg.Server.Host = v
	}
	if v, ok := os.LookupEnv(EnvDBPath); ok {
		cfg.Database.Path = v
	}
	if v, ok := os.LookupEnv(EnvPort); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%s=%q: not a valid TCP port", EnvPort, v)
		}
		cfg.Server.Port = n
	}
	if v, ok := os.LookupEnv(EnvMaxUploadSize); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("%s=%q: not a valid byte count", EnvMaxUploadSize, v)
		}
		cfg.Server.MaxUploadSize = n
	}
	if v, ok := os.LookupEnv(EnvEventsMaxBuffer); ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("%s=%q: not a valid event count", EnvEventsMaxBuffer, v)
		}
		cfg.Events.MaxBuffer = n
	}
	if v, ok := os.LookupEnv(EnvAllowPrivateWebhookTargets); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s=%q: not a boolean", EnvAllowPrivateWebhookTargets, v)
		}
		cfg.AllowPrivateWebhookTargets = b
	}
	return nil
}

// Save writes config to path, creating parent directories as needed. Callers
// must only invoke this in response to an explicit user request.
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

// KeyFingerprint returns a short, non-reversible identifier for an API key,
// safe to log: enough to confirm which key a client should be using without
// putting the credential itself into log storage. Returns "none" for an
// empty key.
func KeyFingerprint(key string) string {
	if key == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}
