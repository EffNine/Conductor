package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Roles understood by the gateway. A role is descriptive; enforcement uses
// scopes, and the admin role is treated as conductor:admin by IsAdmin.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleMember   = "member"
	RoleViewer   = "viewer"
)

// Principal is the authenticated caller of a request. UserID is the only
// attribution source; handlers must never read a user id from a header.
type Principal struct {
	UserID string
	KeyID  string
	Role   string
	Scopes []string
}

// HasScope reports whether the principal carries a scope. Admin implies all.
func (p *Principal) HasScope(required string) bool {
	if p == nil {
		return false
	}
	for _, s := range p.Scopes {
		if s == required || s == "conductor:admin" {
			return true
		}
	}
	return false
}

// IsAdmin reports whether the principal may see and manage every user.
func (p *Principal) IsAdmin() bool {
	return p != nil && (p.Role == RoleAdmin || p.HasScope("conductor:admin"))
}

// User is a gateway user. Quota fields are nil when the gateway default applies.
type User struct {
	ID                   string     `json:"id"`
	Label                string     `json:"label"`
	Role                 string     `json:"role"`
	DisabledAt           *time.Time `json:"disabled_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	MaxRequestsPerMinute *int       `json:"max_requests_per_minute,omitempty"`
	MaxTokensPerMonth    *int64     `json:"max_tokens_per_month,omitempty"`
	MaxSpendUSDPerMonth  *float64   `json:"max_spend_usd_per_month,omitempty"`
	ModelAllowlist       []string   `json:"model_allowlist,omitempty"`
}

// APIKey is a credential's public metadata. The raw key is never stored.
type APIKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// KeyStore owns users and API keys in the gateway database.
type KeyStore struct {
	db *sql.DB
}

// NewKeyStore wraps the gateway SQLite handle.
func NewKeyStore(db *sql.DB) *KeyStore {
	return &KeyStore{db: db}
}

func parseTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s.String); err == nil {
			return &t
		}
	}
	return nil
}

func validRole(role string) bool {
	switch role {
	case RoleAdmin, RoleOperator, RoleMember, RoleViewer:
		return true
	}
	return false
}

// CreateUser inserts a user. ID and label are required; an empty role is member.
func (s *KeyStore) CreateUser(u User) error {
	if strings.TrimSpace(u.ID) == "" || strings.TrimSpace(u.Label) == "" {
		return fmt.Errorf("user id and label are required")
	}
	if u.Role == "" {
		u.Role = RoleMember
	}
	if !validRole(u.Role) {
		return fmt.Errorf("invalid role %q", u.Role)
	}
	allowlist := strings.Join(u.ModelAllowlist, ",")
	_, err := s.db.Exec(`
		INSERT INTO users (id, label, role, created_at, max_requests_per_minute,
		                   max_tokens_per_month, max_spend_usd_per_month, model_allowlist)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Label, u.Role, time.Now().UTC().Format(time.RFC3339),
		u.MaxRequestsPerMinute, u.MaxTokensPerMonth, u.MaxSpendUSDPerMonth, allowlist,
	)
	return err
}

// ListUsers returns every user, newest last.
func (s *KeyStore) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`
		SELECT id, label, role, disabled_at, created_at, max_requests_per_minute,
		       max_tokens_per_month, max_spend_usd_per_month, model_allowlist
		FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var disabled, created, allowlist sql.NullString
		if err := rows.Scan(&u.ID, &u.Label, &u.Role, &disabled, &created,
			&u.MaxRequestsPerMinute, &u.MaxTokensPerMonth, &u.MaxSpendUSDPerMonth, &allowlist); err != nil {
			return nil, err
		}
		u.DisabledAt = parseTime(disabled)
		if t := parseTime(created); t != nil {
			u.CreatedAt = *t
		}
		if allowlist.String != "" {
			u.ModelAllowlist = strings.Split(allowlist.String, ",")
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// SetUserDisabled enables or disables a user. Disabling does not revoke keys;
// authentication checks the user's disabled_at on every request.
func (s *KeyStore) SetUserDisabled(id string, disabled bool) error {
	var value any
	if disabled {
		value = time.Now().UTC().Format(time.RFC3339)
	}
	res, err := s.db.Exec(`UPDATE users SET disabled_at = ? WHERE id = ?`, value, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user %q not found", id)
	}
	return nil
}

// CreateKey mints a key for userID and returns the raw key ONCE.
func (s *KeyStore) CreateKey(userID string, scopes []string, ttl *time.Duration) (string, APIKey, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT count(*) FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		return "", APIKey{}, err
	}
	if exists == 0 {
		return "", APIKey{}, fmt.Errorf("user %q not found", userID)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", APIKey{}, fmt.Errorf("generate key: %w", err)
	}
	raw := "cond_" + base64.RawURLEncoding.EncodeToString(buf)
	now := time.Now().UTC()
	id := "key_" + base64.RawURLEncoding.EncodeToString(buf[:9])

	var expiresAt *string
	if ttl != nil && *ttl > 0 {
		value := now.Add(*ttl).Format(time.RFC3339)
		expiresAt = &value
	}
	scopesJSON, _ := json.Marshal(scopes)
	prefix := raw[:13] // "cond_" + 8 characters

	_, err := s.db.Exec(`
		INSERT INTO api_keys (id, user_id, token_hash, prefix, scopes, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, userID, hashToken(raw), prefix, string(scopesJSON), now.Format(time.RFC3339), expiresAt,
	)
	if err != nil {
		return "", APIKey{}, err
	}
	key := APIKey{ID: id, UserID: userID, Prefix: prefix, Scopes: scopes, CreatedAt: now}
	if expiresAt != nil {
		if t := parseTime(sql.NullString{String: *expiresAt, Valid: true}); t != nil {
			key.ExpiresAt = t
		}
	}
	return raw, key, nil
}

// ListKeys returns the keys of one user, or every key when userID is empty.
func (s *KeyStore) ListKeys(userID string) ([]APIKey, error) {
	const allKeys = `SELECT id, user_id, prefix, scopes, created_at, last_used_at, expires_at, revoked_at
	                  FROM api_keys ORDER BY created_at`
	const userKeys = `SELECT id, user_id, prefix, scopes, created_at, last_used_at, expires_at, revoked_at
	                  FROM api_keys WHERE user_id = ? ORDER BY created_at`

	var rows *sql.Rows
	var err error
	if userID == "" {
		rows, err = s.db.Query(allKeys)
	} else {
		rows, err = s.db.Query(userKeys, userID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var scopesJSON, created sql.NullString
		var used, expires, revoked sql.NullString
		if err := rows.Scan(&k.ID, &k.UserID, &k.Prefix, &scopesJSON, &created, &used, &expires, &revoked); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopesJSON.String), &k.Scopes)
		if t := parseTime(created); t != nil {
			k.CreatedAt = *t
		}
		k.LastUsedAt = parseTime(used)
		k.ExpiresAt = parseTime(expires)
		k.RevokedAt = parseTime(revoked)
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// RevokeKey marks a key unusable. Unknown ids are not an error (idempotent).
func (s *KeyStore) RevokeKey(id string) error {
	_, err := s.db.Exec(`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// Authenticate resolves a raw key to a Principal.
func (s *KeyStore) Authenticate(raw string) (*Principal, error) {
	if s == nil || s.db == nil {
		return nil, ErrTokenNotFound
	}
	var id, userID, scopesJSON, role string
	var disabled, expires, revoked sql.NullString
	err := s.db.QueryRow(`
		SELECT k.id, k.user_id, k.scopes, u.role, u.disabled_at, k.expires_at, k.revoked_at
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.token_hash = ?`, hashToken(raw)).
		Scan(&id, &userID, &scopesJSON, &role, &disabled, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	if disabled.Valid && disabled.String != "" {
		return nil, ErrTokenRevoked
	}
	if revoked.Valid && revoked.String != "" {
		return nil, ErrTokenRevoked
	}
	if expires.Valid && expires.String != "" {
		if t := parseTime(expires); t != nil && time.Now().After(*t) {
			return nil, ErrTokenExpired
		}
	}

	var scopes []string
	if scopesJSON != "" {
		_ = json.Unmarshal([]byte(scopesJSON), &scopes)
	}
	// Best-effort, at most once a minute: keeps SQLite writes off the hot path.
	cutoff := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	_, _ = s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		time.Now().UTC().Format(time.RFC3339), id, cutoff)

	return &Principal{UserID: userID, KeyID: id, Role: role, Scopes: scopes}, nil
}
