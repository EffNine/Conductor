package provider

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	future := now.Add(45 * time.Second).UTC().Format(http.TimeFormat)
	past := now.Add(-45 * time.Second).UTC().Format(http.TimeFormat)

	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "30", 30 * time.Second},
		{"seconds with plus", "+120", 120 * time.Second},
		{"zero seconds", "0", 0},
		{"negative seconds", "-5", 0},
		{"garbage", "soon-ish", 0},
		{"overflow", "99999999999999999999", 0},
		{"http date future", future, 45 * time.Second},
		{"http date past", past, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRetryAfter(tt.header, now)
			if got != tt.want {
				t.Fatalf("ParseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestWithRetryAfter(t *testing.T) {
	pe := NewProviderError("p", 429, ErrorTypeRateLimit, "slow down", nil)
	if pe.RetryAfter != 0 {
		t.Fatalf("default RetryAfter = %v, want 0", pe.RetryAfter)
	}

	returned := pe.WithRetryAfter(15 * time.Second)
	if returned != pe || pe.RetryAfter != 15*time.Second {
		t.Fatalf("WithRetryAfter did not attach hint: %+v", pe)
	}

	pe.WithRetryAfter(0)
	pe.WithRetryAfter(-time.Second)
	if pe.RetryAfter != 15*time.Second {
		t.Fatalf("non-positive hints must be ignored, got %v", pe.RetryAfter)
	}
}
