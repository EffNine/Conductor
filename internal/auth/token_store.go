package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TokenStore verifies conductor service tokens stored in the effnine control-plane DB.
// It is the counterpart to the control-plane's internal/tokens.Store.
// The store is optional; when not configured the gateway falls back to single-key auth.
type TokenStore struct {
	db *sql.DB
}

// NewTokenStore opens the control-plane SQLite file in read-only mode.
// If path is empty it tries common locations and env vars.
func NewTokenStore(path string) (*TokenStore, error) {
	if path == "" {
		// env override
		if v := os.Getenv("EFFNINE_DB_PATH"); v != "" {
			path = v
		} else if v := os.Getenv("CONDUCTOR_CONTROL_PLANE_DB"); v != "" {
			path = v
		} else if v := os.Getenv("EFFNINE_CONTROL_PLANE_DB"); v != "" {
			path = v
		}
	}
	if path == "" {
		// try default dev locations (no error if not found — caller may ignore)
		candidates := []string{
			"./data/effnine.db",
			"./domain/control-plane/data/effnine.db",
			"../domain/control-plane/data/effnine.db",
			os.Getenv("HOME") + "/projects/data/effnine.db",
			"/app/data/effnine.db",
		}
		for _, c := range candidates {
			if c == "" {
				continue
			}
			if _, err := os.Stat(c); err == nil {
				path = c
				break
			}
		}
	}
	if path == "" {
		return nil, fmt.Errorf("no control-plane db path")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	// read-only open with WAL+busy_timeout for shared access
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL&_busy_timeout=5000&_query_only=true", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &TokenStore{db: db}, nil
}

// Close releases the handle.
func (ts *TokenStore) Close() error {
	if ts == nil || ts.db == nil {
		return nil
	}
	return ts.db.Close()
}

// Verify checks that raw token exists, is for service=conductor, not revoked/expired, and contains one of requiredScopes (if non-empty).
// It returns the token's scopes and fingerprint on success.
func (ts *TokenStore) Verify(raw string, requiredScopes []string) (scopes []string, fingerprint string, err error) {
	if ts == nil || ts.db == nil {
		return nil, "", fmt.Errorf("token store not configured")
	}
	hash := hashToken(raw)
	var service, scopesJSON, fp, expiresAt, revokedAt sql.NullString
	err = ts.db.QueryRow(`SELECT service, scopes, token_fingerprint, expires_at, revoked_at FROM service_token WHERE token_hash=? LIMIT 1`, hash).
		Scan(&service, &scopesJSON, &fp, &expiresAt, &revokedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, "", ErrTokenNotFound
		}
		return nil, "", err
	}
	if service.String != "conductor" {
		return nil, "", ErrWrongService
	}
	if revokedAt.Valid && revokedAt.String != "" {
		return nil, "", ErrTokenRevoked
	}
	if expiresAt.Valid && expiresAt.String != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt.String); err == nil && time.Now().After(t) {
			return nil, "", ErrTokenExpired
		}
	}
	if scopesJSON.Valid {
		_ = json.Unmarshal([]byte(scopesJSON.String), &scopes)
	}
	// scope check
	if len(requiredScopes) > 0 {
		for _, need := range requiredScopes {
			if !hasScope(scopes, need) {
				return nil, "", fmt.Errorf("%w: %s", ErrInsufficientScope, need)
			}
		}
	}
	// best-effort last_used update — ignored for read-only open (query_only); try but don't fail on error
	// We skip update to keep read-only; control-plane's /internal/verify is the authoritative last_used writer.
	return scopes, fp.String, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func hasScope(tokenScopes []string, required string) bool {
	for _, s := range tokenScopes {
		if s == required {
			return true
		}
		if s == "conductor:admin" {
			// admin implies all conductor scopes
			return true
		}
	}
	return false
}

// MustClose is helper for main defer.
func (ts *TokenStore) mustClose() {
	if ts != nil {
		_ = ts.Close()
	}
}
