package auth

import (
	"encoding/hex"
	"testing"
)

func TestGenerate_LengthAndHex(t *testing.T) {
	key, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	wantLen := DefaultKeyBytes * 2 // hex encoding
	if len(key) != wantLen {
		t.Fatalf("len(key) = %d, want %d", len(key), wantLen)
	}
	if _, err := hex.DecodeString(key); err != nil {
		t.Fatalf("key is not valid hex: %v", err)
	}
}

func TestGenerate_Unique(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if a == b {
		t.Fatal("expected two Generate() calls to return different keys")
	}
}

func TestGenerate_NonEmpty(t *testing.T) {
	key, err := Generate()
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if key == "" {
		t.Fatal("expected non-empty key")
	}
}

func TestGenerateN_Invalid(t *testing.T) {
	if _, err := GenerateN(0); err == nil {
		t.Fatal("expected error for n=0")
	}
	if _, err := GenerateN(-1); err == nil {
		t.Fatal("expected error for n=-1")
	}
}
