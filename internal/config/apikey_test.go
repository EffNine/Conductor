package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAPIKey_EnvWins(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, DefaultAPIKeyFileName)
	if err := os.WriteFile(keyPath, []byte("file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_API_KEY", "env-key")

	cfg := &Config{}
	cfg.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg.Server.Port = 8080
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Database.Driver = "sqlite"

	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env-key", cfg.APIKey)
	}
	if cfg.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=false when env is set")
	}
}

func TestResolveAPIKey_YAMLWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultAPIKeyFileName), []byte("file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_API_KEY", "")

	cfg := &Config{APIKey: "yaml-key"}
	cfg.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg.Server.Port = 8080
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Database.Driver = "sqlite"

	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.APIKey != "yaml-key" {
		t.Fatalf("APIKey = %q, want yaml-key", cfg.APIKey)
	}
	if cfg.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=false when YAML key is set")
	}
}

func TestResolveAPIKey_LoadsPersistedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DefaultAPIKeyFileName), []byte("persisted-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_API_KEY", "")

	cfg := &Config{}
	cfg.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg.Server.Port = 8080
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Database.Driver = "sqlite"

	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.APIKey != "persisted-key" {
		t.Fatalf("APIKey = %q, want persisted-key", cfg.APIKey)
	}
	if cfg.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=false when loading existing file")
	}
}

func TestResolveAPIKey_OverwritesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, DefaultAPIKeyFileName)
	if err := os.WriteFile(keyPath, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_API_KEY", "")

	cfg := &Config{}
	cfg.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg.Server.Port = 8080
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Database.Driver = "sqlite"

	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.APIKey == "" {
		t.Fatal("expected generated API key after empty file")
	}
	if !cfg.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=true when recovering empty file")
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read persisted key: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != cfg.APIKey {
		t.Fatalf("persisted key = %q, want %q", got, cfg.APIKey)
	}
}

func TestResolveAPIKey_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONDUCTOR_API_KEY", "")

	cfg := &Config{}
	cfg.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg.Server.Port = 8080
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Database.Driver = "sqlite"

	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.APIKey == "" {
		t.Fatal("expected generated API key")
	}
	if !cfg.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=true")
	}
	if len(cfg.APIKey) != 64 {
		t.Fatalf("len(APIKey) = %d, want 64", len(cfg.APIKey))
	}

	keyPath := APIKeyFilePath(cfg)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read persisted key: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != cfg.APIKey {
		t.Fatalf("persisted key = %q, want %q", got, cfg.APIKey)
	}

	// Second resolve should reuse the file and not regenerate.
	cfg2 := &Config{}
	cfg2.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg2.Server.Port = 8080
	cfg2.Logging.Level = "info"
	cfg2.Logging.Format = "json"
	cfg2.Database.Driver = "sqlite"
	t.Setenv("CONDUCTOR_API_KEY", "")

	if err := validate(cfg2); err != nil {
		t.Fatalf("second validate: %v", err)
	}
	if cfg2.APIKey != cfg.APIKey {
		t.Fatalf("second APIKey = %q, want %q", cfg2.APIKey, cfg.APIKey)
	}
	if cfg2.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=false on second load")
	}
}

func TestResolveAPIKey_PlaceholderTreatedAsUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONDUCTOR_API_KEY", "")

	cfg := &Config{APIKey: "${CONDUCTOR_API_KEY}"}
	cfg.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg.Server.Port = 8080
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Database.Driver = "sqlite"

	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.APIKey == "" || cfg.APIKey == "${CONDUCTOR_API_KEY}" {
		t.Fatalf("expected generated key, got %q", cfg.APIKey)
	}
	if !cfg.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=true for unresolved placeholder")
	}
}

func TestResolveAPIKey_EnvWinsOverPlaceholder(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONDUCTOR_API_KEY", "env-key")

	cfg := &Config{APIKey: "${CONDUCTOR_API_KEY}"}
	cfg.Database.DSN = filepath.Join(dir, "conductor.db")
	cfg.Server.Port = 8080
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"
	cfg.Database.Driver = "sqlite"

	if err := validate(cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.APIKey != "env-key" {
		t.Fatalf("APIKey = %q, want env-key", cfg.APIKey)
	}
	if cfg.APIKeyJustGenerated {
		t.Fatal("expected APIKeyJustGenerated=false when env wins over placeholder")
	}
}

func TestAPIKeyFilePath_Default(t *testing.T) {
	got := APIKeyFilePath(nil)
	want := filepath.Join("./data", DefaultAPIKeyFileName)
	if got != want {
		t.Fatalf("APIKeyFilePath(nil) = %q, want %q", got, want)
	}
}

func TestAPIKeyFilePath_FromDSN(t *testing.T) {
	cfg := &Config{}
	cfg.Database.DSN = "/var/lib/conductor/conductor.db"
	got := APIKeyFilePath(cfg)
	want := filepath.Join("/var/lib/conductor", DefaultAPIKeyFileName)
	if got != want {
		t.Fatalf("APIKeyFilePath = %q, want %q", got, want)
	}
}

func TestAPIKeyFilePath_BareFilenameDSN(t *testing.T) {
	cfg := &Config{}
	cfg.Database.DSN = "conductor.db"
	got := APIKeyFilePath(cfg)
	want := filepath.Join(".", DefaultAPIKeyFileName)
	if got != want {
		t.Fatalf("APIKeyFilePath = %q, want %q", got, want)
	}
}
