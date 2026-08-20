package middleware

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/EffNine/conductor/internal/auth"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const authTestKey = "test-secret-key"

// authTestApp builds an app with the real Auth middleware and two endpoints:
// a protected one and the unprotected /health probe.
func authTestApp(service *auth.Service) *fiber.App {
	app := fiber.New()
	app.Use(Auth(service))
	app.Get("/v1/models", func(c *fiber.Ctx) error {
		return c.SendString("models")
	})
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

// doAuthRequest runs a GET against the app and returns (status, body,
// WWW-Authenticate header).
func doAuthRequest(t *testing.T, app *fiber.App, path string, header string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body), resp.Header.Get("WWW-Authenticate")
}

func authErrorCode(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))
	return envelope.Error.Code
}

func TestAuth_MissingAuthorization(t *testing.T) {
	app := authTestApp(auth.NewService(authTestKey))
	status, body, wwwAuth := doAuthRequest(t, app, "/v1/models", "")
	assert.Equal(t, 401, status)
	assert.Equal(t, "Bearer", wwwAuth)
	assert.Equal(t, "missing_api_key", authErrorCode(t, body))
}

func TestAuth_MalformedAuthorization(t *testing.T) {
	app := authTestApp(auth.NewService(authTestKey))

	malformed := []struct {
		name   string
		header string
	}{
		{"basic scheme", "Basic " + authTestKey},
		{"other scheme", "Key " + authTestKey},
		{"bearer only", "Bearer"},
		{"bearer no space", "Bearer" + authTestKey},
		{"extra token", "Bearer " + authTestKey + " extra"},
		{"bare key without scheme", authTestKey},
		{"empty credential", "Bearer "},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			status, body, wwwAuth := doAuthRequest(t, app, "/v1/models", tt.header)
			assert.Equal(t, 401, status, "header %q", tt.header)
			assert.Equal(t, "Bearer", wwwAuth)
			assert.Equal(t, "invalid_api_key", authErrorCode(t, body))
		})
	}
}

func TestAuth_CaseInsensitiveScheme(t *testing.T) {
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		app := authTestApp(auth.NewService(authTestKey))
		status, _, _ := doAuthRequest(t, app, "/v1/models", scheme+" "+authTestKey)
		assert.Equal(t, 200, status, "scheme %q", scheme)
	}
}

func TestAuth_InvalidKey(t *testing.T) {
	app := authTestApp(auth.NewService(authTestKey))
	status, body, wwwAuth := doAuthRequest(t, app, "/v1/models", "Bearer wrong-key")
	assert.Equal(t, 401, status)
	assert.Equal(t, "Bearer", wwwAuth)
	assert.Equal(t, "invalid_api_key", authErrorCode(t, body))
}

func TestAuth_ValidKey(t *testing.T) {
	app := authTestApp(auth.NewService(authTestKey))
	status, _, _ := doAuthRequest(t, app, "/v1/models", "Bearer "+authTestKey)
	assert.Equal(t, 200, status)
}

func TestAuth_HealthEndpointOpen(t *testing.T) {
	app := authTestApp(auth.NewService(authTestKey))
	for _, header := range []string{"", "Bearer " + authTestKey} {
		status, _, _ := doAuthRequest(t, app, "/health", header)
		assert.Equal(t, 200, status, "header %q", header)
	}
}

func TestAuth_ProtectedEndpointWithoutKey(t *testing.T) {
	app := authTestApp(auth.NewService(authTestKey))
	for _, path := range []string{"/v1/models", "/api/usage", "/v1/chat/completions"} {
		status, _, _ := doAuthRequest(t, app, path, "")
		assert.Equal(t, 401, status, "path %q", path)
	}
}

func TestAuth_NotConfiguredFailsClosed(t *testing.T) {
	app := authTestApp(auth.NewService(""))
	status, _, _ := doAuthRequest(t, app, "/v1/models", "Bearer anything")
	assert.Equal(t, 401, status)
	status, _, _ = doAuthRequest(t, app, "/v1/models", "")
	assert.Equal(t, 401, status)
}

func TestAuth_NoKeyLeakageInResponse(t *testing.T) {
	app := authTestApp(auth.NewService(authTestKey))
	status, body, _ := doAuthRequest(t, app, "/v1/models", "Bearer leaked-attempt-credential-12345")
	assert.Equal(t, 401, status)
	assert.NotContains(t, body, authTestKey, "response must not contain the configured key")
	assert.NotContains(t, body, "leaked-attempt-credential-12345", "response must not echo the provided credential")
	assert.NotContains(t, body, "Authorization", "response must not echo the header name")
}
