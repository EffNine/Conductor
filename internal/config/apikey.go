package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EffNine/conductor/internal/auth"
)

// resolveAPIKey loads the gateway API key from YAML/env, then a persisted file,
// or generates and persists a new key when none is configured.
func resolveAPIKey(cfg *Config) error {
	cfg.APIKeyJustGenerated = false

	// Treat empty and unresolved placeholders the same so env can win over a
	// literal "${CONDUCTOR_API_KEY}" left in YAML.
	if isUnsetAPIKey(cfg.APIKey) {
		cfg.APIKey = os.Getenv("CONDUCTOR_API_KEY")
	}
	if isUnsetAPIKey(cfg.APIKey) {
		cfg.APIKey = ""
	}
	if cfg.APIKey != "" {
		return nil
	}

	keyPath := APIKeyFilePath(cfg)
	if key, ok, err := readAPIKeyFile(keyPath); err != nil {
		return err
	} else if ok {
		cfg.APIKey = key
		return nil
	}

	key, err := auth.Generate()
	if err != nil {
		return fmt.Errorf("generate api key: %w", err)
	}

	if err = os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return fmt.Errorf("create api key directory: %w", err)
	}

	// Exclusive create so concurrent first boots converge on one key.
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			existing, ok, readErr := readAPIKeyFileRetry(keyPath)
			if readErr != nil {
				return readErr
			}
			if ok {
				cfg.APIKey = existing
				return nil
			}
			// Empty/stale file (failed prior write, crash after create, or touch)
			// blocks O_EXCL; overwrite so startup can recover without manual deletion.
			if writeErr := os.WriteFile(keyPath, []byte(key+"\n"), 0o600); writeErr != nil {
				return fmt.Errorf("persist api key to %s: %w", keyPath, writeErr)
			}
			cfg.APIKey = key
			cfg.APIKeyJustGenerated = true
			return nil
		}
		return fmt.Errorf("persist api key to %s: %w", keyPath, err)
	}
	_, writeErr := f.WriteString(key + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("persist api key to %s: %w", keyPath, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("persist api key to %s: %w", keyPath, closeErr)
	}

	cfg.APIKey = key
	cfg.APIKeyJustGenerated = true
	return nil
}

// readAPIKeyFile reads a persisted gateway API key. ok is false when the file
// is missing or empty (caller may generate). Non-existence is not an error.
func readAPIKeyFile(path string) (key string, ok bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read api key file %s: %w", path, err)
	}
	key = strings.TrimSpace(string(data))
	if key == "" {
		return "", false, nil
	}
	return key, true, nil
}

// readAPIKeyFileRetry re-reads after losing an exclusive-create race, giving the
// winning writer a brief window to finish flushing the key.
func readAPIKeyFileRetry(path string) (string, bool, error) {
	for i := 0; i < 50; i++ {
		key, ok, err := readAPIKeyFile(path)
		if err != nil {
			return "", false, err
		}
		if ok {
			return key, true, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "", false, nil
}

// isUnsetAPIKey reports whether a configured value is empty or an unresolved
// shell-style placeholder from config.example.yaml.
func isUnsetAPIKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return true
	}
	switch key {
	case "${CONDUCTOR_API_KEY}", "$CONDUCTOR_API_KEY", "${NOVEXA_API_KEY}", "$NOVEXA_API_KEY":
		return true
	default:
		return false
	}
}

// APIKeyFilePath returns the path used to persist an auto-generated gateway API key.
// It lives next to the SQLite database when the DSN is a local file path; otherwise
// it defaults to ./data/conductor.api_key.
func APIKeyFilePath(cfg *Config) string {
	dir := "./data"
	if cfg != nil && cfg.Database.DSN != "" {
		dsn := cfg.Database.DSN
		// Only derive a directory from simple filesystem DSNs (not postgres URLs / :memory:).
		// Bare filenames (e.g. "conductor.db") yield Dir ".", which still colocates the key
		// with the DB in the process working directory.
		if !strings.Contains(dsn, "://") && !strings.HasPrefix(dsn, "file:") && dsn != ":memory:" {
			if d := filepath.Dir(dsn); d != "" {
				dir = d
			}
		}
	}
	return filepath.Join(dir, DefaultAPIKeyFileName)
}
