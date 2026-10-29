package auth

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func testKeyStore(t *testing.T) *KeyStore {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/keys.db?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	schema := []string{
		`CREATE TABLE users (
			id TEXT PRIMARY KEY, label TEXT NOT NULL, role TEXT NOT NULL,
			disabled_at DATETIME, created_at DATETIME NOT NULL,
			max_requests_per_minute INTEGER, max_tokens_per_month INTEGER,
			max_spend_usd_per_month REAL, model_allowlist TEXT)`,
		`CREATE TABLE api_keys (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
			prefix TEXT NOT NULL, scopes TEXT NOT NULL, created_at DATETIME NOT NULL,
			last_used_at DATETIME, expires_at DATETIME, revoked_at DATETIME)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return NewKeyStore(db)
}

func TestKeyStoreIssueAndAuthenticate(t *testing.T) {
	ks := testKeyStore(t)
	if err := ks.CreateUser(User{ID: "alice", Label: "Alice", Role: RoleMember}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw, key, err := ks.CreateKey("alice", []string{"conductor:read", "conductor:execute"}, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if raw == "" || key.Prefix == "" {
		t.Fatal("empty key material")
	}
	if !strings.HasPrefix(raw, key.Prefix) || len(raw) == len(key.Prefix) {
		t.Fatal("prefix must be a strict prefix of the key")
	}

	p, err := ks.Authenticate(raw)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if p.UserID != "alice" || p.Role != RoleMember || !p.HasScope("conductor:execute") {
		t.Fatalf("principal = %+v", p)
	}
	if p.IsAdmin() {
		t.Fatal("member must not be admin")
	}

	if _, err := ks.Authenticate("cond_" + raw); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("wrong key error = %v, want ErrTokenNotFound", err)
	}
}

func TestKeyStoreRevokedExpiredDisabled(t *testing.T) {
	ks := testKeyStore(t)
	if err := ks.CreateUser(User{ID: "bob", Label: "Bob", Role: RoleMember}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw, key, err := ks.CreateKey("bob", []string{"conductor:read"}, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if err := ks.RevokeKey(key.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := ks.Authenticate(raw); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("revoked key error = %v, want ErrTokenRevoked", err)
	}

	if err := ks.CreateUser(User{ID: "carol", Label: "Carol", Role: RoleMember}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	ttl := time.Millisecond
	rawExpired, _, err := ks.CreateKey("carol", []string{"conductor:read"}, &ttl)
	if err != nil {
		t.Fatalf("create expiring key: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := ks.Authenticate(rawExpired); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired key error = %v, want ErrTokenExpired", err)
	}

	if err := ks.CreateUser(User{ID: "dave", Label: "Dave", Role: RoleMember}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	rawDisabled, _, err := ks.CreateKey("dave", []string{"conductor:read"}, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if err := ks.SetUserDisabled("dave", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := ks.Authenticate(rawDisabled); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("disabled user key error = %v, want ErrTokenRevoked", err)
	}
}

func TestKeyStoreRolesAreValidated(t *testing.T) {
	ks := testKeyStore(t)
	if err := ks.CreateUser(User{ID: "eve", Label: "Eve", Role: "root"}); err == nil {
		t.Fatal("invalid role was accepted")
	}
	if err := ks.CreateUser(User{ID: "", Label: "No id"}); err == nil {
		t.Fatal("empty id was accepted")
	}
	if _, _, err := ks.CreateKey("missing", []string{"conductor:read"}, nil); err == nil {
		t.Fatal("key for unknown user was accepted")
	}
}

func TestServicePrincipalOrder(t *testing.T) {
	ks := testKeyStore(t)
	if err := ks.CreateUser(User{ID: "alice", Label: "Alice", Role: RoleMember}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw, _, err := ks.CreateKey("alice", []string{"conductor:read"}, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	svc := NewService("static-key")
	svc.SetKeyStore(ks)

	p, err := svc.AuthenticatePrincipal(raw)
	if err != nil || p.UserID != "alice" {
		t.Fatalf("local key principal = %+v, err = %v", p, err)
	}

	p, err = svc.AuthenticatePrincipal("static-key")
	if err != nil || p.UserID != "default" || !p.IsAdmin() {
		t.Fatalf("static key principal = %+v, err = %v", p, err)
	}

	if _, err := svc.AuthenticatePrincipal("nope"); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("unknown credential error = %v, want ErrInvalidAPIKey", err)
	}
}
