package auth

import (
	"errors"
	"strings"
	"testing"
)

const testKey = "64-hex-char-test-key-0123456789abcdef"

func TestAuthenticate_ValidKey(t *testing.T) {
	s := NewService(testKey)
	if err := s.Authenticate(testKey); err != nil {
		t.Fatalf("Authenticate(valid) error: %v", err)
	}
}

func TestAuthenticate_InvalidKey(t *testing.T) {
	s := NewService(testKey)
	if err := s.Authenticate("wrong-key"); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("Authenticate(wrong) = %v, want ErrInvalidAPIKey", err)
	}
}

func TestAuthenticate_EmptyProvidedKey(t *testing.T) {
	s := NewService(testKey)
	if err := s.Authenticate(""); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("Authenticate(empty) = %v, want ErrInvalidAPIKey", err)
	}
}

func TestAuthenticate_NotConfigured(t *testing.T) {
	s := NewService("")
	if err := s.Authenticate(testKey); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Authenticate = %v, want ErrNotConfigured", err)
	}
}

func TestAuthenticate_DoesNotLeakKeyInErrors(t *testing.T) {
	s := NewService(testKey)
	for _, provided := range []string{"", "wrong-key", testKey + "-suffix", strings.Repeat("x", 128)} {
		err := s.Authenticate(provided)
		if err == nil {
			t.Fatal("expected an error for non-matching key")
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("error leaks configured key: %q", err.Error())
		}
		if provided != "" && strings.Contains(err.Error(), provided) {
			t.Fatalf("error leaks provided credential: %q", err.Error())
		}
	}
}

func TestAuthenticate_DifferentLengthsRejected(t *testing.T) {
	s := NewService(testKey)
	for _, provided := range []string{testKey[:1], testKey[:32], testKey + "extra", strings.Repeat("a", 200)} {
		if err := s.Authenticate(provided); !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("Authenticate(len=%d) = %v, want ErrInvalidAPIKey", len(provided), err)
		}
	}
}

func TestIsConfigured(t *testing.T) {
	if NewService("").IsConfigured() {
		t.Fatal("IsConfigured() = true for empty key")
	}
	if !NewService("key").IsConfigured() {
		t.Fatal("IsConfigured() = false for configured key")
	}
}
