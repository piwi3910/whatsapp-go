package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// notInContainer pins container detection off so the test asserts the
// interactive defaults regardless of where the suite runs (CI runners,
// dev laptops, and containers must all agree).
func notInContainer(t *testing.T) {
	t.Helper()
	t.Setenv(EnvContainer, "false")
}

// clearEnv unsets every WA_* override so a developer's shell can't change
// the outcome of a precedence test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		EnvAPIKey, EnvHost, EnvPort, EnvDBPath, EnvMaxUploadSize,
		EnvEventsMaxBuffer, EnvAllowPrivateWebhookTargets,
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

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

func TestContainerDefaults(t *testing.T) {
	cfg := ContainerDefaults()
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("container host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Database.Path != filepath.Join(ContainerStateDir, "wa.db") {
		t.Errorf("container db path = %q, want under %q", cfg.Database.Path, ContainerStateDir)
	}
}

func TestLoadMissingFileDoesNotWrite(t *testing.T) {
	notInContainer(t)
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", path, err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("loaded port = %d, want default 8080", cfg.Server.Port)
	}
	// A read-only rootfs must not turn a missing config into a startup failure.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Load created a config file; it must never write")
	}
}

func TestLoadExistingFile(t *testing.T) {
	notInContainer(t)
	clearEnv(t)
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
	// Untouched by the file: default must survive.
	if cfg.Events.MaxBuffer != 10000 {
		t.Errorf("max_buffer = %d, want default 10000", cfg.Events.MaxBuffer)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	notInContainer(t)
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("api_key: \"from_file\"\nserver:\n  host: \"127.0.0.1\"\n  port: 9090\n  max_upload_size: 111\ndatabase:\n  path: \"/file/wa.db\"\nevents:\n  max_buffer: 222\nallow_private_webhook_targets: false\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(EnvAPIKey, "from_env")
	t.Setenv(EnvHost, "0.0.0.0")
	t.Setenv(EnvPort, "7000")
	t.Setenv(EnvDBPath, "/env/wa.db")
	t.Setenv(EnvMaxUploadSize, "333")
	t.Setenv(EnvEventsMaxBuffer, "444")
	t.Setenv(EnvAllowPrivateWebhookTargets, "true")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if cfg.APIKey != "from_env" {
		t.Errorf("api_key = %q, want env value", cfg.APIKey)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("host = %q, want env value", cfg.Server.Host)
	}
	if cfg.Server.Port != 7000 {
		t.Errorf("port = %d, want env value 7000", cfg.Server.Port)
	}
	if cfg.Database.Path != "/env/wa.db" {
		t.Errorf("db path = %q, want env value", cfg.Database.Path)
	}
	if cfg.Server.MaxUploadSize != 333 {
		t.Errorf("max_upload_size = %d, want env value 333", cfg.Server.MaxUploadSize)
	}
	if cfg.Events.MaxBuffer != 444 {
		t.Errorf("max_buffer = %d, want env value 444", cfg.Events.MaxBuffer)
	}
	if !cfg.AllowPrivateWebhookTargets {
		t.Error("allow_private_webhook_targets = false, want env value true")
	}
}

func TestEnvOverridesDefaultsWithoutFile(t *testing.T) {
	notInContainer(t)
	clearEnv(t)
	t.Setenv(EnvHost, "0.0.0.0")
	t.Setenv(EnvPort, "9999")

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 9999 {
		t.Errorf("got %s:%d, want 0.0.0.0:9999", cfg.Server.Host, cfg.Server.Port)
	}
}

func TestContainerModeChangesDefaultsButFileWins(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvContainer, "true")

	// No file: container defaults apply.
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("container host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Database.Path != filepath.Join(ContainerStateDir, "wa.db") {
		t.Errorf("container db path = %q, want %q", cfg.Database.Path, filepath.Join(ContainerStateDir, "wa.db"))
	}

	// A file that names a host must still beat the container default.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  host: \"10.0.0.1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if cfg.Server.Host != "10.0.0.1" {
		t.Errorf("host = %q, want file value 10.0.0.1", cfg.Server.Host)
	}
}

func TestInContainerExplicitOverride(t *testing.T) {
	t.Setenv(EnvContainer, "true")
	if !InContainer() {
		t.Error("WA_CONTAINER=true should force container mode on")
	}
	t.Setenv(EnvContainer, "false")
	if InContainer() {
		t.Error("WA_CONTAINER=false should force container mode off")
	}
}

func TestLoadRejectsBadEnv(t *testing.T) {
	notInContainer(t)
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "absent.yaml")

	for _, tc := range []struct{ key, val string }{
		{EnvPort, "not-a-number"},
		{EnvPort, "70000"},
		{EnvMaxUploadSize, "-1"},
		{EnvEventsMaxBuffer, "lots"},
		{EnvAllowPrivateWebhookTargets, "sure"},
	} {
		t.Run(tc.key+"="+tc.val, func(t *testing.T) {
			t.Setenv(tc.key, tc.val)
			if _, err := Load(path); err == nil {
				t.Errorf("Load with %s=%q: want error, got nil", tc.key, tc.val)
			}
		})
	}
}

func TestSaveRoundTrip(t *testing.T) {
	notInContainer(t)
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg := Defaults()
	cfg.APIKey = "wa_saved"
	if err := Save(path, &cfg); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if got.APIKey != "wa_saved" {
		t.Errorf("api_key = %q, want wa_saved", got.APIKey)
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

func TestKeyFingerprint(t *testing.T) {
	if got := KeyFingerprint(""); got != "none" {
		t.Errorf("KeyFingerprint(\"\") = %q, want none", got)
	}
	a := KeyFingerprint("wa_one")
	if a == "wa_one" || len(a) != 8 {
		t.Errorf("fingerprint %q should be an 8-char digest, not the key", a)
	}
	if KeyFingerprint("wa_two") == a {
		t.Error("different keys produced the same fingerprint")
	}
}
