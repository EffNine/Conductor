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
)

// Service handles API key authentication.
type Service struct {
	apiKey string
}

// NewService creates a new auth service.
func NewService(apiKey string) *Service {
	return &Service{
		apiKey: apiKey,
	}
}

// Authenticate validates a provided API key.
//
// Both the configured key and the provided credential are reduced to a
// SHA-256 digest before comparison, so the comparison runs in constant time
// regardless of key length (digests are always 32 bytes). The raw key or
// credential never appears in any returned error.
func (s *Service) Authenticate(providedKey string) error {
	if s.apiKey == "" {
		return ErrNotConfigured
	}

	if subtle.ConstantTimeCompare(hashKey(s.apiKey), hashKey(providedKey)) != 1 {
		return ErrInvalidAPIKey
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
