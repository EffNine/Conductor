package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
)

// Sentinel errors returned by Service.Authenticate. Their messages are
// deliberately generic and never include the configured key or the provided
// credential.
var (
	// ErrNotConfigured is returned when the service holds no API key.
	ErrNotConfigured = errors.New("gateway API key not configured")
	// ErrInvalidAPIKey is returned when the provided credential does not
	// match the configured key.
	ErrInvalidAPIKey = errors.New("invalid API key")
	// ErrInsufficientScope is returned when a valid token lacks a required scope.
	ErrInsufficientScope = errors.New("insufficient scope")
	// ErrTokenRevoked is returned when the token has been revoked.
	ErrTokenRevoked = errors.New("token revoked")
	// ErrTokenExpired is returned when the token has expired.
	ErrTokenExpired = errors.New("token expired")
	// ErrTokenNotFound is returned when the token hash is not in the store.
	ErrTokenNotFound = errors.New("not found")
	// ErrWrongService is returned when the token belongs to a different service.
	ErrWrongService = errors.New("wrong_service")
)

// Service handles API key authentication.
// When a TokenStore is attached, Authenticate first tries the control-plane
// service_token table (service=conductor) before falling back to the
// single gateway key. This gives per-token revocation, expiry and scope
// without breaking single-key deployments.
type Service struct {
	apiKey     string
	tokenStore *TokenStore
}

// NewService creates a new auth service with single-key only.
func NewService(apiKey string) *Service {
	return &Service{
		apiKey: apiKey,
	}
}

// NewServiceWithStore creates an auth service that also checks the control-plane token store.
func NewServiceWithStore(apiKey string, store *TokenStore) *Service {
	return &Service{
		apiKey:     apiKey,
		tokenStore: store,
	}
}

// SetTokenStore attaches a store after construction (used by tests).
func (s *Service) SetTokenStore(ts *TokenStore) {
	s.tokenStore = ts
}

// Authenticate validates a provided API key.
//
// If a TokenStore is configured, the method first looks up the token by
// SHA256 hash in the control-plane DB (service=conductor). A matching,
// non-revoked, non-expired token succeeds immediately without requiring the
// single gateway key. This provides per-token lifecycle while preserving
// backward compatibility with the static key.
//
// Both the configured key and the provided credential are reduced to a
// SHA-256 digest before comparison, so the single-key fallback runs in
// constant time regardless of key length (digests are always 32 bytes).
// The raw key or credential never appears in any returned error.
func (s *Service) Authenticate(providedKey string) error {
	// Try token store first (service-token path)
	if s.tokenStore != nil {
		if _, _, err := s.tokenStore.Verify(providedKey, nil); err == nil {
			return nil
		} else {
			// revoked/expired are auth failures (401), not scope failures.
			if errors.Is(err, ErrTokenRevoked) || errors.Is(err, ErrTokenExpired) {
				return ErrInvalidAPIKey
			}
			if errors.Is(err, ErrInsufficientScope) {
				return ErrInsufficientScope
			}
			// not found / wrong_service fall through to single-key check; other errors are invalid.
			if errors.Is(err, ErrTokenNotFound) || errors.Is(err, ErrWrongService) {
				// fall through
			} else {
				return ErrInvalidAPIKey
			}
		}
	}

	if s.apiKey == "" {
		return ErrNotConfigured
	}

	if subtle.ConstantTimeCompare(hashKey(s.apiKey), hashKey(providedKey)) != 1 {
		return ErrInvalidAPIKey
	}

	return nil
}

// AuthenticateWithScopes validates that the provided key is valid and carries one of the required scopes.
// Required scopes are evaluated only for token-store tokens; single-key fallback is treated as conductor:admin (full).
func (s *Service) AuthenticateWithScopes(providedKey string, requiredScopes []string) error {
	if s.tokenStore != nil {
		if _, _, err := s.tokenStore.Verify(providedKey, requiredScopes); err == nil {
			return nil
		} else if errors.Is(err, ErrInsufficientScope) {
			return ErrInsufficientScope
		} else if errors.Is(err, ErrTokenRevoked) || errors.Is(err, ErrTokenExpired) {
			return ErrInvalidAPIKey
		} else if errors.Is(err, ErrTokenNotFound) || errors.Is(err, ErrWrongService) {
			// fall through to single-key
		} else {
			return ErrInvalidAPIKey
		}
	}
	// single-key is admin-equivalent for backward compat — it satisfies any scope
	if s.apiKey == "" {
		return ErrNotConfigured
	}
	if subtle.ConstantTimeCompare(hashKey(s.apiKey), hashKey(providedKey)) != 1 {
		return ErrInvalidAPIKey
	}
	return nil
}

// ScopesForToken returns the scopes associated with a token, or nil for single-key.
// Used to store scopes in request context after authentication.
func (s *Service) ScopesForToken(providedKey string) []string {
	if s.tokenStore != nil {
		scopes, _, err := s.tokenStore.Verify(providedKey, nil)
		if err == nil {
			return scopes
		}
	}
	// single-key is admin
	if s.apiKey != "" && subtle.ConstantTimeCompare(hashKey(s.apiKey), hashKey(providedKey)) == 1 {
		return []string{"conductor:admin", "conductor:read", "conductor:execute"}
	}
	return nil
}

// IsConfigured returns true if an API key is set.
func (s *Service) IsConfigured() bool {
	return s.apiKey != ""
}

// hashKey returns the SHA-256 digest of a key. Fixed-length digests keep the
// subsequent constant-time comparison independent of the raw key length.
func hashKey(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}
