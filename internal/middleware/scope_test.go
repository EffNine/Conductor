package middleware

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EffNine/conductor/internal/auth"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

func setupScopeApp(t *testing.T) (*fiber.App, *auth.Service, func(service, name string, scopes []string, expiresAt *string, revoked bool) string) {
	t.Helper()
	tmp := t.TempDir() + "/scope.db"
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL", tmp))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE service_token (id TEXT PRIMARY KEY, service TEXT NOT NULL, name TEXT NOT NULL, description TEXT, token_hash TEXT NOT NULL UNIQUE, token_prefix TEXT NOT NULL, token_fingerprint TEXT NOT NULL, scopes TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT, last_used_at TEXT, revoked_at TEXT, created_by TEXT)`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	create := func(service, name string, scopes []string, expiresAt *string, revoked bool) string {
		raw := "cond_" + uuid.NewString() + uuid.NewString()
		sum := sha256.Sum256([]byte(raw))
		hash := hex.EncodeToString(sum[:])
		prefix := raw[:8]
		fp := hash[:16]
		scopesJSON, _ := json.Marshal(scopes)
		now := time.Now().UTC().Format(time.RFC3339)
		var exp *string
		if expiresAt != nil {
			exp = expiresAt
		}
		var revokedAt *string
		if revoked {
			s := time.Now().Format(time.RFC3339)
			revokedAt = &s
		}
		_, err := db.Exec(`INSERT INTO service_token (id, service, name, token_hash, token_prefix, token_fingerprint, scopes, created_at, expires_at, revoked_at, created_by) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			uuid.NewString(), service, name, hash, prefix, fp, string(scopesJSON), now, exp, revokedAt, "admin")
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		return raw
	}
	t.Setenv("EFFNINE_DB_PATH", tmp)
	t.Setenv("CONDUCTOR_CONTROL_PLANE_DB", tmp)
	store, err := auth.NewTokenStore(tmp)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = db.Close() })
	svc := auth.NewServiceWithStore("single-key-fallback", store)

	app := fiber.New()
	app.Use(SecurityHeaders())
	app.Use(Auth(svc))
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/api/models", RequireScope("conductor:read"), func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	app.Post("/v1/chat/completions", RequireScope("conductor:execute"), func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	app.Post("/api/models/force-probe", RequireScope("conductor:admin"), func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })

	return app, svc, create
}

func TestScope_ReadTokenCanReadButNotExecute(t *testing.T) {
	app, _, create := setupScopeApp(t)
	readToken := create("conductor", "read", []string{"conductor:read"}, nil, false)

	req := httptest.NewRequest("GET", "/api/models", nil)
	req.Header.Set("Authorization", "Bearer "+readToken)
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("read token read endpoint: want 200 got %d", resp.StatusCode)
	}
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
	req2.Header.Set("Authorization", "Bearer "+readToken)
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := app.Test(req2)
	if resp2.StatusCode != 403 {
		t.Fatalf("read token execute endpoint: want 403 got %d", resp2.StatusCode)
	}
	req3 := httptest.NewRequest("POST", "/api/models/force-probe", nil)
	req3.Header.Set("Authorization", "Bearer "+readToken)
	resp3, _ := app.Test(req3)
	if resp3.StatusCode != 403 {
		t.Fatalf("read token admin endpoint: want 403 got %d", resp3.StatusCode)
	}
}

func TestScope_ExecuteTokenCanExecuteButNotAdmin(t *testing.T) {
	app, _, create := setupScopeApp(t)
	execToken := create("conductor", "exec", []string{"conductor:execute"}, nil, false)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+execToken)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("exec token execute: want 200 got %d", resp.StatusCode)
	}
	req2 := httptest.NewRequest("POST", "/api/models/force-probe", nil)
	req2.Header.Set("Authorization", "Bearer "+execToken)
	resp2, _ := app.Test(req2)
	if resp2.StatusCode != 403 {
		t.Fatalf("exec token admin: want 403 got %d", resp2.StatusCode)
	}
}

func TestScope_AdminTokenCanAll(t *testing.T) {
	app, _, create := setupScopeApp(t)
	adminToken := create("conductor", "admin", []string{"conductor:admin"}, nil, false)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/models"},
		{"POST", "/v1/chat/completions"},
		{"POST", "/api/models/force-probe"},
	} {
		var req *http.Request
		if tc.method == "GET" {
			req = httptest.NewRequest(tc.method, tc.path, nil)
		} else {
			req = httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte(`{}`)))
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+adminToken)
		resp, _ := app.Test(req)
		if resp.StatusCode != 200 {
			t.Fatalf("admin token %s %s: want 200 got %d", tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestScope_WrongServiceRejected(t *testing.T) {
	app, _, create := setupScopeApp(t)
	mct := create("mycontext", "myctx", []string{"mycontext:read"}, nil, false)
	req := httptest.NewRequest("GET", "/api/models", nil)
	req.Header.Set("Authorization", "Bearer "+mct)
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("wrong service token: want 401 got %d", resp.StatusCode)
	}
}

func TestScope_RevokedAndExpiredRejected(t *testing.T) {
	app, _, create := setupScopeApp(t)
	past := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	expired := create("conductor", "expired", []string{"conductor:read"}, &past, false)
	req := httptest.NewRequest("GET", "/api/models", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expired token: want 401 got %d", resp.StatusCode)
	}
	revoked := create("conductor", "revoked", []string{"conductor:read"}, nil, true)
	req2 := httptest.NewRequest("GET", "/api/models", nil)
	req2.Header.Set("Authorization", "Bearer "+revoked)
	resp2, _ := app.Test(req2)
	if resp2.StatusCode != 401 {
		t.Fatalf("revoked token: want 401 got %d", resp2.StatusCode)
	}
}

// TestScope_RouteRegisteredBeforeAuthFailsClosed pins the S1 class of bug: a
// route registered before the auth middleware never runs it, and RequireScope
// used to treat the absent scope as single-key admin and allow the request.
// /api/jev-shadow was registered before middleware.Register (fixed 2026-10-28).
func TestScope_RouteRegisteredBeforeAuthFailsClosed(t *testing.T) {
	app := fiber.New()
	// Mirror the production wiring: an auth-wired app whose route was
	// registered before Auth.
	authRequired.Store(true)
	t.Cleanup(func() { authRequired.Store(false) })
	app.Get("/api/jev-shadow", RequireScope("conductor:read"), func(c *fiber.Ctx) error {
		return c.SendString("leaked")
	})
	app.Use(Auth(auth.NewService("single-key")))

	req := httptest.NewRequest("GET", "/api/jev-shadow", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("route registered before Auth: want 401, got %d", resp.StatusCode)
	}
}

// TestScope_SingleKeyIsAdminScoped pins that single-key mode keeps its
// documented admin-equivalence now that RequireScope fails closed without an
// authenticated marker.
func TestScope_SingleKeyIsAdminScoped(t *testing.T) {
	app := fiber.New()
	app.Use(Auth(auth.NewService("single-key")))
	app.Get("/api/models/force-probe", RequireScope("conductor:admin"), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/api/models/force-probe", nil)
	req.Header.Set("Authorization", "Bearer single-key")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("single-key admin: want 200, got %d", resp.StatusCode)
	}

	anon := httptest.NewRequest("GET", "/api/models/force-probe", nil)
	resp2, err := app.Test(anon)
	if err != nil {
		t.Fatalf("app.Test anon: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("single-key anon: want 401, got %d", resp2.StatusCode)
	}
}
