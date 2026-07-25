package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// DefaultKeyBytes is the number of random bytes used for a generated gateway API key.
const DefaultKeyBytes = 32

// Generate creates a cryptographically random gateway API key (hex-encoded).
func Generate() (string, error) {
	return GenerateN(DefaultKeyBytes)
}

// GenerateN creates a cryptographically random hex-encoded key of n random bytes.
func GenerateN(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("key byte length must be positive, got %d", n)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
